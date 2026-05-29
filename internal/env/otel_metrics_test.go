// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package env

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMetricsExportEnabled(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		assert.False(t, MetricsExportEnabled())
	})
	t.Run("enabled via DD_METRICS_OTEL_ENABLED", func(t *testing.T) {
		t.Setenv("DD_METRICS_OTEL_ENABLED", "true")
		assert.True(t, MetricsExportEnabled())
	})
	t.Run("enabled via OTEL_METRICS_EXPORTER=otlp", func(t *testing.T) {
		t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
		assert.True(t, MetricsExportEnabled())
	})
	t.Run("disabled when OTEL_METRICS_EXPORTER=none", func(t *testing.T) {
		t.Setenv("DD_METRICS_OTEL_ENABLED", "true")
		t.Setenv("OTEL_METRICS_EXPORTER", "none")
		assert.False(t, MetricsExportEnabled())
	})
}

func TestOtelRuntimeMetricsEnabled(t *testing.T) {
	t.Run("all flags", func(t *testing.T) {
		t.Setenv("DD_RUNTIME_METRICS_ENABLED", "true")
		t.Setenv("DD_METRICS_OTEL_ENABLED", "true")
		t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
		assert.True(t, OtelRuntimeMetricsEnabled())
	})
	t.Run("missing DD_RUNTIME_METRICS_ENABLED", func(t *testing.T) {
		t.Setenv("DD_METRICS_OTEL_ENABLED", "true")
		t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
		assert.False(t, OtelRuntimeMetricsEnabled())
	})
}
