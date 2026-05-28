// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func newTestMeterProvider() (*sdkmetric.MeterProvider, *sdkmetric.ManualReader) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return mp, reader
}

func collectResourceMetrics(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	return rm
}

func metricsByName(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	rm := collectResourceMetrics(t, reader)
	out := make(map[string]metricdata.Metrics)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out[m.Name] = m
		}
	}
	return out
}

func TestOtelRuntimeMetricsStartNoopProvider(t *testing.T) {
	// With no global provider set and DD_METRICS_OTEL_ENABLED unset,
	// InstallGlobal is a no-op so the global stays noop.
	// startOtelRuntimeMetrics must still succeed (metrics are silently discarded).
	o, err := startOtelRuntimeMetrics(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, o)
}

func TestOtelRuntimeMetricsStartWithSDKProvider(t *testing.T) {
	mp, _ := newTestMeterProvider()
	otel.SetMeterProvider(mp)
	defer otel.SetMeterProvider(noop.NewMeterProvider())

	o, err := startOtelRuntimeMetrics(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, o)
}

func TestOtelRuntimeMetricsStop(t *testing.T) {
	o := &otelRuntimeMetrics{}
	assert.NotPanics(t, func() { o.stop() })
}

func TestOtelRuntimeMetricsRecommendedNames(t *testing.T) {
	mp, reader := newTestMeterProvider()
	meter := mp.Meter(otelRuntimeMetricsInstrumentationScope)

	require.NoError(t, registerRecommendedMetrics(context.Background(), meter))

	byName := metricsByName(t, reader)

	expected := []string{
		"go.memory.used",
		"go.memory.limit",
		"go.memory.allocated",
		"go.memory.allocations",
		"go.memory.gc.goal",
		"go.goroutine.count",
		"go.processor.limit",
		"go.config.gogc",
	}
	for _, want := range expected {
		_, ok := byName[want]
		assert.True(t, ok, "expected metric %q to be present", want)
	}
}

func TestOtelRuntimeMetricsRecommendedValues(t *testing.T) {
	mp, reader := newTestMeterProvider()
	meter := mp.Meter(otelRuntimeMetricsInstrumentationScope)

	require.NoError(t, registerRecommendedMetrics(context.Background(), meter))

	byName := metricsByName(t, reader)

	gm, ok := byName["go.goroutine.count"]
	require.True(t, ok)
	gData, ok := gm.Data.(metricdata.Sum[int64])
	require.True(t, ok, "go.goroutine.count should be Sum[int64]")
	require.NotEmpty(t, gData.DataPoints)
	assert.Greater(t, gData.DataPoints[0].Value, int64(0))

	pm, ok := byName["go.processor.limit"]
	require.True(t, ok)
	pData, ok := pm.Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.NotEmpty(t, pData.DataPoints)
	assert.GreaterOrEqual(t, pData.DataPoints[0].Value, int64(1))

	gm2, ok := byName["go.config.gogc"]
	require.True(t, ok)
	gData2, ok := gm2.Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.NotEmpty(t, gData2.DataPoints)
	assert.Greater(t, gData2.DataPoints[0].Value, int64(0))

	// go.memory.used emits two data points carrying go.memory.type ∈ {other, stack}.
	mm, ok := byName["go.memory.used"]
	require.True(t, ok)
	mData, ok := mm.Data.(metricdata.Sum[int64])
	require.True(t, ok, "go.memory.used should be Sum[int64]")
	require.Equal(t, 2, len(mData.DataPoints), "go.memory.used should have 2 data points (other, stack)")
	memTypes := make(map[string]bool)
	for _, dp := range mData.DataPoints {
		if v, exists := dp.Attributes.Value(attribute.Key("go.memory.type")); exists {
			memTypes[v.AsString()] = true
		}
	}
	assert.True(t, memTypes["other"], "go.memory.used should have type=other")
	assert.True(t, memTypes["stack"], "go.memory.used should have type=stack")
}

func TestOtelRuntimeMetricsInstrumentationScope(t *testing.T) {
	mp, reader := newTestMeterProvider()
	meter := mp.Meter(otelRuntimeMetricsInstrumentationScope,
		otelmetric.WithInstrumentationVersion("test"))

	require.NoError(t, registerRecommendedMetrics(context.Background(), meter))

	rm := collectResourceMetrics(t, reader)
	require.NotEmpty(t, rm.ScopeMetrics)

	for _, sm := range rm.ScopeMetrics {
		assert.Equal(t, otelRuntimeMetricsInstrumentationScope, sm.Scope.Name)
	}
}
