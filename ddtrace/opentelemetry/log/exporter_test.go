// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/DataDog/dd-trace-go/v2/internal/config"
	internallog "github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestOTLPJSONFallsBackToProtobuf(t *testing.T) {
	t.Cleanup(func() { config.CreateNew() })
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/json")
	config.CreateNew()
	logger := new(internallog.RecordLogger)
	defer internallog.UseLogger(logger)()
	previousLevel := internallog.GetLevel()
	internallog.SetLevel(internallog.LevelWarn)
	defer internallog.SetLevel(previousLevel)

	recorder := &telemetrytest.RecordClient{}
	defer telemetry.MockClient(recorder)()

	ctx := context.Background()
	exporter, err := newOTLPExporter(ctx, nil, nil)
	require.NoError(t, err)
	defer exporter.Shutdown(ctx)

	wrapped := exporter.(*telemetryExporter)
	assert.IsType(t, new(otlploghttp.Exporter), wrapped.Exporter)
	assert.Contains(t, strings.Join(logger.Logs(), "\n"), `Unknown OTLP logs protocol "http/json", defaulting to http/protobuf`)

	wrapped.telemetry.RecordLogRecords(1)
	key := telemetrytest.MetricKey{
		Namespace: telemetry.NamespaceTracers,
		Name:      "otel.log_records",
		Tags:      "encoding:protobuf,protocol:http",
		Kind:      "count",
	}
	require.Contains(t, recorder.Metrics, key)
	assert.Equal(t, float64(1), recorder.Metrics[key].Get())
}

func TestNewOTLPExporterProtocol(t *testing.T) {
	t.Cleanup(func() { config.CreateNew() })
	for protocol, expected := range map[string]struct {
		exporter sdklog.Exporter
		tags     string
	}{
		"":              {&otlploghttp.Exporter{}, "encoding:protobuf,protocol:http"},
		"http/protobuf": {&otlploghttp.Exporter{}, "encoding:protobuf,protocol:http"},
		"http":          {&otlploghttp.Exporter{}, "encoding:protobuf,protocol:http"},
		" GRPC ":        {&otlploggrpc.Exporter{}, "encoding:protobuf,protocol:grpc"},
	} {
		t.Run(protocol, func(t *testing.T) {
			recorder := &telemetrytest.RecordClient{}
			defer telemetry.MockClient(recorder)()
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", protocol)
			config.CreateNew()

			exporter, err := newOTLPExporter(t.Context(), nil, nil)
			require.NoError(t, err)
			defer exporter.Shutdown(t.Context())
			te := exporter.(*telemetryExporter)
			assert.IsType(t, expected.exporter, te.Exporter)

			te.telemetry.RecordLogRecords(1)
			assert.Contains(t, recorder.Metrics, telemetrytest.MetricKey{
				Namespace: telemetry.NamespaceTracers,
				Name:      "otel.log_records",
				Tags:      expected.tags,
				Kind:      "count",
			})
		})
	}
}

func TestNewOTLPExporterHTTPConfig(t *testing.T) {
	t.Cleanup(func() { config.CreateNew() })
	t.Run("endpoint and headers", func(t *testing.T) {
		requests := make(chan *http.Request, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			requests <- r
		}))
		defer srv.Close()
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", srv.URL+"/custom")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "api-key=secret%20value")
		config.CreateNew()

		exporter, err := newOTLPExporter(t.Context(), nil, nil)
		require.NoError(t, err)
		defer exporter.Shutdown(t.Context())
		require.NoError(t, exporter.Export(t.Context(), []sdklog.Record{{}}))

		r := <-requests
		assert.Equal(t, "/custom/v1/logs", r.URL.Path)
		assert.Equal(t, "secret value", r.Header.Get("api-key"))
	})

	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			<-release
		}))
		defer srv.Close()
		defer close(release)
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", srv.URL)
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", "50")
		config.CreateNew()

		noRetry := []otlploghttp.Option{otlploghttp.WithRetry(otlploghttp.RetryConfig{Enabled: false})}
		exporter, err := newOTLPExporter(t.Context(), noRetry, nil)
		require.NoError(t, err)
		defer exporter.Shutdown(t.Context())
		start := time.Now()
		assert.Error(t, exporter.Export(t.Context(), []sdklog.Record{{}}))
		assert.Less(t, time.Since(start), 5*time.Second)
	})
}

func TestNewOTLPExporterGRPCTransportSecurity(t *testing.T) {
	t.Cleanup(func() { config.CreateNew() })
	const tlsHandshakeRecord = 0x16
	for scheme, wantTLS := range map[string]bool{"https": true, "http": false, "grpc": false} {
		t.Run(scheme, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer ln.Close()
			firstByte := make(chan byte, 1)
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				b := make([]byte, 1)
				if _, err := conn.Read(b); err == nil {
					firstByte <- b[0]
				}
			}()
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "grpc")
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", scheme+"://"+ln.Addr().String())
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", "500")
			config.CreateNew()

			noRetry := []otlploggrpc.Option{otlploggrpc.WithRetry(otlploggrpc.RetryConfig{Enabled: false})}
			exporter, err := newOTLPExporter(t.Context(), nil, noRetry)
			require.NoError(t, err)
			defer exporter.Shutdown(t.Context())
			_ = exporter.Export(t.Context(), []sdklog.Record{{}})

			select {
			case b := <-firstByte:
				assert.Equal(t, wantTLS, b == tlsHandshakeRecord)
			case <-time.After(5 * time.Second):
				t.Fatal("exporter never connected")
			}
		})
	}
}
