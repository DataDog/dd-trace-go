// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"context"
	"testing"
	"time"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/otelmetricsinstall"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric/noop"
)

// withTestHooks installs stub otelmetricsinstall hooks for the duration of a test
// so the tracer wiring can be verified without importing the OTel SDK.
func withTestHooks(t *testing.T) *bool {
	t.Helper()
	started := false
	otelmetricsinstall.StartHook = func(_ context.Context) error {
		started = true
		return nil
	}
	t.Cleanup(func() {
		otelmetricsinstall.StartHook = nil
		otelmetricsinstall.ShutdownHook = nil
	})
	return &started
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
	t.Setenv("DD_RUNTIME_METRICS_ENABLED", "true")
	t.Setenv("DD_METRICS_OTEL_ENABLED", "true")
	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "86400000")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	internalconfig.SetUseFreshConfig(true)
	defer internalconfig.SetUseFreshConfig(false)
	defer shutdownAndResetProvider(t)

	started := withTestHooks(t)

	require.NoError(t, Start(WithLogger(log.DiscardLogger{})))
	defer Stop()

	assert.True(t, *started, "StartRuntimeMetrics hook should have been called")
}

func TestTracerStartSkipsOtelRuntimeMetricsWithoutAllFlags(t *testing.T) {
	t.Setenv("DD_METRICS_OTEL_ENABLED", "true")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "86400000")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	internalconfig.SetUseFreshConfig(true)
	defer internalconfig.SetUseFreshConfig(false)
	defer shutdownAndResetProvider(t)

	started := withTestHooks(t)

	require.NoError(t, Start(WithLogger(log.DiscardLogger{})))
	defer Stop()

	assert.False(t, *started, "StartRuntimeMetrics hook should not have been called")
}
