// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGet(t *testing.T) {
	t.Run("returns non-nil config", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		cfg := Get()
		assert.NotNil(t, cfg, "Get() should never return nil")
	})

	t.Run("singleton behavior - returns same instance", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		cfg1 := Get()
		cfg2 := Get()
		cfg3 := Get()

		// All calls should return the same instance
		assert.Same(t, cfg1, cfg2, "First and second Get() calls should return the same instance")
		assert.Same(t, cfg1, cfg3, "First and third Get() calls should return the same instance")
	})

	t.Run("fresh config flag forces new instance", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		// Get the first instance
		cfg1 := Get()
		require.NotNil(t, cfg1)

		// Enable fresh config to allow us to create new instances
		SetUseFreshConfig(true)

		// Get should now return a new instance
		cfg2 := Get()
		require.NotNil(t, cfg2)
		assert.NotSame(t, cfg1, cfg2, "With useFreshConfig=true, Get() should return a new instance")

		// Another call with useFreshConfig still true should return another new instance
		cfg3 := Get()
		require.NotNil(t, cfg3)
		assert.NotSame(t, cfg2, cfg3, "With useFreshConfig=true, each Get() call should return a new instance")

		// Disable fresh config to allow us to cache the same instance
		SetUseFreshConfig(false)

		// Now it should cache the same instance
		cfg4 := Get()
		cfg5 := Get()
		assert.Same(t, cfg4, cfg5, "With useFreshConfig=false, Get() should cache the same instance")
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		const numGoroutines = 100
		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		// All goroutines should get a non-nil config
		configs := make([]*Config, numGoroutines)

		for i := range numGoroutines {
			go func(j int) {
				defer wg.Done()
				configs[j] = Get()
			}(i)
		}

		wg.Wait()

		// All configs should be non-nil
		for i, cfg := range configs {
			assert.NotNil(t, cfg, "Config at index %d should not be nil", i)
		}

		// All configs should be the same instance (singleton)
		firstConfig := configs[0]
		for i, cfg := range configs[1:] {
			assert.Same(t, firstConfig, cfg, "Config at index %d should be the same instance", i+1)
		}
	})

	t.Run("concurrent access with fresh config", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		// Enable fresh config to allow us to create new instances
		SetUseFreshConfig(true)

		const numGoroutines = 50
		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		// Track if we get different instances (which is expected with useFreshConfig=true)
		var uniqueInstances sync.Map
		var configCount atomic.Int32

		for range numGoroutines {
			go func() {
				defer wg.Done()
				cfg := Get()
				require.NotNil(t, cfg, "Get() should not return nil even under concurrent access")

				// Track unique instances
				if _, loaded := uniqueInstances.LoadOrStore(cfg, true); !loaded {
					configCount.Add(1)
				}
			}()
		}

		wg.Wait()

		// With useFreshConfig=true, we should get multiple different instances
		count := configCount.Load()
		assert.Greater(t, count, int32(1), "With useFreshConfig=true, should get multiple different instances")
	})

	t.Run("config is properly initialized with values", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		// Set an environment variable to ensure it's loaded
		t.Setenv("DD_TRACE_DEBUG", "true")

		cfg := Get()
		require.NotNil(t, cfg)

		// Verify that config values are accessible (using the Debug() method)
		debug := cfg.Debug()
		assert.True(t, debug, "Config should have loaded DD_TRACE_DEBUG=true")
	})

	t.Run("Setter methods update config and maintain thread-safety", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		cfg := Get()
		require.NotNil(t, cfg)

		initialDebug := cfg.Debug()
		cfg.SetDebug(!initialDebug, "test")
		assert.Equal(t, !initialDebug, cfg.Debug(), "Debug setting should have changed")

		// Verify concurrent reads don't panic
		const numReaders = 100
		var wg sync.WaitGroup
		wg.Add(numReaders)

		for range numReaders {
			go func() {
				defer wg.Done()
				_ = cfg.Debug()
			}()
		}

		wg.Wait()
	})

	t.Run("SetDebug concurrent with reads is safe", func(t *testing.T) {
		resetGlobalState()
		defer resetGlobalState()

		cfg := Get()
		require.NotNil(t, cfg)

		var wg sync.WaitGroup
		const numOperations = 100

		// Start readers
		wg.Add(numOperations)
		for range numOperations {
			go func() {
				defer wg.Done()
				_ = cfg.Debug()
			}()
		}

		// Start writers
		wg.Add(numOperations)
		for i := range numOperations {
			go func(val bool) {
				defer wg.Done()
				cfg.SetDebug(val, "test")
			}(i%2 == 0)
		}

		wg.Wait()

		// Should not panic and should have a valid boolean value
		finalDebug := cfg.Debug()
		assert.IsType(t, true, finalDebug)
	})
}

// settersWithoutTelemetry lists Set methods that don't report telemetry.
// Add your setter here with a reason if telemetry reporting is not needed.
var settersWithoutTelemetry = map[string]string{
	"SetLogToStdout":      "not user-configurable",
	"SetIsLambdaFunction": "not user-configurable",
}

