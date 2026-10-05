// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func resetProductStartState() {
	startMu.Lock()
	defer startMu.Unlock()
	lastEnvHash, lastProduct = 0, ""
}

func TestRecordProductStart(t *testing.T) {
	t.Run("first call records a baseline", func(t *testing.T) {
		resetProductStartState()
		defer resetProductStartState()

		RecordProductStart(ProductTracer)

		assert.Equal(t, ProductTracer, lastProduct)
		assert.Equal(t, envSnapshotHash(), lastEnvHash)
	})

	t.Run("repeat call with unchanged environment updates lastProduct", func(t *testing.T) {
		resetProductStartState()
		defer resetProductStartState()

		RecordProductStart(ProductTracer)
		RecordProductStart(ProductProfiler)

		assert.Equal(t, ProductProfiler, lastProduct)
	})

	t.Run("repeat call after an env change updates the recorded baseline", func(t *testing.T) {
		resetProductStartState()
		defer resetProductStartState()

		RecordProductStart(ProductTracer)

		t.Setenv("DD_SERVICE", "changed-service")
		RecordProductStart(ProductProfiler)

		assert.Equal(t, ProductProfiler, lastProduct)
		assert.Equal(t, envSnapshotHash(), lastEnvHash)
	})

	t.Run("removing a previously set env var counts as a change", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "initial-service")
		resetProductStartState()
		defer resetProductStartState()

		RecordProductStart(ProductTracer)
		before := lastEnvHash

		t.Setenv("DD_SERVICE", "")
		RecordProductStart(ProductProfiler)

		assert.NotEqual(t, before, lastEnvHash)
	})
}

func TestEnvSnapshotHash(t *testing.T) {
	t.Run("deterministic for the same environment", func(t *testing.T) {
		assert.Equal(t, envSnapshotHash(), envSnapshotHash())
	})

	t.Run("changes when a supported, non-sensitive var changes", func(t *testing.T) {
		before := envSnapshotHash()
		t.Setenv("DD_SERVICE", "some-other-service")
		after := envSnapshotHash()

		assert.NotEqual(t, before, after)
	})

	t.Run("does not collide when a value contains the field-separator characters", func(t *testing.T) {
		// DD_SERVICE_MAPPING sorts immediately after DD_SERVICE, so a naive
		// "k=v;" join would make these two environments indistinguishable.
		t.Setenv("DD_SERVICE", "x")
		t.Setenv("DD_SERVICE_MAPPING", "y")
		merged := envSnapshotHash()

		t.Setenv("DD_SERVICE", "x;DD_SERVICE_MAPPING=y")
		// t.Setenv above still restores the pre-test value on cleanup even
		// though we unset it directly here.
		require.NoError(t, os.Unsetenv("DD_SERVICE_MAPPING"))
		split := envSnapshotHash()

		assert.NotEqual(t, merged, split)
	})

	t.Run("changes when a sensitive configuration value changes", func(t *testing.T) {
		var sensitiveKey string
		for k := range env.SensitiveConfigurations {
			if _, ok := env.SupportedConfigurations[k]; ok {
				sensitiveKey = k
				break
			}
		}
		if sensitiveKey == "" {
			t.Skip("no sensitive key overlaps with SupportedConfigurations")
		}

		before := envSnapshotHash()
		t.Setenv(sensitiveKey, "super-secret-value")
		after := envSnapshotHash()

		assert.NotEqual(t, before, after)
	})
}

func TestRecordProductStart_ReportsMetric(t *testing.T) {
	resetProductStartState()
	defer resetProductStartState()

	rec := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(rec)()

	RecordProductStart(ProductTracer)

	t.Setenv("DD_SERVICE", "changed-service")
	RecordProductStart(ProductProfiler)

	tags := []string{"trigger_product:profiler", "previous_product:tracer"}
	sort.Strings(tags)
	key := telemetrytest.MetricKey{
		Namespace: telemetry.NamespaceGeneral,
		Name:      "config.repeat_start_env_diff",
		Tags:      strings.Join(tags, ","),
		Kind:      "count",
	}
	handle, ok := rec.Metrics[key]
	require.True(t, ok, "expected config.repeat_start_env_diff to be recorded")
	assert.Equal(t, float64(1), handle.Get())
}
