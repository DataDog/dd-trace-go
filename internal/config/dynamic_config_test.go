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
		dc := newDynamicConfig("test", 42, telemetry.OriginDefault, func(a, b int) bool { return a == b }, nil)
		assert.Equal(t, 42, dc.Get())
	})

	t.Run("handleRC with non-nil updates value", func(t *testing.T) {
		dc := newDynamicConfig("test", 1.0, telemetry.OriginDefault, equalFloat, nil)
		rate := 0.5
		changed := dc.HandleRC(&rate)
		assert.True(t, changed)
		assert.Equal(t, 0.5, dc.Get())
	})

	t.Run("handleRC with nil resets to startup", func(t *testing.T) {
		dc := newDynamicConfig("test", 1.0, telemetry.OriginDefault, equalFloat, nil)
		rate := 0.5
		dc.HandleRC(&rate)
		assert.Equal(t, 0.5, dc.Get())

		changed := dc.HandleRC(nil)
		assert.True(t, changed)
		assert.Equal(t, 1.0, dc.Get())
	})

	t.Run("handleRC with same value is a no-op", func(t *testing.T) {
		dc := newDynamicConfig("test", 3.14, telemetry.OriginDefault, equalFloat, nil)
		same := 3.14
		changed := dc.HandleRC(&same)
		assert.False(t, changed)
	})

	t.Run("handleRC reset when already at startup is a no-op", func(t *testing.T) {
		dc := newDynamicConfig("test", 100, telemetry.OriginDefault, func(a, b int) bool { return a == b }, nil)
		changed := dc.HandleRC(nil)
		assert.False(t, changed)
	})

	t.Run("NaN startup is treated as equal on reset", func(t *testing.T) {
		dc := newDynamicConfig("test", math.NaN(), telemetry.OriginDefault, equalFloat, nil)
		changed := dc.HandleRC(nil)
		assert.False(t, changed, "NaN→NaN reset should be a no-op")
	})

	t.Run("NaN update is treated as equal", func(t *testing.T) {
		dc := newDynamicConfig("test", math.NaN(), telemetry.OriginDefault, equalFloat, nil)
		nan := math.NaN()
		changed := dc.HandleRC(&nan)
		assert.False(t, changed, "NaN→NaN update should be a no-op")
	})

	t.Run("NaN full cycle: update then reset", func(t *testing.T) {
		dc := newDynamicConfig("test", math.NaN(), telemetry.OriginDefault, equalFloat, nil)
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

		dc := newDynamicConfig("my_field", 1.0, telemetry.OriginEnvVar, equalFloat, nil)
		rate := 0.5
		dc.HandleRC(&rate)

		assertTelemetryReport(t, client.Configuration, "my_field", 0.5, telemetry.OriginRemoteConfig)
	})

	t.Run("handleRC reset reports startupOrigin=OriginEnvVar", func(t *testing.T) {
		client := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(client)()

		dc := newDynamicConfig("my_field", 1.0, telemetry.OriginEnvVar, equalFloat, nil)
		rate := 0.5
		dc.HandleRC(&rate)
		client.Configuration = nil // clear so we only see the reset report

		dc.HandleRC(nil)

		assertTelemetryReport(t, client.Configuration, "my_field", 1.0, telemetry.OriginEnvVar)
	})

	t.Run("handleRC reset reports startupOrigin=OriginDefault", func(t *testing.T) {
		client := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(client)()

		dc := newDynamicConfig("my_field", 1.0, telemetry.OriginDefault, equalFloat, nil)
		rate := 0.5
		dc.HandleRC(&rate)
		client.Configuration = nil

		dc.HandleRC(nil)

		assertTelemetryReport(t, client.Configuration, "my_field", 1.0, telemetry.OriginDefault)
	})

	t.Run("handleRC no-op reset emits no telemetry", func(t *testing.T) {
		client := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(client)()

		dc := newDynamicConfig("my_field", 1.0, telemetry.OriginEnvVar, equalFloat, nil)
		dc.HandleRC(nil)

		assert.Empty(t, client.Configuration)
	})

	t.Run("setBaseline updates RC reset target", func(t *testing.T) {
		// Simulates: env var sets NaN, then programmatic override sets 1.0,
		// then RC pushes 0.5, then RC resets. Should reset to 1.0 (the
		// programmatic baseline), not NaN (the original env var value).
		dc := newDynamicConfig("test", math.NaN(), telemetry.OriginDefault, equalFloat, nil)
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

		dc := newDynamicConfig("my_field", math.NaN(), telemetry.OriginDefault, equalFloat, nil)
		dc.setBaseline(1.0, telemetry.OriginCode)

		rate := 0.5
		dc.HandleRC(&rate)
		client.Configuration = nil

		dc.HandleRC(nil)
		assertTelemetryReport(t, client.Configuration, "my_field", 1.0, telemetry.OriginCode)
	})

	t.Run("Baseline returns startup value and origin", func(t *testing.T) {
		dc := newDynamicConfig("test", 1.0, telemetry.OriginEnvVar, equalFloat, nil)
		val, origin := dc.Baseline()
		assert.Equal(t, 1.0, val)
		assert.Equal(t, telemetry.OriginEnvVar, origin)
	})

	t.Run("Baseline unchanged by HandleRC", func(t *testing.T) {
		dc := newDynamicConfig("test", 1.0, telemetry.OriginEnvVar, equalFloat, nil)
		rate := 0.5
		dc.HandleRC(&rate)
		val, origin := dc.Baseline()
		assert.Equal(t, 1.0, val, "baseline value should not reflect RC update")
		assert.Equal(t, telemetry.OriginEnvVar, origin, "baseline origin should not reflect RC")
		assert.Equal(t, 0.5, dc.Get(), "current value should reflect RC update")
	})

	t.Run("Baseline reflects setBaseline updates", func(t *testing.T) {
		dc := newDynamicConfig("test", 1.0, telemetry.OriginDefault, equalFloat, nil)
		dc.setBaseline(2.0, telemetry.OriginCode)
		val, origin := dc.Baseline()
		assert.Equal(t, 2.0, val)
		assert.Equal(t, telemetry.OriginCode, origin)
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		dc := newDynamicConfig("test", 0, telemetry.OriginDefault, func(a, b int) bool { return a == b }, nil)
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
	t.Run("non-comparable values do not panic", func(t *testing.T) {
		// WithGlobalTag accepts any value, including slices, on which == would
		// panic; equalMap must compare them with reflect.DeepEqual instead.
		a := map[string]any{"k": []string{"x", "y"}}
		b := map[string]any{"k": []string{"x", "y"}}
		assert.True(t, equalMap(a, b))
		c := map[string]any{"k": []string{"x", "z"}}
		assert.False(t, equalMap(a, c))
	})
}