// specialCaseSetters handles setters with non-standard signatures.
// Add here if signature is not: SetX(value T, origin telemetry.Origin)
var specialCaseSetters = map[string]func(*Config, telemetry.Origin){
	"SetServiceMapping": func(c *Config, origin telemetry.Origin) {
		c.SetServiceMapping("from-service", "to-service", origin)
	},
}

// TestAllSettersReportTelemetry verifies Set* methods report telemetry with seqID > defaultSeqID.
// If this fails: call reportTelemetry() in your setter, OR add to settersWithoutTelemetry, OR add to specialCaseSetters.
func TestAllSettersReportTelemetry(t *testing.T) {
	// Get all methods on *Config
	configType := reflect.TypeOf(&Config{})

	for i := 0; i < configType.NumMethod(); i++ {
		// Capture method
		method := configType.Method(i)
		methodName := method.Name

		// Skip if not a Set method
		if len(methodName) < 3 || methodName[:3] != "Set" {
			continue
		}

		// Skip if in exclusion list
		if reason, excluded := settersWithoutTelemetry[methodName]; excluded {
			t.Logf("Skipping %s: %s", methodName, reason)
			continue
		}

		t.Run(methodName, func(t *testing.T) {
			resetGlobalState()
			defer resetGlobalState()

			// Mock telemetry client
			telemetryClient := new(telemetrytest.MockClient)
			telemetryClient.On("RegisterAppConfigs", mock.Anything).Return().Maybe()
			defer telemetry.MockClient(telemetryClient)()

			cfg := Get()
			testOrigin := telemetry.OriginCode

			// Check if this is a special case
			if callFunc, isSpecial := specialCaseSetters[methodName]; isSpecial {
				callFunc(cfg, testOrigin)
			} else {
				// Try to call the method generically
				callSetter(t, cfg, method, testOrigin)
			}

			// Verify telemetry was reported with seqID > defaultSeqID
			foundTelemetry := false
			for _, call := range telemetryClient.Calls {
				if call.Method == "RegisterAppConfigs" {
					if len(call.Arguments) > 0 {
						if configs, ok := call.Arguments[0].([]telemetry.Configuration); ok && len(configs) > 0 {
							config := configs[0]
							if config.Origin == testOrigin && config.SeqID > defaultSeqID {
								foundTelemetry = true
								break
							}
						}
					}
				}
			}

			assert.True(t, foundTelemetry,
				"%s: no telemetry with origin=%v and seqID > %d. Fix: call reportTelemetry() OR add to settersWithoutTelemetry/specialCaseSetters",
				methodName, testOrigin, defaultSeqID)
		})
	}
}

// callSetter attempts to call a setter method with appropriate test values
func callSetter(t *testing.T, cfg *Config, method reflect.Method, origin telemetry.Origin) {
	methodType := method.Type

	// Verify it has the right number of parameters (receiver + value param(s) + origin)
	if methodType.NumIn() < 3 {
		t.Fatalf("%s: expected ≥3 params (receiver, value, origin), got %d. Add to specialCaseSetters if non-standard.",
			method.Name, methodType.NumIn())
	}

	// Last parameter should be telemetry.Origin
	originType := reflect.TypeOf((*telemetry.Origin)(nil)).Elem()
	lastParamType := methodType.In(methodType.NumIn() - 1)
	if lastParamType != originType {
		t.Fatalf("%s: last param should be telemetry.Origin, got %v. Add to specialCaseSetters if non-standard.",
			method.Name, lastParamType)
	}

	// Build arguments
	args := []reflect.Value{reflect.ValueOf(cfg)}

	// Add value parameters (all except receiver and origin)
	for i := 1; i < methodType.NumIn()-1; i++ {
		paramType := methodType.In(i)
		testValue := getTestValueForType(paramType)
		args = append(args, reflect.ValueOf(testValue))
	}

	// Add origin parameter
	args = append(args, reflect.ValueOf(origin))

	// Call the method
	method.Func.Call(args)
}

// getTestValueForType generates appropriate test values based on parameter type.
// Add support for new types here as setters with new parameter types are added.
func getTestValueForType(t reflect.Type) interface{} {
	// Check for specific named types first (before kind checks)
	if t == reflect.TypeOf(time.Duration(0)) {
		return 10 * time.Second
	}

	// Then check by kind
	switch t.Kind() {
	case reflect.Bool:
		return true
	case reflect.String:
		return "test-value"
	case reflect.Int:
		return 42
	case reflect.Float64:
		return 0.75
	case reflect.Slice:
		if t.Elem().Kind() == reflect.String {
			return []string{"feature1", "feature2"}
		}
	}

	panic("getTestValueForType: unsupported parameter type: " + t.String() +
		". Add support for this type in getTestValueForType() or add your setter to specialCaseSetters.")
}

// resetGlobalState resets all global singleton state for testing
func resetGlobalState() {
	mu = sync.Mutex{}
	instance = nil
	useFreshConfig = false
}
