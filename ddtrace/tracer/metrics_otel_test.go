// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric/noop"
)

// otelRuntimeMetricsActive reports whether tracer.Start() installed a real OTel
// SDK MeterProvider as the global. When the feature is disabled the global stays
// the no-op provider, so this is a reliable proxy for "the wiring fired".
func otelRuntimeMetricsActive() bool {
	_, isNoop := otel.GetMeterProvider().(noop.MeterProvider)
	return !isNoop
}

// startOTLPSink stands up a local HTTP endpoint that accepts OTLP metric exports
// and points the exporter at it via OTEL_EXPORTER_OTLP_METRICS_ENDPOINT. The
// server is closed during cleanup, which tears down the exporter's keep-alive
// connection so its persistConn goroutines unwind before the package-level
// goleak check in TestMain runs.
func startOTLPSink(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", srv.URL)
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
		// Belt-and-suspenders: drop any idle conns the exporter still holds.
		if tr, ok := http.DefaultTransport.(*http.Transport); ok {
			tr.CloseIdleConnections()
		}
	})
}

func shutdownAndResetProvider(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	type shutdowner interface{ Shutdown(context.Context) error }
	if mp, ok := otel.GetMeterProvider().(shutdowner); ok {
		_ = mp.Shutdown(ctx)
	}
	otel.SetMeterProvider(noop.NewMeterProvider())
}

func TestTracerStartOtelRuntimeMetricsRequiresAllFlags(t *testing.T) {
	startOTLPSink(t)
	t.Setenv("DD_RUNTIME_METRICS_ENABLED", "true")
	t.Setenv("DD_METRICS_OTEL_ENABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "86400000")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	internalconfig.SetUseFreshConfig(true)
	defer internalconfig.SetUseFreshConfig(false)
	defer shutdownAndResetProvider(t)

	require.NoError(t, Start(WithLogger(log.DiscardLogger{})))
	defer Stop()

	assert.True(t, otelRuntimeMetricsActive(), "tracer.Start should have installed the OTel runtime metrics provider")
}

func TestTracerStartSkipsOtelRuntimeMetricsWhenExporterNone(t *testing.T) {
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "86400000")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	internalconfig.SetUseFreshConfig(true)
	defer internalconfig.SetUseFreshConfig(false)
	defer shutdownAndResetProvider(t)

	require.NoError(t, Start(WithLogger(log.DiscardLogger{})))
	defer Stop()

	assert.False(t, otelRuntimeMetricsActive(), "tracer.Start should not install the OTel provider when OTEL_METRICS_EXPORTER=none")
}
