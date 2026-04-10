// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/stretchr/testify/assert"
)

func TestDynamicConfig(t *testing.T) {
	t.Run("get returns initial value", func(t *testing.T) {
		dc := newDynamicConfig("test", 42)
		assert.Equal(t, 42, dc.Get())
		assert.Equal(t, telemetry.OriginDefault, dc.Origin())
	})

	t.Run("update changes value and origin", func(t *testing.T) {
		dc := newDynamicConfig("test", "initial")
		changed := dc.update("updated", telemetry.OriginEnvVar)
		assert.True(t, changed)
		assert.Equal(t, "updated", dc.Get())
		assert.Equal(t, telemetry.OriginEnvVar, dc.Origin())
	})

	t.Run("update with same value is a no-op", func(t *testing.T) {
		dc := newDynamicConfig("test", 3.14)
		changed := dc.update(3.14, telemetry.OriginEnvVar)
		assert.False(t, changed)
		assert.Equal(t, telemetry.OriginDefault, dc.Origin(), "origin should not change on no-op update")
	})

	t.Run("reset restores startup value", func(t *testing.T) {
		dc := newDynamicConfig("test", "startup")
		dc.update("modified", telemetry.OriginRemoteConfig)
		assert.Equal(t, "modified", dc.Get())

		changed := dc.reset()
		assert.True(t, changed)
		assert.Equal(t, "startup", dc.Get())
		assert.Equal(t, telemetry.OriginDefault, dc.Origin())
	})

	t.Run("reset when already at startup is a no-op", func(t *testing.T) {
		dc := newDynamicConfig("test", 100)
		changed := dc.reset()
		assert.False(t, changed)
	})

	t.Run("handleRC with non-nil updates value", func(t *testing.T) {
		dc := newDynamicConfig("test", 1.0)
		rate := 0.5
		changed := dc.HandleRC(&rate)
		assert.True(t, changed)
		assert.Equal(t, 0.5, dc.Get())
		assert.Equal(t, telemetry.OriginRemoteConfig, dc.Origin())
	})

	t.Run("handleRC with nil resets to startup", func(t *testing.T) {
		dc := newDynamicConfig("test", 1.0)
		rate := 0.5
		dc.HandleRC(&rate)
		assert.Equal(t, 0.5, dc.Get())

		changed := dc.HandleRC(nil)
		assert.True(t, changed)
		assert.Equal(t, 1.0, dc.Get())
		assert.Equal(t, telemetry.OriginDefault, dc.Origin())
	})

	t.Run("toTelemetry returns snapshot", func(t *testing.T) {
		dc := newDynamicConfig("my_field", "hello")
		dc.update("world", telemetry.OriginCode)

		cfg := dc.ToTelemetry()
		assert.Equal(t, "my_field", cfg.Name)
		assert.Equal(t, "world", cfg.Value)
		assert.Equal(t, telemetry.OriginCode, cfg.Origin)
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		dc := newDynamicConfig("test", 0)
		var wg sync.WaitGroup

		for i := 0; i < 100; i++ {
			wg.Add(2)
			go func(v int) {
				defer wg.Done()
				dc.update(v, telemetry.OriginCode)
			}(i)
			go func() {
				defer wg.Done()
				_ = dc.Get()
			}()
		}
		wg.Wait()
	})
}
