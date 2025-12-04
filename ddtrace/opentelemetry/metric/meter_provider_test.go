// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
)

// TestMeterProviderInstruments verifies that various OTel instrument types
// can be created and used without errors.
func TestMeterProviderInstruments(t *testing.T) {
	t.Setenv(envDDMetricsOtelEnabled, "true")
	mp, err := NewMeterProvider(WithExportInterval(24 * time.Hour))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	defer Shutdown(ctx, mp)

	meter := mp.Meter("test-meter")

	t.Run("Histogram", func(t *testing.T) {
		histogram, err := meter.Float64Histogram("test.histogram")
		require.NoError(t, err)
		histogram.Record(ctx, 1.5)
	})

	t.Run("UpDownCounter", func(t *testing.T) {
		counter, err := meter.Int64UpDownCounter("test.updowncounter")
		require.NoError(t, err)
		counter.Add(ctx, 5)
		counter.Add(ctx, -2)
	})

	t.Run("ObservableGauge", func(t *testing.T) {
		_, err := meter.Int64ObservableGauge("test.gauge",
			metric.WithInt64Callback(func(_ context.Context, observer metric.Int64Observer) error {
				observer.Observe(100)
				return nil
			}),
		)
		require.NoError(t, err)
	})

	t.Run("Counter", func(t *testing.T) {
		counter, err := meter.Int64Counter("test.counter")
		require.NoError(t, err)
		counter.Add(ctx, 42)
	})
}

// TestForceFlush verifies that ForceFlush can be called without panicking.
func TestForceFlush(t *testing.T) {
	t.Setenv(envDDMetricsOtelEnabled, "true")
	mp, err := NewMeterProvider(WithExportInterval(24 * time.Hour))
	require.NoError(t, err)
	defer Shutdown(context.Background(), mp)

	// ForceFlush may fail due to connection issues, but shouldn't panic
	_ = ForceFlush(context.Background(), mp)
}

// TestMetricsDisabledByDefault verifies that when DD_METRICS_OTEL_ENABLED is not set,
// the MeterProvider returns a no-op provider that doesn't export metrics.
func TestMetricsDisabledByDefault(t *testing.T) {
	mp, err := NewMeterProvider()
	require.NoError(t, err)
	require.NotNil(t, mp)
	assert.True(t, IsNoop(mp), "MeterProvider should be no-op when DD_METRICS_OTEL_ENABLED is not set")

	// Shutdown should not fail for no-op provider
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = Shutdown(ctx, mp)
	assert.NoError(t, err)
}

// TestMetricsDisabledExplicitly verifies that metrics are disabled when:
// - DD_METRICS_OTEL_ENABLED=false
// - DD_METRICS_OTEL_ENABLED=0
// - OTEL_METRICS_EXPORTER=none
func TestMetricsDisabledExplicitly(t *testing.T) {
	t.Run("DD_METRICS_OTEL_ENABLED=false", func(t *testing.T) {
		t.Setenv(envDDMetricsOtelEnabled, "false")
		mp, err := NewMeterProvider()
		require.NoError(t, err)
		assert.True(t, IsNoop(mp), "MeterProvider should be no-op when DD_METRICS_OTEL_ENABLED=false")
	})

	t.Run("DD_METRICS_OTEL_ENABLED=0", func(t *testing.T) {
		t.Setenv(envDDMetricsOtelEnabled, "0")
		mp, err := NewMeterProvider()
		require.NoError(t, err)
		assert.True(t, IsNoop(mp), "MeterProvider should be no-op when DD_METRICS_OTEL_ENABLED=0")
	})

	t.Run("OTEL_METRICS_EXPORTER=none", func(t *testing.T) {
		t.Setenv(envDDMetricsOtelEnabled, "true")
		t.Setenv(envOtelMetricsExporter, "none")
		mp, err := NewMeterProvider()
		require.NoError(t, err)
		assert.True(t, IsNoop(mp), "MeterProvider should be no-op when OTEL_METRICS_EXPORTER=none")
	})
}

// TestMetricsEnabled verifies that metrics are enabled when DD_METRICS_OTEL_ENABLED
// is set to "true" or "1", returning a functional (non-noop) MeterProvider.
func TestMetricsEnabled(t *testing.T) {
	t.Run("DD_METRICS_OTEL_ENABLED=true", func(t *testing.T) {
		t.Setenv(envDDMetricsOtelEnabled, "true")
		mp, err := NewMeterProvider(WithExportInterval(24 * time.Hour))
		require.NoError(t, err)
		assert.False(t, IsNoop(mp), "MeterProvider should not be no-op when DD_METRICS_OTEL_ENABLED=true")

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		defer Shutdown(ctx, mp)
	})

	t.Run("DD_METRICS_OTEL_ENABLED=1", func(t *testing.T) {
		t.Setenv(envDDMetricsOtelEnabled, "1")
		mp, err := NewMeterProvider(WithExportInterval(24 * time.Hour))
		require.NoError(t, err)
		assert.False(t, IsNoop(mp), "MeterProvider should not be no-op when DD_METRICS_OTEL_ENABLED=1")

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		defer Shutdown(ctx, mp)
	})
}

// TestMeterProviderExporterProtocols verifies that the MeterProvider can be created
// with both gRPC and HTTP exporters and instruments work correctly.
func TestMeterProviderExporterProtocols(t *testing.T) {
	protocols := []string{"grpc", "http/protobuf"}

	for _, protocol := range protocols {
		t.Run(protocol, func(t *testing.T) {
			t.Setenv(envDDMetricsOtelEnabled, "true")
			t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", protocol)

			mp, err := NewMeterProvider(WithExportInterval(24 * time.Hour))
			require.NoError(t, err)
			require.NotNil(t, mp)
			assert.False(t, IsNoop(mp))

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			defer Shutdown(ctx, mp)

			meter := mp.Meter("test-meter")
			counter, err := meter.Int64Counter("test.counter")
			require.NoError(t, err)
			counter.Add(ctx, 1)
		})
	}
}

// TestNoopMeterProviderCanRecordMetrics verifies that recording metrics
// on a no-op provider doesn't crash.
func TestNoopMeterProviderCanRecordMetrics(t *testing.T) {
	mp, err := NewMeterProvider() // Default: disabled
	require.NoError(t, err)
	assert.True(t, IsNoop(mp))

	meter := mp.Meter("test-meter")
	counter, err := meter.Int64Counter("test.counter")
	require.NoError(t, err)
	counter.Add(context.Background(), 1) // Should not crash

	assert.NoError(t, Shutdown(context.Background(), mp))
}
