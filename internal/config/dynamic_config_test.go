// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestDynamicConfig(t *testing.T) {
	t.Run("get returns initial value", func(t *testing.T) {
		dc := newDynamicConfig("test", 42, telemetry.OriginDefault, func(a, b int) bool { return a == b })
		assert.Equal(t, 42, dc.Get())
	})

	t.Run("handleRC with non-nil updates value", func(t *testing.T) {
		dc := newDynamicConfig("test", 1.0, telemetry.OriginDefault, equalFloat)
		rate := 0.5
		changed := dc.HandleRC(&rate)
		assert.True(t, changed)
		assert.Equal(t, 0.5, dc.Get())
	})

	t.Run("handleRC with nil resets to startup", func(t *testing.T) {
		dc := newDynamicConfig("test", 1.0, telemetry.OriginDefault, equalFloat)
		rate := 0.5
		dc.HandleRC(&rate)
		assert.Equal(t, 0.5, dc.Get())

		changed := dc.HandleRC(nil)
		assert.True(t, changed)
		assert.Equal(t, 1.0, dc.Get())
	})

	t.Run("handleRC with same value is a no-op", func(t *testing.T) {
		dc := newDynamicConfig("test", 3.14, telemetry.OriginDefault, equalFloat)
		same := 3.14
		changed := dc.HandleRC(&same)
		assert.False(t, changed)
	})

	t.Run("handleRC reset when already at startup is a no-op", func(t *testing.T) {
		dc := newDynamicConfig("test", 100, telemetry.OriginDefault, func(a, b int) bool { return a == b })
		changed := dc.HandleRC(nil)
		assert.False(t, changed)
	})

	t.Run("NaN startup is treated as equal on reset", func(t *testing.T) {
		dc := newDynamicConfig("test", math.NaN(), telemetry.OriginDefault, equalFloat)
		changed := dc.HandleRC(nil)
		assert.False(t, changed, "NaN→NaN reset should be a no-op")
	})

	t.Run("NaN update is treated as equal", func(t *testing.T) {
		dc := newDynamicConfig("test", math.NaN(), telemetry.OriginDefault, equalFloat)
		nan := math.NaN()
		changed := dc.HandleRC(&nan)
		assert.False(t, changed, "NaN→NaN update should be a no-op")
	})

	t.Run("NaN full cycle: update then reset", func(t *testing.T) {
		dc := newDynamicConfig("test", math.NaN(), telemetry.OriginDefault, equalFloat)
		rate := 0.5
		changed := dc.HandleRC(&rate)
		assert.True(t, changed)
		assert.Equal(t, 0.5, dc.Get())

		changed = dc.HandleRC(nil)
		assert.True(t, changed)
		assert.True(t, math.IsNaN(dc.Get()), "should reset back to NaN")
	})

	t.Run("handleRC update reports OriginRemoteConfig", func(t *testing.T) {
		client := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(client)()

		dc := newDynamicConfig("my_field", 1.0, telemetry.OriginEnvVar, equalFloat)
		rate := 0.5
		dc.HandleRC(&rate)

		assertTelemetryReport(t, client.Configuration, "my_field", 0.5, telemetry.OriginRemoteConfig)
	})

	t.Run("handleRC reset reports startupOrigin=OriginEnvVar", func(t *testing.T) {
		client := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(client)()

		dc := newDynamicConfig("my_field", 1.0, telemetry.OriginEnvVar, equalFloat)
		rate := 0.5
		dc.HandleRC(&rate)
		client.Configuration = nil // clear so we only see the reset report

		dc.HandleRC(nil)

		assertTelemetryReport(t, client.Configuration, "my_field", 1.0, telemetry.OriginEnvVar)
	})

	t.Run("handleRC reset reports startupOrigin=OriginDefault", func(t *testing.T) {
		client := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(client)()

		dc := newDynamicConfig("my_field", 1.0, telemetry.OriginDefault, equalFloat)
		rate := 0.5
		dc.HandleRC(&rate)
		client.Configuration = nil

		dc.HandleRC(nil)

		assertTelemetryReport(t, client.Configuration, "my_field", 1.0, telemetry.OriginDefault)
	})

	t.Run("handleRC no-op reset emits no telemetry", func(t *testing.T) {
		client := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(client)()

		dc := newDynamicConfig("my_field", 1.0, telemetry.OriginEnvVar, equalFloat)
		dc.HandleRC(nil)

		assert.Empty(t, client.Configuration)
	})

	t.Run("setBaseline updates RC reset target", func(t *testing.T) {
		// Simulates: env var sets NaN, then programmatic override sets 1.0,
		// then RC pushes 0.5, then RC resets. Should reset to 1.0 (the
		// programmatic baseline), not NaN (the original env var value).
		dc := newDynamicConfig("test", math.NaN(), telemetry.OriginDefault, equalFloat)
		dc.setBaseline(1.0, telemetry.OriginCode)

		rate := 0.5
		dc.HandleRC(&rate)
		assert.Equal(t, 0.5, dc.Get())

		dc.HandleRC(nil)
		assert.Equal(t, 1.0, dc.Get())
	})

	t.Run("setBaseline updates startupOrigin for telemetry", func(t *testing.T) {
		client := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(client)()

		dc := newDynamicConfig("my_field", math.NaN(), telemetry.OriginDefault, equalFloat)
		dc.setBaseline(1.0, telemetry.OriginCode)

		rate := 0.5
		dc.HandleRC(&rate)
		client.Configuration = nil

		dc.HandleRC(nil)
		assertTelemetryReport(t, client.Configuration, "my_field", 1.0, telemetry.OriginCode)
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		dc := newDynamicConfig("test", 0, telemetry.OriginDefault, func(a, b int) bool { return a == b })
		var wg sync.WaitGroup

		for i := range 100 {
			wg.Add(3)
			go func(v int) {
				defer wg.Done()
				dc.HandleRC(&v)
			}(i)
			go func(v int) {
				defer wg.Done()
				dc.setBaseline(v, telemetry.OriginCode)
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
