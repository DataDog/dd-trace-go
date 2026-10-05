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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// TestMeterProviderInstruments verifies that various OTel instrument types
// can be created and used without errors.
func TestMeterProviderInstruments(t *testing.T) {
	setMetricsExportEnv(t)
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
	setMetricsExportEnv(t)
	mp, err := NewMeterProvider(WithExportInterval(24 * time.Hour))
	require.NoError(t, err)
	defer Shutdown(context.Background(), mp)

	// ForceFlush may fail due to connection issues, but shouldn't panic
	_ = ForceFlush(context.Background(), mp)
}

func setMetricsExportEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DD_METRICS_OTEL_ENABLED", "true")

}

func TestInstallGlobalWithMetricsExport(t *testing.T) {
	setMetricsExportEnv(t)
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "86400000")
	defer otel.SetMeterProvider(noop.NewMeterProvider())

	require.NoError(t, installGlobal())
	_, ok := otel.GetMeterProvider().(*sdkmetric.MeterProvider)
	assert.True(t, ok)
}

// TestNewMeterProviderAlwaysReturnsSDKProvider verifies that NewMeterProvider always
// returns a real SDK provider regardless of env vars — enablement checks are the
// caller's responsibility (e.g. tracer.Start).
func TestNewMeterProviderAlwaysReturnsSDKProvider(t *testing.T) {
	setMetricsExportEnv(t)
	mp, err := NewMeterProvider(WithExportInterval(24 * time.Hour))
	require.NoError(t, err)
	require.NotNil(t, mp)
	assert.False(t, isNoop(mp), "NewMeterProvider should always return a real SDK provider")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = Shutdown(ctx, mp) // best-effort; OTLP endpoint may not be available
}

// TestMetricsEnabled verifies that metrics are enabled when all required env vars are set.
func TestMetricsEnabled(t *testing.T) {
	setMetricsExportEnv(t)
	mp, err := NewMeterProvider(WithExportInterval(24 * time.Hour))
	require.NoError(t, err)
	assert.False(t, isNoop(mp), "MeterProvider should not be no-op when metrics export is enabled")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	defer Shutdown(ctx, mp)
}

// TestMeterProviderExporterProtocols verifies that the MeterProvider can be created
// with both gRPC and HTTP exporters and instruments work correctly.
func TestMeterProviderExporterProtocols(t *testing.T) {
	protocols := []string{"grpc", "http/protobuf"}

	for _, protocol := range protocols {
		t.Run(protocol, func(t *testing.T) {
			setMetricsExportEnv(t)
			t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", protocol)

			mp, err := NewMeterProvider(WithExportInterval(24 * time.Hour))
			require.NoError(t, err)
			require.NotNil(t, mp)
			assert.False(t, isNoop(mp))

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
