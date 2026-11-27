// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package otlpjson_test

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry/internal/otlpjson"
)

func sampleResourceMetrics() *metricdata.ResourceMetrics {
	start := time.Unix(1700000000, 1)
	now := start.Add(10 * time.Second)
	attrs := attribute.NewSet(attribute.String("host", "a"), attribute.Int64("shard", 3), attribute.StringSlice("tags", []string{"x", "y"}))
	traceID := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	spanID := []byte{9, 8, 7, 6, 5, 4, 3, 2}
	exemplarInt := metricdata.Exemplar[int64]{
		FilteredAttributes: []attribute.KeyValue{attribute.String("fa", "v")},
		Time:               now, Value: 5, SpanID: spanID, TraceID: traceID,
	}
	exemplarFloat := metricdata.Exemplar[float64]{Time: now, Value: 2.5, SpanID: spanID, TraceID: traceID}
	numInt := []metricdata.DataPoint[int64]{
		{Attributes: attrs, StartTime: start, Time: now, Value: 42, Exemplars: []metricdata.Exemplar[int64]{exemplarInt}},
		{StartTime: start, Time: now, Value: 0},
	}
	numFloat := []metricdata.DataPoint[float64]{
		{Attributes: attrs, StartTime: start, Time: now, Value: math.NaN(), Exemplars: []metricdata.Exemplar[float64]{exemplarFloat}},
		{StartTime: start, Time: now, Value: 1.25},
	}
	hist := func(min, max float64) metricdata.HistogramDataPoint[float64] {
		return metricdata.HistogramDataPoint[float64]{
			Attributes: attrs, StartTime: start, Time: now,
			Count: 7, Sum: 21.5, Bounds: []float64{1, 5, 10}, BucketCounts: []uint64{1, 2, 3, 1},
			Min: metricdata.NewExtrema(min), Max: metricdata.NewExtrema(max),
			Exemplars: []metricdata.Exemplar[float64]{exemplarFloat},
		}
	}
	expo := metricdata.ExponentialHistogramDataPoint[float64]{
		Attributes: attrs, StartTime: start, Time: now,
		Count: 9, Sum: 100, Scale: -2, ZeroCount: 1,
		PositiveBucket: metricdata.ExponentialBucket{Offset: 3, Counts: []uint64{1, 2, 3}},
		NegativeBucket: metricdata.ExponentialBucket{Offset: -1, Counts: []uint64{2}},
		Min:            metricdata.NewExtrema(0.5), Max: metricdata.NewExtrema(64.0),
		Exemplars: []metricdata.Exemplar[float64]{exemplarFloat},
	}
	return &metricdata.ResourceMetrics{
		Resource: resource.NewWithAttributes("https://opentelemetry.io/schemas/1.26.0", attribute.String("service.name", "svc")),
		ScopeMetrics: []metricdata.ScopeMetrics{
			{
				Scope: instrumentation.Scope{Name: "scope-a", Version: "1.0", SchemaURL: "https://opentelemetry.io/schemas/1.27.0", Attributes: attribute.NewSet(attribute.String("sa", "v"))},
				Metrics: []metricdata.Metrics{
					{Name: "gauge.int", Description: "d", Unit: "1", Data: metricdata.Gauge[int64]{DataPoints: numInt}},
					{Name: "gauge.float", Data: metricdata.Gauge[float64]{DataPoints: numFloat}},
					{Name: "sum.int", Unit: "By", Data: metricdata.Sum[int64]{DataPoints: numInt, Temporality: metricdata.DeltaTemporality, IsMonotonic: true}},
					{Name: "sum.float", Data: metricdata.Sum[float64]{DataPoints: numFloat, Temporality: metricdata.CumulativeTemporality}},
					{Name: "hist.float", Data: metricdata.Histogram[float64]{DataPoints: []metricdata.HistogramDataPoint[float64]{hist(0.1, 9), {StartTime: start, Time: now}}, Temporality: metricdata.DeltaTemporality}},
					{Name: "hist.int", Data: metricdata.Histogram[int64]{DataPoints: []metricdata.HistogramDataPoint[int64]{{StartTime: start, Time: now, Count: 2, Sum: 10, Bounds: []float64{5}, BucketCounts: []uint64{1, 1}}}, Temporality: metricdata.CumulativeTemporality}},
					{Name: "expo.float", Data: metricdata.ExponentialHistogram[float64]{DataPoints: []metricdata.ExponentialHistogramDataPoint[float64]{expo}, Temporality: metricdata.DeltaTemporality}},
					{Name: "summary", Data: metricdata.Summary{DataPoints: []metricdata.SummaryDataPoint{{Attributes: attrs, StartTime: start, Time: now, Count: 4, Sum: 8, QuantileValues: []metricdata.QuantileValue{{Quantile: 0.5, Value: 2}, {Quantile: 1, Value: 4}}}}}},
				},
			},
			{
				Scope:   instrumentation.Scope{Name: "scope-b"},
				Metrics: []metricdata.Metrics{{Name: "plain", Data: metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{{Time: now, Value: -3}}}}},
			},
		},
	}
}

