// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"math"
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
	"github.com/stretchr/testify/assert"
)

func TestDynamicConfig(t *testing.T) {
	t.Run("get returns initial value", func(t *testing.T) {
		dc := newDynamicConfig("test", 42, telemetry.OriginDefault)
		assert.Equal(t, 42, dc.Get())
	})

	t.Run("update changes value", func(t *testing.T) {
		dc := newDynamicConfig("test", "initial", telemetry.OriginDefault)
		changed := dc.update("updated")
		assert.True(t, changed)
		assert.Equal(t, "updated", dc.Get())
	})

	t.Run("update with same value is a no-op", func(t *testing.T) {
		dc := newDynamicConfig("test", 3.14, telemetry.OriginDefault)
		changed := dc.update(3.14)
		assert.False(t, changed)
	})

	t.Run("reset restores startup value", func(t *testing.T) {
		dc := newDynamicConfig("test", "startup", telemetry.OriginDefault)
		dc.update("modified")
		assert.Equal(t, "modified", dc.Get())

		changed := dc.reset()
		assert.True(t, changed)
		assert.Equal(t, "startup", dc.Get())
	})

	t.Run("reset when already at startup is a no-op", func(t *testing.T) {
		dc := newDynamicConfig("test", 100, telemetry.OriginDefault)
		changed := dc.reset()
		assert.False(t, changed)
	})

	t.Run("handleRC with non-nil updates value", func(t *testing.T) {
		dc := newDynamicConfig("test", 1.0, telemetry.OriginDefault)
		rate := 0.5
		changed := dc.HandleRC(&rate)
		assert.True(t, changed)
		assert.Equal(t, 0.5, dc.Get())
	})

	t.Run("handleRC with nil resets to startup", func(t *testing.T) {
		dc := newDynamicConfig("test", 1.0, telemetry.OriginDefault)
		rate := 0.5
		dc.HandleRC(&rate)
		assert.Equal(t, 0.5, dc.Get())

		changed := dc.HandleRC(nil)
		assert.True(t, changed)
		assert.Equal(t, 1.0, dc.Get())
	})

	t.Run("NaN startup is treated as equal on reset", func(t *testing.T) {
		dc := newDynamicConfig("test", math.NaN(), telemetry.OriginDefault)
		changed := dc.reset()
		assert.False(t, changed, "NaN→NaN reset should be a no-op")
	})

	t.Run("NaN update is treated as equal", func(t *testing.T) {
		dc := newDynamicConfig("test", math.NaN(), telemetry.OriginDefault)
		changed := dc.update(math.NaN())
		assert.False(t, changed, "NaN→NaN update should be a no-op")
	})

	t.Run("handleRC update reports OriginRemoteConfig", func(t *testing.T) {
		client := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(client)()

		dc := newDynamicConfig("my_field", 1.0, telemetry.OriginEnvVar)
		rate := 0.5
		dc.HandleRC(&rate)

		assertTelemetryReport(t, client.Configuration, "my_field", 0.5, telemetry.OriginRemoteConfig)
	})

	t.Run("handleRC reset reports startupOrigin=OriginEnvVar", func(t *testing.T) {
		client := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(client)()

		dc := newDynamicConfig("my_field", 1.0, telemetry.OriginEnvVar)
		rate := 0.5
		dc.HandleRC(&rate)
		client.Configuration = nil // clear so we only see the reset report

		dc.HandleRC(nil)

		assertTelemetryReport(t, client.Configuration, "my_field", 1.0, telemetry.OriginEnvVar)
	})

	t.Run("handleRC reset reports startupOrigin=OriginDefault", func(t *testing.T) {
		client := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(client)()

		dc := newDynamicConfig("my_field", 1.0, telemetry.OriginDefault)
		rate := 0.5
		dc.HandleRC(&rate)
		client.Configuration = nil

		dc.HandleRC(nil)

		assertTelemetryReport(t, client.Configuration, "my_field", 1.0, telemetry.OriginDefault)
	})

	t.Run("handleRC no-op reset emits no telemetry", func(t *testing.T) {
		client := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(client)()

		dc := newDynamicConfig("my_field", 1.0, telemetry.OriginEnvVar)
		dc.HandleRC(nil)

		assert.Empty(t, client.Configuration)
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		dc := newDynamicConfig("test", 0, telemetry.OriginDefault)
		var wg sync.WaitGroup

		for i := 0; i < 100; i++ {
			wg.Add(2)
			go func(v int) {
				defer wg.Done()
				dc.update(v)
			}(i)
			go func() {
				defer wg.Done()
				_ = dc.Get()
			}()
		}
		wg.Wait()
	})
}

func assertTelemetryReport(t *testing.T, cfgs []telemetry.Configuration, name string, value any, origin telemetry.Origin) {
	t.Helper()
	for _, c := range cfgs {
		if c.Name == name && c.Value == value && c.Origin == origin {
			return
		}
	}
	t.Errorf("expected telemetry report Name=%q Value=%v Origin=%v not found in %v", name, value, origin, cfgs)
}
