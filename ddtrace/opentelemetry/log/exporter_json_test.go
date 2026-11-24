// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package log

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/config"
	rootlog "github.com/DataDog/dd-trace-go/v2/internal/log"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
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

func oneLogRecord(t *testing.T) []sdklog.Record {
	t.Helper()
	exp := newTestExporter()
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exp)))
	var r otellog.Record
	r.SetSeverity(otellog.SeverityInfo)
	r.SetBody(attribute.StringValue("hello"))
	provider.Logger("test").Emit(t.Context(), r)
	require.NoError(t, provider.Shutdown(t.Context()))
	return exp.GetRecords()
}

func TestJSONExporter(t *testing.T) {
	t.Cleanup(func() { config.CreateNew() })

	t.Run("sends application/json to the logs endpoint with the configured headers", func(t *testing.T) {
		url, requests := newCollector(t, nil)
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/json")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", url)
		t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "api-key=secret%20value")
		config.CreateNew()

		exporter, err := newOTLPExporter(t.Context(), nil, nil)
		require.NoError(t, err)
		defer exporter.Shutdown(t.Context())
		require.NoError(t, exporter.Export(t.Context(), oneLogRecord(t)))

		r := <-requests
		assert.Equal(t, "/v1/logs", r.path)
		assert.Equal(t, "application/json", r.header.Get("Content-Type"))
		assert.Equal(t, "secret value", r.header.Get("api-key"))
		assert.Empty(t, r.header.Get("Content-Encoding"))
		req := plogotlp.NewExportRequest()
		require.NoError(t, req.UnmarshalJSON(r.body))
		assert.Equal(t, "hello", req.Logs().ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Body().Str())
	})

	t.Run("sends plain http when the shared endpoint uses another scheme", func(t *testing.T) {
		url, requests := newCollector(t, nil)
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/json")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", strings.Replace(url, "http://", "grpc://", 1)+"/v1/logs")
		config.CreateNew()

		exporter, err := newOTLPExporter(t.Context(), nil, nil)
		require.NoError(t, err)
		defer exporter.Shutdown(t.Context())
		require.NoError(t, exporter.Export(t.Context(), oneLogRecord(t)))

		assert.Equal(t, "/v1/logs", (<-requests).path)
	})

	t.Run("compresses the body when OTEL_EXPORTER_OTLP_COMPRESSION is gzip", func(t *testing.T) {
		url, requests := newCollector(t, nil)
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/json")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", url)
		t.Setenv("OTEL_EXPORTER_OTLP_COMPRESSION", "gzip")
		config.CreateNew()

		exporter, err := newOTLPExporter(t.Context(), nil, nil)
		require.NoError(t, err)
		defer exporter.Shutdown(t.Context())
		require.NoError(t, exporter.Export(t.Context(), oneLogRecord(t)))

		r := <-requests
		assert.Equal(t, "gzip", r.header.Get("Content-Encoding"))
		zr, err := gzip.NewReader(bytes.NewReader(r.body))
		require.NoError(t, err)
		decompressed, err := io.ReadAll(zr)
		require.NoError(t, err)
		require.NoError(t, plogotlp.NewExportRequest().UnmarshalJSON(decompressed))
	})

	t.Run("retries a 503", func(t *testing.T) {
		url, requests := newCollector(t, func(call int) int {
			if call == 1 {
				return http.StatusServiceUnavailable
			}
			return http.StatusOK
		})
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/json")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", url)
		config.CreateNew()

		exporter, err := newOTLPExporter(t.Context(), nil, nil)
		require.NoError(t, err)
		defer exporter.Shutdown(t.Context())
		require.NoError(t, exporter.Export(t.Context(), oneLogRecord(t)))

		assert.Len(t, requests, 2)
	})

	t.Run("does not retry a 400", func(t *testing.T) {
		url, requests := newCollector(t, func(int) int { return http.StatusBadRequest })
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/json")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", url)
		config.CreateNew()

		exporter, err := newOTLPExporter(t.Context(), nil, nil)
		require.NoError(t, err)
		defer exporter.Shutdown(t.Context())

		assert.Error(t, exporter.Export(t.Context(), oneLogRecord(t)))
		assert.Len(t, requests, 1)
	})

	t.Run("fails after being stopped", func(t *testing.T) {
		url, requests := newCollector(t, nil)
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/json")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", url)
		config.CreateNew()

		exporter, err := newOTLPExporter(t.Context(), nil, nil)
		require.NoError(t, err)
		require.NoError(t, exporter.Shutdown(t.Context()))

		assert.ErrorIs(t, exporter.Export(t.Context(), oneLogRecord(t)), errShutdown)
		assert.Empty(t, requests)
	})

	t.Run("warns when the endpoint already ends in /v1/logs", func(t *testing.T) {
		logger := new(rootlog.RecordLogger)
		defer rootlog.UseLogger(logger)()
		url, _ := newCollector(t, nil)
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/json")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", url+"/v1/logs")
		config.CreateNew()

		exporter, err := newOTLPExporter(t.Context(), nil, nil)
		require.NoError(t, err)
		defer exporter.Shutdown(t.Context())

		assert.Contains(t, strings.Join(logger.Logs(), "\n"), "already ends in /v1/logs")
	})
}

func BenchmarkJSONExport(b *testing.B) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer srv.Close()
	b.Cleanup(func() { config.CreateNew() })
	b.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/json")
	b.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", srv.URL)
	config.CreateNew()
	exporter, err := newOTLPExporter(b.Context(), nil, nil)
	require.NoError(b, err)
	defer exporter.Shutdown(b.Context())
	records := make([]sdklog.Record, 512)
	for i := range records {
		records[i].SetBody(attribute.StringValue("benchmark log line"))
		records[i].SetSeverity(otellog.SeverityInfo)
		records[i].AddAttributes(attribute.String("k", "v"), attribute.Int("n", i))
	}

	b.ReportAllocs()
	for b.Loop() {
		if err := exporter.Export(b.Context(), records); err != nil {
			b.Fatal(err)
		}
	}
}

// TestProtobufExporterIsUnchanged guards the upstream path: http/protobuf must keep
// sending protobuf even though http/json now has its own exporter.
func TestProtobufExporterIsUnchanged(t *testing.T) {
	t.Cleanup(func() { config.CreateNew() })
	url, requests := newCollector(t, nil)
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", url)
	config.CreateNew()

	exporter, err := newOTLPExporter(t.Context(), nil, nil)
	require.NoError(t, err)
	defer exporter.Shutdown(t.Context())
	require.NoError(t, exporter.Export(t.Context(), oneLogRecord(t)))

	assert.Equal(t, "application/x-protobuf", (<-requests).header.Get("Content-Type"))
}