// TestEncodeMetricsMatchesUpstreamProtobuf is the drift guard for metrics: the JSON
// we write must decode to the same data as the protobuf the upstream exporter
// writes. It fails when an OpenTelemetry upgrade adds a field we do not encode.
func TestEncodeMetricsMatchesUpstreamProtobuf(t *testing.T) {
	rm := sampleResourceMetrics()

	jsonBody, err := otlpjson.EncodeMetrics(nil, rm)
	require.NoError(t, err)
	got := pmetricotlp.NewExportRequest()
	require.NoError(t, got.UnmarshalJSON(jsonBody))

	var protoBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protoBody, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()
	exp, err := otlpmetrichttp.New(context.Background(), otlpmetrichttp.WithEndpointURL(srv.URL+"/v1/metrics"))
	require.NoError(t, err)
	require.NoError(t, exp.Export(context.Background(), rm))
	require.NoError(t, exp.Shutdown(context.Background()))
	want := pmetricotlp.NewExportRequest()
	require.NoError(t, want.UnmarshalProto(protoBody))

	gotProto, err := got.MarshalProto()
	require.NoError(t, err)
	wantProto, err := want.MarshalProto()
	require.NoError(t, err)
	assert.Equal(t, wantProto, gotProto)
	assert.Equal(t, 9, got.Metrics().MetricCount())
}

func TestEncodeMetricsFollowsOTLPJSONRules(t *testing.T) {
	rm := &metricdata.ResourceMetrics{
		Resource: resource.Empty(),
		ScopeMetrics: []metricdata.ScopeMetrics{{
			Metrics: []metricdata.Metrics{{
				Name: "requests",
				Data: metricdata.Sum[int64]{
					Temporality: metricdata.DeltaTemporality,
					IsMonotonic: true,
					DataPoints:  []metricdata.DataPoint[int64]{{StartTime: time.Unix(0, 1), Time: time.Unix(0, 2), Value: 9}},
				},
			}},
		}},
	}
	body, err := otlpjson.EncodeMetrics(nil, rm)
	require.NoError(t, err)
	assert.JSONEq(t, `{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{
		"name":"requests",
		"sum":{
			"dataPoints":[{"startTimeUnixNano":"1","timeUnixNano":"2","asInt":"9"}],
			"aggregationTemporality":1,
			"isMonotonic":true
		}
	}]}]}]}`, string(body))
}

func TestEncodeMetricsDropsMetricsItCannotEncode(t *testing.T) {
	rm := &metricdata.ResourceMetrics{
		Resource: resource.Empty(),
		ScopeMetrics: []metricdata.ScopeMetrics{{
			Metrics: []metricdata.Metrics{
				{Name: "bad.temporality", Data: metricdata.Sum[int64]{Temporality: 0}},
				{Name: "bad.aggregation"},
				{Name: "good", Data: metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{{Value: 1}}}},
			},
		}},
	}
	body, err := otlpjson.EncodeMetrics(nil, rm)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad.temporality")
	req := pmetricotlp.NewExportRequest()
	require.NoError(t, req.UnmarshalJSON(body), "the request must stay valid JSON")
	require.Equal(t, 1, req.Metrics().MetricCount())
	assert.Equal(t, "good", req.Metrics().ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Name())
}

func BenchmarkEncodeMetrics(b *testing.B) {
	points := make([]metricdata.DataPoint[int64], 1000)
	for i := range points {
		points[i] = metricdata.DataPoint[int64]{Value: int64(i), Attributes: attribute.NewSet(attribute.Int("n", i))}
	}
	rm := &metricdata.ResourceMetrics{ScopeMetrics: []metricdata.ScopeMetrics{{
		Metrics: []metricdata.Metrics{{
			Name: "requests",
			Data: metricdata.Sum[int64]{Temporality: metricdata.DeltaTemporality, DataPoints: points},
		}},
	}}}
	var buf []byte

	b.ReportAllocs()
	for b.Loop() {
		var err error
		if buf, err = otlpjson.EncodeMetrics(buf[:0], rm); err != nil {
			b.Fatal(err)
		}
	}
}
