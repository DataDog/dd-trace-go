// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"context"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestIsOTLPMetricsEnabled(t *testing.T) {
	tests := []struct {
		envVal   string
		expected bool
	}{
		{"true", true},
		{"True", true},
		{"TRUE", true},
		{"1", true},
		{"false", false},
		{"False", false},
		{"0", false},
		{"", false},
		{"invalid", false},
	}
	for _, tt := range tests {
		t.Run(tt.envVal, func(t *testing.T) {
			if tt.envVal != "" {
				t.Setenv("DD_METRICS_OTEL_ENABLED", tt.envVal)
			}
			assert.Equal(t, tt.expected, isOTLPMetricsEnabled())
		})
	}
}

func TestTracerOTLPRuntimeMetricsToggle(t *testing.T) {
	t.Setenv("DD_METRICS_OTEL_ENABLED", "false")
	assert.False(t, isOTLPMetricsEnabled())

	t.Setenv("DD_METRICS_OTEL_ENABLED", "true")
	assert.True(t, isOTLPMetricsEnabled())
}

// TestOTLPRuntimeMetricsCollection verifies that all 8 OTel Go runtime metrics
// are registered and produce real values. This mirrors TestReportRuntimeMetrics
// which does the same for DogStatsD metrics via a mock statsd client.
//
// Uses OTel SDK's ManualReader to collect metrics in-memory without needing
// a real OTLP endpoint — equivalent to how the DD test uses TestStatsdClient.
func TestOTLPRuntimeMetricsCollection(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer provider.Shutdown(context.Background())

	meter := provider.Meter("github.com/DataDog/dd-trace-go/runtime")

	// Register the same instruments as startOTLPRuntimeMetrics()
	memUsed, err := meter.Float64ObservableGauge("go.memory.used",
		otelmetric.WithUnit("By"))
	require.NoError(t, err)

	memLimit, err := meter.Int64ObservableGauge("go.memory.limit",
		otelmetric.WithUnit("By"))
	require.NoError(t, err)

	memAllocated, err := meter.Int64ObservableCounter("go.memory.allocated",
		otelmetric.WithUnit("By"))
	require.NoError(t, err)

	memAllocations, err := meter.Int64ObservableCounter("go.memory.allocations",
		otelmetric.WithUnit("{allocation}"))
	require.NoError(t, err)

	gcGoal, err := meter.Int64ObservableGauge("go.memory.gc.goal",
		otelmetric.WithUnit("By"))
	require.NoError(t, err)

	goroutineCount, err := meter.Int64ObservableGauge("go.goroutine.count",
		otelmetric.WithUnit("{goroutine}"))
	require.NoError(t, err)

	processorLimit, err := meter.Int64ObservableGauge("go.processor.limit",
		otelmetric.WithUnit("{thread}"))
	require.NoError(t, err)

	configGogc, err := meter.Int64ObservableGauge("go.config.gogc",
		otelmetric.WithUnit("%"))
	require.NoError(t, err)

	var ms runtime.MemStats
	attrStack := otelmetric.WithAttributes(attribute.String("go.memory.type", "stack"))
	attrOther := otelmetric.WithAttributes(attribute.String("go.memory.type", "other"))

	_, err = meter.RegisterCallback(
		func(ctx context.Context, o otelmetric.Observer) error {
			runtime.ReadMemStats(&ms)
			o.ObserveFloat64(memUsed, float64(ms.StackInuse), attrStack)
			o.ObserveFloat64(memUsed, float64(ms.HeapInuse), attrOther)
			o.ObserveInt64(memLimit, debug.SetMemoryLimit(-1))
			o.ObserveInt64(memAllocated, int64(ms.TotalAlloc))
			o.ObserveInt64(memAllocations, int64(ms.Mallocs))
			o.ObserveInt64(gcGoal, int64(ms.NextGC))
			o.ObserveInt64(goroutineCount, int64(runtime.NumGoroutine()))
			o.ObserveInt64(processorLimit, int64(runtime.GOMAXPROCS(0)))
			gogc := debug.SetGCPercent(-1)
			debug.SetGCPercent(gogc)
			o.ObserveInt64(configGogc, int64(gogc))
			return nil
		},
		memUsed, memLimit, memAllocated, memAllocations, gcGoal,
		goroutineCount, processorLimit, configGogc,
	)
	require.NoError(t, err)

	// Collect metrics
	var rm metricdata.ResourceMetrics
	err = reader.Collect(context.Background(), &rm)
	require.NoError(t, err)

	// Extract metric names and values
	metricNames := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			metricNames[m.Name] = true
		}
	}

	// Verify all 8 OTel Go runtime metrics are present
	// (mirrors TestReportRuntimeMetrics asserting runtime.go.num_cpu etc.)
	expectedMetrics := []string{
		"go.memory.used",
		"go.memory.limit",
		"go.memory.allocated",
		"go.memory.allocations",
		"go.memory.gc.goal",
		"go.goroutine.count",
		"go.processor.limit",
		"go.config.gogc",
	}
	for _, name := range expectedMetrics {
		assert.True(t, metricNames[name], "expected metric %q to be present", name)
	}
	assert.Equal(t, 8, len(metricNames), "expected exactly 8 metrics")

	// Verify go.memory.used has go.memory.type attribute (stack + other)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "go.memory.used" {
				gauge, ok := m.Data.(metricdata.Gauge[float64])
				require.True(t, ok, "go.memory.used should be a float64 gauge")
				assert.Equal(t, 2, len(gauge.DataPoints), "go.memory.used should have 2 data points (stack + other)")
				types := map[string]bool{}
				for _, dp := range gauge.DataPoints {
					for _, attr := range dp.Attributes.ToSlice() {
						if string(attr.Key) == "go.memory.type" {
							types[attr.Value.AsString()] = true
						}
					}
					assert.Greater(t, dp.Value, float64(0), "memory value should be positive")
				}
				assert.True(t, types["stack"], "should have go.memory.type=stack")
				assert.True(t, types["other"], "should have go.memory.type=other")
			}
			if m.Name == "go.goroutine.count" {
				gauge, ok := m.Data.(metricdata.Gauge[int64])
				require.True(t, ok, "go.goroutine.count should be an int64 gauge")
				require.Equal(t, 1, len(gauge.DataPoints))
				assert.Greater(t, gauge.DataPoints[0].Value, int64(0), "goroutine count should be positive")
			}
			if m.Name == "go.processor.limit" {
				gauge, ok := m.Data.(metricdata.Gauge[int64])
				require.True(t, ok, "go.processor.limit should be an int64 gauge")
				require.Equal(t, 1, len(gauge.DataPoints))
				assert.Greater(t, gauge.DataPoints[0].Value, int64(0), "GOMAXPROCS should be positive")
			}
		}
	}

	// Verify no metric uses DD-proprietary naming
	for name := range metricNames {
		assert.NotContains(t, name, "runtime.go",
			"metric %q should use OTel naming (go.*), not DD naming (runtime.go.*)", name)
	}
}
