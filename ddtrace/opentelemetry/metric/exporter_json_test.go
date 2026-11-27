// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package metric

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// capturedRequest is what a test collector received.
type capturedRequest struct {
	header http.Header
	path   string
	body   []byte
}

// newCollector starts a test collector that records requests and answers with status(callNumber).
func newCollector(t *testing.T, status func(call int) int) (url string, requests chan capturedRequest) {
	t.Helper()
	requests = make(chan capturedRequest, 10)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- capturedRequest{header: r.Header, path: r.URL.Path, body: body}
		if status != nil {
			w.WriteHeader(status(int(calls.Add(1))))
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, requests
}

func useJSONProtocol(t *testing.T, endpoint string) {
	t.Helper()
	t.Cleanup(func() { internalconfig.CreateNew() })
	t.Setenv("DD_METRICS_OTEL_ENABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "http/json")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", endpoint)
	internalconfig.CreateNew()
}

func oneMetric() *metricdata.ResourceMetrics {
	return &metricdata.ResourceMetrics{ScopeMetrics: []metricdata.ScopeMetrics{{
		Metrics: []metricdata.Metrics{{
			Name: "requests",
			Data: metricdata.Sum[int64]{
				Temporality: metricdata.DeltaTemporality,
				DataPoints:  []metricdata.DataPoint[int64]{{Value: 3}},
			},
		}},
	}}}
}

func TestJSONExporter(t *testing.T) {
	t.Run("sends application/json to the configured endpoint with the configured headers", func(t *testing.T) {
		url, requests := newCollector(t, nil)
		useJSONProtocol(t, url+"/v1/metrics")
		t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", "api-key=secret%20value")
		internalconfig.CreateNew()

		exp, err := newDatadogOTLPExporter(t.Context(), nil, nil, nil)
		require.NoError(t, err)
		defer exp.Shutdown(t.Context())
		require.NoError(t, exp.Export(t.Context(), oneMetric()))

		r := <-requests
		assert.Equal(t, "/v1/metrics", r.path)
		assert.Equal(t, "application/json", r.header.Get("Content-Type"))
		assert.Equal(t, "secret value", r.header.Get("api-key"))
		assert.Empty(t, r.header.Get("Content-Encoding"))
		req := pmetricotlp.NewExportRequest()
		require.NoError(t, req.UnmarshalJSON(r.body))
		assert.Equal(t, "requests", req.Metrics().ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Name())
	})

	t.Run("compresses the body when OTEL_EXPORTER_OTLP_COMPRESSION is gzip", func(t *testing.T) {
		url, requests := newCollector(t, nil)
		useJSONProtocol(t, url)
		t.Setenv("OTEL_EXPORTER_OTLP_COMPRESSION", "gzip")
		internalconfig.CreateNew()

		exp, err := newDatadogOTLPExporter(t.Context(), nil, nil, nil)
		require.NoError(t, err)
		defer exp.Shutdown(t.Context())
		require.NoError(t, exp.Export(t.Context(), oneMetric()))

		r := <-requests
		assert.Equal(t, "gzip", r.header.Get("Content-Encoding"))
		zr, err := gzip.NewReader(bytes.NewReader(r.body))
		require.NoError(t, err)
		decompressed, err := io.ReadAll(zr)
		require.NoError(t, err)
		require.NoError(t, pmetricotlp.NewExportRequest().UnmarshalJSON(decompressed))
	})

	t.Run("retries a 503", func(t *testing.T) {
		url, requests := newCollector(t, func(call int) int {
			if call == 1 {
				return http.StatusServiceUnavailable
			}
			return http.StatusOK
		})
		useJSONProtocol(t, url)

		exp, err := newDatadogOTLPExporter(t.Context(), nil, nil, nil)
		require.NoError(t, err)
		defer exp.Shutdown(t.Context())
		require.NoError(t, exp.Export(t.Context(), oneMetric()))

		assert.Len(t, requests, 2)
	})

	t.Run("uses the delta temporality default and honors a selector", func(t *testing.T) {
		url, _ := newCollector(t, nil)
		useJSONProtocol(t, url)

		def, err := newDatadogOTLPExporter(t.Context(), nil, nil, nil)
		require.NoError(t, err)
		assert.Equal(t, metricdata.DeltaTemporality, def.Temporality(sdkmetric.InstrumentKindCounter))

		cumulative, err := newDatadogOTLPExporter(t.Context(), nil, nil, cumulativeTemporalitySelector())
		require.NoError(t, err)
		assert.Equal(t, metricdata.CumulativeTemporality, cumulative.Temporality(sdkmetric.InstrumentKindCounter))
	})

	t.Run("reports the json encoding in telemetry", func(t *testing.T) {
		recorder := &telemetrytest.RecordClient{}
		defer telemetry.MockClient(recorder)()
		url, _ := newCollector(t, nil)
		useJSONProtocol(t, url)

		exp, err := newDatadogOTLPExporter(t.Context(), nil, nil, nil)
		require.NoError(t, err)
		defer exp.Shutdown(t.Context())
		require.NoError(t, exp.Export(t.Context(), oneMetric()))

		assert.Contains(t, recorder.Metrics, telemetrytest.MetricKey{
			Namespace: telemetry.NamespaceTracers,
			Name:      "otel.metrics_export_successes",
			Tags:      "encoding:json,protocol:http",
			Kind:      "count",
		})
	})
}

func TestJSONExporterWarnsAboutDroppedHTTPExporterOptions(t *testing.T) {
	const warning = "WithHTTPExporter options are ignored"
	warnHTTPOptionsIgnored = sync.Once{}
	newExporter := func(t *testing.T, httpOpts []otlpmetrichttp.Option, selector sdkmetric.TemporalitySelector) string {
		logger := new(log.RecordLogger)
		defer log.UseLogger(logger)()
		url, _ := newCollector(t, nil)
		useJSONProtocol(t, url)
		exp, err := newDatadogOTLPExporter(t.Context(), httpOpts, nil, selector)
		require.NoError(t, err)
		require.NoError(t, exp.Shutdown(t.Context()))
		return strings.Join(logger.Logs(), "\n")
	}

	t.Run("warns when the caller passes options", func(t *testing.T) {
		logs := newExporter(t, []otlpmetrichttp.Option{otlpmetrichttp.WithTimeout(time.Second)}, nil)
		assert.Contains(t, logs, warning)
	})

	t.Run("warns once", func(t *testing.T) {
		warnHTTPOptionsIgnored = sync.Once{}
		opts := []otlpmetrichttp.Option{otlpmetrichttp.WithTimeout(time.Second)}
		assert.Contains(t, newExporter(t, opts, nil), warning)
		assert.NotContains(t, newExporter(t, opts, nil), warning)
	})

	t.Run("does not warn for a temporality selector alone", func(t *testing.T) {
		logs := newExporter(t, nil, cumulativeTemporalitySelector())
		assert.NotContains(t, logs, warning)
	})
}

func TestJSONExporterHistogramAggregation(t *testing.T) {
	const warning = "Unsupported OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION"
	newExporter := func(t *testing.T, value string) (agg sdkmetric.Aggregation, logs string) {
		logger := new(log.RecordLogger)
		defer log.UseLogger(logger)()
		url, _ := newCollector(t, nil)
		useJSONProtocol(t, url)
		if value != "" {
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION", value)
			internalconfig.CreateNew()
		}
		exp := newJSONExporter(nil)
		defer exp.Shutdown(t.Context())
		return exp.Aggregation(sdkmetric.InstrumentKindHistogram), strings.Join(logger.Logs(), "\n")
	}

	t.Run("defaults to explicit bucket histograms", func(t *testing.T) {
		agg, logs := newExporter(t, "")
		assert.IsType(t, sdkmetric.AggregationExplicitBucketHistogram{}, agg)
		assert.NotContains(t, logs, warning)
	})

	t.Run("base2_exponential_bucket_histogram selects exponential histograms", func(t *testing.T) {
		agg, logs := newExporter(t, "base2_exponential_bucket_histogram")
		assert.Equal(t, sdkmetric.AggregationBase2ExponentialHistogram{MaxSize: 160, MaxScale: 20}, agg)
		assert.NotContains(t, logs, warning)
	})

	t.Run("an invalid value warns and falls back to explicit bucket histograms", func(t *testing.T) {
		agg, logs := newExporter(t, "bogus")
		assert.IsType(t, sdkmetric.AggregationExplicitBucketHistogram{}, agg)
		assert.Contains(t, logs, warning)
	})
}

func TestJSONExporterFailsAfterShutdown(t *testing.T) {
	url, requests := newCollector(t, nil)
	useJSONProtocol(t, url)
	exp := newJSONExporter(nil)
	require.NoError(t, exp.Shutdown(t.Context()))

	assert.ErrorIs(t, exp.Export(t.Context(), oneMetric()), errShutdown)
	assert.Empty(t, requests)
}

// metricWithPoints returns one sum metric holding n data points with distinct attributes.
func metricWithPoints(n int) *metricdata.ResourceMetrics {
	points := make([]metricdata.DataPoint[int64], n)
	for i := range points {
		points[i] = metricdata.DataPoint[int64]{Value: int64(i), Attributes: attribute.NewSet(attribute.Int("n", i))}
	}
	return &metricdata.ResourceMetrics{ScopeMetrics: []metricdata.ScopeMetrics{{
		Metrics: []metricdata.Metrics{{
			Name: "requests",
			Data: metricdata.Sum[int64]{Temporality: metricdata.DeltaTemporality, DataPoints: points},
		}},
	}}}
}

func BenchmarkJSONExport(b *testing.B) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer srv.Close()
	b.Cleanup(func() { internalconfig.CreateNew() })
	b.Setenv("DD_METRICS_OTEL_ENABLED", "true")
	b.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "http/json")
	b.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", srv.URL)
	internalconfig.CreateNew()
	exp := newJSONExporter(nil)
	defer exp.Shutdown(b.Context())
	rm := metricWithPoints(1000)

	b.ReportAllocs()
	for b.Loop() {
		if err := exp.Export(b.Context(), rm); err != nil {
			b.Fatal(err)
		}
	}
}

// TestProtobufExporterIsUnchanged guards the upstream path: http/protobuf must keep
// sending protobuf and keep honoring WithHTTPExporter even though http/json has its own exporter.
func TestProtobufExporterIsUnchanged(t *testing.T) {
	t.Cleanup(func() { internalconfig.CreateNew() })
	url, requests := newCollector(t, nil)
	t.Setenv("DD_METRICS_OTEL_ENABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", url)
	internalconfig.CreateNew()

	var userClientUsed atomic.Bool
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		userClientUsed.Store(true)
		return http.DefaultTransport.RoundTrip(r)
	})}
	exp, err := newDatadogOTLPExporter(t.Context(), []otlpmetrichttp.Option{otlpmetrichttp.WithHTTPClient(client)}, nil, nil)
	require.NoError(t, err)
	defer exp.Shutdown(t.Context())
	require.NoError(t, exp.Export(t.Context(), oneMetric()))

	assert.Equal(t, "application/x-protobuf", (<-requests).header.Get("Content-Type"))
	assert.True(t, userClientUsed.Load(), "WithHTTPExporter options must still reach the upstream exporter")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
