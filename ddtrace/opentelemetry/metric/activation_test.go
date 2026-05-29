// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRuntimeMetricsEnabled(t *testing.T) {
	t.Run("all flags set", func(t *testing.T) {
		t.Setenv("DD_RUNTIME_METRICS_ENABLED", "true")
		t.Setenv("DD_METRICS_OTEL_ENABLED", "true")
		t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
		assert.True(t, runtimeMetricsEnabled())
	})

	t.Run("missing DD_RUNTIME_METRICS_ENABLED", func(t *testing.T) {
		t.Setenv("DD_METRICS_OTEL_ENABLED", "true")
		t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
		assert.False(t, runtimeMetricsEnabled())
	})

	t.Run("missing DD_METRICS_OTEL_ENABLED", func(t *testing.T) {
		t.Setenv("DD_RUNTIME_METRICS_ENABLED", "true")
		t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
		assert.False(t, runtimeMetricsEnabled())
	})

	t.Run("OTEL_METRICS_EXPORTER not otlp", func(t *testing.T) {
		t.Setenv("DD_RUNTIME_METRICS_ENABLED", "true")
		t.Setenv("DD_METRICS_OTEL_ENABLED", "true")
		assert.False(t, runtimeMetricsEnabled())
	})

	t.Run("OTEL_METRICS_EXPORTER=none disables", func(t *testing.T) {
		t.Setenv("DD_RUNTIME_METRICS_ENABLED", "true")
		t.Setenv("DD_METRICS_OTEL_ENABLED", "true")
		t.Setenv("OTEL_METRICS_EXPORTER", "none")
		assert.False(t, runtimeMetricsEnabled())
	})
}
