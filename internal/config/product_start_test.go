// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
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

	t.Run("ignores sensitive configuration values", func(t *testing.T) {
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

		assert.Equal(t, before, after)
	})
}

// TODO: config.repeat_start_env_diff isn't registered in dd-go's golang_metrics.json
// yet, so telemetrytest.MockClient/RecordClient panic on it (see
// internal/telemetry/internal/knownmetrics). Once it's registered there and the
// generator has been re-run to pick it up, replace this with a real test asserting
// RecordProductStart's telemetry.Count("config.repeat_start_env_diff", ...).Submit(1)
// call via telemetry.MockClient, following the pattern in TestSetFeatureFlagsReportsFullList.
func TestRecordProductStart_ReportsMetric(t *testing.T) {
	t.Skip("pending config.repeat_start_env_diff registration in dd-go's golang_metrics.json")
}
