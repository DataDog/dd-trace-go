// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestEqualMap(t *testing.T) {
	t.Run("equal regardless of order", func(t *testing.T) {
		assert.True(t, equalMap(map[string]any{"a": "1", "b": "2"}, map[string]any{"b": "2", "a": "1"}))
	})
	t.Run("different length", func(t *testing.T) {
		assert.False(t, equalMap(map[string]any{"a": "1"}, map[string]any{"a": "1", "b": "2"}))
	})
	t.Run("different value", func(t *testing.T) {
		assert.False(t, equalMap(map[string]any{"a": "1"}, map[string]any{"a": "2"}))
	})
	t.Run("missing key", func(t *testing.T) {
		assert.False(t, equalMap(map[string]any{"a": "1"}, map[string]any{"b": "1"}))
	})
	t.Run("empty and nil are equal", func(t *testing.T) {
		assert.True(t, equalMap(map[string]any{}, map[string]any{}))
		assert.True(t, equalMap[string](nil, nil))
		assert.True(t, equalMap(nil, map[string]any{}))
	})
}

func TestGlobalTags(t *testing.T) {
	t.Run("nil by default", func(t *testing.T) {
		cfg := loadConfig()
		assert.Nil(t, cfg.GlobalTags())
	})

	t.Run("SetGlobalTag accumulates and overwrites by key", func(t *testing.T) {
		cfg := loadConfig()
		cfg.SetGlobalTag("a", "1", telemetry.OriginCode)
		cfg.SetGlobalTag("b", "2", telemetry.OriginCode)
		cfg.SetGlobalTag("a", "3", telemetry.OriginCode) // overwrite
		tags := cfg.GlobalTags()
		assert.Equal(t, "3", tags["a"])
		assert.Equal(t, "2", tags["b"])
		assert.Len(t, tags, 2)
	})

	t.Run("env-sourced origin is sticky across programmatic adds", func(t *testing.T) {
		cfg := loadConfig()
		cfg.SetGlobalTag("env-tag", "v", telemetry.OriginEnvVar)
		cfg.SetGlobalTag("code-tag", "v", telemetry.OriginCode)
		_, origin := cfg.GlobalTagsConfig().Baseline()
		assert.Equal(t, telemetry.OriginEnvVar, origin)
	})

	t.Run("programmatic-only origin is OriginCode", func(t *testing.T) {
		cfg := loadConfig()
		cfg.SetGlobalTag("code-tag", "v", telemetry.OriginCode)
		_, origin := cfg.GlobalTagsConfig().Baseline()
		assert.Equal(t, telemetry.OriginCode, origin)
	})

	t.Run("SetGlobalTag clones, leaving prior snapshots intact", func(t *testing.T) {
		cfg := loadConfig()
		cfg.SetGlobalTag("a", "1", telemetry.OriginCode)
		first := cfg.GlobalTags()
		cfg.SetGlobalTag("b", "2", telemetry.OriginCode)
		_, ok := first["b"]
		assert.False(t, ok, "earlier snapshot must not be mutated by a later SetGlobalTag")
	})

	t.Run("SetGlobalTag reports trace_tags telemetry", func(t *testing.T) {
		rec := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(rec)()

		cfg := loadConfig()
		cfg.SetGlobalTag("a", "1", telemetry.OriginCode)
		assertTelemetryReport(t, rec.Configuration, "trace_tags", "a:1", telemetry.OriginCode)
	})

	t.Run("HandleRC update then reset to startup baseline", func(t *testing.T) {
		cfg := loadConfig()
		cfg.SetGlobalTag("startup", "yes", telemetry.OriginEnvVar)
		dc := cfg.GlobalTagsConfig()

		rc := map[string]any{"rc": "tag"}
		require.True(t, dc.HandleRC(&rc))
		assert.Equal(t, map[string]any{"rc": "tag"}, cfg.GlobalTags())

		require.True(t, dc.HandleRC(nil))
		assert.Equal(t, map[string]any{"startup": "yes"}, cfg.GlobalTags())
	})

	t.Run("HandleRC telemetry: update reports RemoteConfig, reset reports startup origin", func(t *testing.T) {
		rec := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(rec)()

		cfg := loadConfig()
		cfg.SetGlobalTag("startup", "yes", telemetry.OriginEnvVar)
		dc := cfg.GlobalTagsConfig()

		rc := map[string]any{"rc": "tag"}
		dc.HandleRC(&rc)
		assertTelemetryReport(t, rec.Configuration, "trace_tags", "rc:tag", telemetry.OriginRemoteConfig)

		rec.Configuration = nil // observe only the reset report
		dc.HandleRC(nil)
		assertTelemetryReport(t, rec.Configuration, "trace_tags", "startup:yes", telemetry.OriginEnvVar)
	})

	t.Run("identical RC update is deduped", func(t *testing.T) {
		cfg := loadConfig()
		dc := cfg.GlobalTagsConfig()
		rc := map[string]any{"a": "1"}
		require.True(t, dc.HandleRC(&rc))
		same := map[string]any{"a": "1"}
		assert.False(t, dc.HandleRC(&same), "identical update should be a no-op")
	})

	t.Run("concurrent SetGlobalTag is race-safe", func(t *testing.T) {
		cfg := loadConfig()
		const n = 50
		var wg sync.WaitGroup
		wg.Add(n)
		for i := range n {
			go func(i int) {
				defer wg.Done()
				cfg.SetGlobalTag(fmt.Sprintf("k%d", i), i, telemetry.OriginCode)
			}(i)
		}
		wg.Wait()
		assert.Len(t, cfg.GlobalTags(), n)
	})
}
