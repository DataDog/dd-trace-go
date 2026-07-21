// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/open-feature/go-sdk/openfeature"
)

// Helper function to create a test configuration
func createTestConfig() *universalFlagsConfiguration {
	return &universalFlagsConfiguration{
		CreatedAt: time.Now(),
		Format:    "SERVER",
		Environment: environment{
			Name: "test",
		},
		Flags: map[string]*flag{
			"bool-flag": {
				Key:           "bool-flag",
				Enabled:       true,
				VariationType: valueTypeBoolean,
				Variations: map[string]*variant{
					"on":  {Key: "on", Value: true},
					"off": {Key: "off", Value: false},
				},
				Allocations: []*allocation{
					{
						Key: "allocation1",
						Rules: []*rule{
							{
								Conditions: []*condition{
									{
										Operator:  operatorOneOf,
										Attribute: "country",
										Value:     []string{"US"},
									},
								},
							},
						},
						Splits: []*split{
							{
								Shards: []*shard{
									{
										Salt: "test",
										Ranges: []*shardRange{
											{Start: 0, End: 8192},
										},
										TotalShards: 8192,
									},
								},
								VariationKey: "on",
							},
						},
					},
				},
			},
			"string-flag": {
				Key:           "string-flag",
				Enabled:       true,
				VariationType: valueTypeString,
				Variations: map[string]*variant{
					"v1": {Key: "v1", Value: "version-1"},
					"v2": {Key: "v2", Value: "version-2"},
				},
				Allocations: []*allocation{
					{
						Key: "allocation1",
						Rules: []*rule{
							{
								Conditions: []*condition{
									{
										Operator:  operatorGTE,
										Attribute: "age",
										Value:     18.0,
									},
								},
							},
						},
						Splits: []*split{
							{
								Shards: []*shard{
									{
										Salt: "test",
										Ranges: []*shardRange{
											{Start: 0, End: 8192},
										},
										TotalShards: 8192,
									},
								},
								VariationKey: "v2",
							},
						},
					},
				},
			},
			"int-flag": {
				Key:           "int-flag",
				Enabled:       true,
				VariationType: valueTypeInteger,
				Variations: map[string]*variant{
					"small": {Key: "small", Value: int64(10)},
					"large": {Key: "large", Value: int64(100)},
				},
				Allocations: []*allocation{
					{
						Key: "allocation1",
						Rules: []*rule{
							{
								Conditions: []*condition{
									{
										Operator:  operatorIsNull,
										Attribute: "premium",
										Value:     false,
									},
								},
							},
						},
						Splits: []*split{
							{
								Shards: []*shard{
									{
										Salt: "test",
										Ranges: []*shardRange{
											{Start: 0, End: 8192},
										},
										TotalShards: 8192,
									},
								},
								VariationKey: "large",
							},
						},
					},
				},
			},
			"float-flag": {
				Key:           "float-flag",
				Enabled:       true,
				VariationType: valueTypeNumeric,
				Variations: map[string]*variant{
					"low":  {Key: "low", Value: 0.5},
					"high": {Key: "high", Value: 1.5},
				},
				Allocations: []*allocation{
					{
						Key: "allocation1",
						Rules: []*rule{
							{
								Conditions: []*condition{
									{
										Operator:  operatorMatches,
										Attribute: "tier",
										Value:     "^premium$",
									},
								},
							},
						},
						Splits: []*split{
							{
								Shards: []*shard{
									{
										Salt: "test",
										Ranges: []*shardRange{
											{Start: 0, End: 8192},
										},
										TotalShards: 8192,
									},
								},
								VariationKey: "high",
							},
						},
					},
				},
			},
			"json-flag": {
				Key:           "json-flag",
				Enabled:       true,
				VariationType: valueTypeJSON,
				Variations: map[string]*variant{
					"config1": {
						Key: "config1",
						Value: map[string]any{
							"timeout": 30,
							"retries": 3,
						},
					},
					"config2": {
						Key: "config2",
						Value: map[string]any{
							"timeout": 60,
							"retries": 5,
						},
					},
				},
				Allocations: []*allocation{
					{
						Key: "allocation1",
						Rules: []*rule{
							{
								Conditions: []*condition{
									{
										Operator:  operatorGT,
										Attribute: "requests",
										Value:     1000.0,
									},
								},
							},
						},
						Splits: []*split{
							{
								Shards: []*shard{
									{
										Salt: "test",
										Ranges: []*shardRange{
											{Start: 0, End: 8192},
										},
										TotalShards: 8192,
									},
								},
								VariationKey: "config2",
							},
						},
					},
				},
			},
			"disabled-flag": {
				Key:           "disabled-flag",
				Enabled:       false,
				VariationType: valueTypeBoolean,
				Variations: map[string]*variant{
					"on": {Key: "on", Value: true},
				},
				Allocations: []*allocation{},
			},
		},
	}
}

func TestNewDatadogProvider(t *testing.T) {
	provider := newDatadogProvider(ProviderConfig{})
	if provider == nil {
		t.Fatal("expected provider to be non-nil")
	}

	metadata := provider.Metadata()
	if metadata.Name != "Datadog Remote Config Provider" {
		t.Errorf("expected provider name to be 'Datadog Remote Config Provider', got %q", metadata.Name)
	}

	hooks := provider.Hooks()
	// 3 hooks: exposure hook + OTel flag eval metrics hook + EVP flagevaluation hook.
	// The EVP hook is enabled by default (DD_FLAGGING_EVALUATION_COUNTS_ENABLED=true).
	if len(hooks) != 3 {
		t.Errorf("expected 3 hooks (exposure + flag eval metrics + EVP flagevaluation), got %d", len(hooks))
	}
}

// TestEvaluateStampsEvalTimeMetadata verifies the provider stamps evaluation-entry
// time into FlagMetadata on every path so EVP first/last bounds use eval-time.
func TestEvaluateStampsEvalTimeMetadata(t *testing.T) {
	provider := newDatadogProvider(ProviderConfig{})
	provider.updateConfiguration(createTestConfig())
	ctx := context.Background()

	cases := []struct {
		name    string
		flagKey string
		flatCtx openfeature.FlattenedContext
	}{
		{"matched allocation", "bool-flag", openfeature.FlattenedContext{"targetingKey": "user-123", "country": "US"}},
		{"default (no match)", "bool-flag", openfeature.FlattenedContext{"targetingKey": "user-123", "country": "CA"}},
		{"disabled flag", "disabled-flag", openfeature.FlattenedContext{"targetingKey": "user-123"}},
		{"flag not found", "nonexistent-flag", openfeature.FlattenedContext{"targetingKey": "user-123"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := time.Now().UnixMilli()
			result := provider.BooleanEvaluation(ctx, tc.flagKey, false, tc.flatCtx)
			after := time.Now().UnixMilli()

			got, ok := result.FlagMetadata[metadataEvalTimeKey].(int64)
			if !ok {
				t.Fatalf("FlagMetadata[%q] missing or wrong type on %q path; metadata=%v",
					metadataEvalTimeKey, tc.name, result.FlagMetadata)
			}
			if got < before || got > after {
				t.Fatalf("FlagMetadata[%q] = %d, want within [%d,%d] on %q path",
					metadataEvalTimeKey, got, before, after, tc.name)
			}
		})
	}
}

func TestBooleanEvaluation(t *testing.T) {
	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	ctx := context.Background()

	t.Run("matching allocation returns true", func(t *testing.T) {
		flatCtx := openfeature.FlattenedContext{
			"targetingKey": "user-123",
			"country":      "US",
		}

		result := provider.BooleanEvaluation(ctx, "bool-flag", false, flatCtx)
		if result.Value != true {
			t.Errorf("expected true, got %v", result.Value)
		}
		if result.Reason != openfeature.TargetingMatchReason {
			t.Errorf("expected TargetingMatchReason, got %s", result.Reason)
		}
		if result.Variant != "on" {
			t.Errorf("expected variant 'on', got %q", result.Variant)
		}
	})

	t.Run("no matching allocation returns default", func(t *testing.T) {
		flatCtx := openfeature.FlattenedContext{
			"targetingKey": "user-123",
			"country":      "CA",
		}

		result := provider.BooleanEvaluation(ctx, "bool-flag", false, flatCtx)
		if result.Value != false {
			t.Errorf("expected false, got %v", result.Value)
		}
		if result.Reason != openfeature.DefaultReason {
			t.Errorf("expected DefaultReason, got %s", result.Reason)
		}
	})

	t.Run("disabled flag returns default", func(t *testing.T) {
		flatCtx := openfeature.FlattenedContext{
			"targetingKey": "user-123",
		}

		result := provider.BooleanEvaluation(ctx, "disabled-flag", false, flatCtx)
		if result.Value != false {
			t.Errorf("expected false, got %v", result.Value)
		}
		if result.Reason != openfeature.DisabledReason {
			t.Errorf("expected DisabledReason, got %s", result.Reason)
		}
	})

	t.Run("flag not found returns error", func(t *testing.T) {
		flatCtx := openfeature.FlattenedContext{
			"targetingKey": "user-123",
		}

		result := provider.BooleanEvaluation(ctx, "nonexistent-flag", false, flatCtx)
		if result.Value != false {
			t.Errorf("expected false, got %v", result.Value)
		}
		if result.Reason != openfeature.ErrorReason {
			t.Errorf("expected ErrorReason, got %s", result.Reason)
		}
		if result.ResolutionError.Error() == "" {
			t.Error("expected error message")
		}
	})
}

func TestStringEvaluation(t *testing.T) {
	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	ctx := context.Background()

	t.Run("matching allocation returns correct variant", func(t *testing.T) {
		flatCtx := openfeature.FlattenedContext{
			"targetingKey": "user-123",
			"age":          25,
		}

		result := provider.StringEvaluation(ctx, "string-flag", "default", flatCtx)
		if result.Value != "version-2" {
			t.Errorf("expected 'version-2', got %q", result.Value)
		}
		if result.Reason != openfeature.TargetingMatchReason {
			t.Errorf("expected TargetingMatchReason, got %s", result.Reason)
		}
		if result.Variant != "v2" {
			t.Errorf("expected variant 'v2', got %q", result.Variant)
		}
	})

	t.Run("no matching allocation returns default", func(t *testing.T) {
		flatCtx := openfeature.FlattenedContext{
			"targetingKey": "user-123",
			"age":          15,
		}

		result := provider.StringEvaluation(ctx, "string-flag", "default", flatCtx)
		if result.Value != "default" {
			t.Errorf("expected 'default', got %q", result.Value)
		}
		if result.Reason != openfeature.DefaultReason {
			t.Errorf("expected DefaultReason, got %s", result.Reason)
		}
	})
}

func TestIntEvaluation(t *testing.T) {
	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	ctx := context.Background()

	t.Run("matching allocation returns correct value", func(t *testing.T) {
		flatCtx := openfeature.FlattenedContext{
			"targetingKey": "user-123",
			"premium":      "yes",
		}

		result := provider.IntEvaluation(ctx, "int-flag", 5, flatCtx)
		if result.Value != 100 {
			t.Errorf("expected 100, got %d", result.Value)
		}
		if result.Reason != openfeature.TargetingMatchReason {
			t.Errorf("expected TargetingMatchReason, got %s", result.Reason)
		}
	})

	t.Run("no matching allocation returns default", func(t *testing.T) {
		flatCtx := openfeature.FlattenedContext{
			"targetingKey": "user-123",
		}

		result := provider.IntEvaluation(ctx, "int-flag", 5, flatCtx)
		if result.Value != 5 {
			t.Errorf("expected 5, got %d", result.Value)
		}
		if result.Reason != openfeature.DefaultReason {
			t.Errorf("expected DefaultReason, got %s", result.Reason)
		}
	})
}

func TestFloatEvaluation(t *testing.T) {
	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	ctx := context.Background()

	t.Run("matching allocation returns correct value", func(t *testing.T) {
		flatCtx := openfeature.FlattenedContext{
			"targetingKey": "user-123",
			"tier":         "premium",
		}

		result := provider.FloatEvaluation(ctx, "float-flag", 0.0, flatCtx)
		if result.Value != 1.5 {
			t.Errorf("expected 1.5, got %f", result.Value)
		}
		if result.Reason != openfeature.TargetingMatchReason {
			t.Errorf("expected TargetingMatchReason, got %s", result.Reason)
		}
	})

	t.Run("no matching allocation returns default", func(t *testing.T) {
		flatCtx := openfeature.FlattenedContext{
			"targetingKey": "user-123",
			"tier":         "basic",
		}

		result := provider.FloatEvaluation(ctx, "float-flag", 0.0, flatCtx)
		if result.Value != 0.0 {
			t.Errorf("expected 0.0, got %f", result.Value)
		}
		if result.Reason != openfeature.DefaultReason {
			t.Errorf("expected DefaultReason, got %s", result.Reason)
		}
	})
}

func TestObjectEvaluation(t *testing.T) {
	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	ctx := context.Background()

	t.Run("matching allocation returns correct object", func(t *testing.T) {
		flatCtx := openfeature.FlattenedContext{
			"targetingKey": "user-123",
			"requests":     1500,
		}

		result := provider.ObjectEvaluation(ctx, "json-flag", nil, flatCtx)
		if result.Value == nil {
			t.Fatal("expected non-nil value")
		}

		objValue, ok := result.Value.(map[string]any)
		if !ok {
			t.Fatalf("expected map[string]interface{}, got %T", result.Value)
		}

		if objValue["timeout"] != 60 {
			t.Errorf("expected timeout=60, got %v", objValue["timeout"])
		}
		if objValue["retries"] != 5 {
			t.Errorf("expected retries=5, got %v", objValue["retries"])
		}

		if result.Reason != openfeature.TargetingMatchReason {
			t.Errorf("expected TargetingMatchReason, got %s", result.Reason)
		}
	})

	t.Run("no matching allocation returns default", func(t *testing.T) {
		flatCtx := openfeature.FlattenedContext{
			"targetingKey": "user-123",
			"requests":     500,
		}

		defaultObj := map[string]any{"default": true}
		result := provider.ObjectEvaluation(ctx, "json-flag", defaultObj, flatCtx)

		if result.Reason != openfeature.DefaultReason {
			t.Errorf("expected DefaultReason, got %s", result.Reason)
		}
	})
}

func TestProviderWithoutConfiguration(t *testing.T) {
	provider := newDatadogProvider(ProviderConfig{})
	ctx := context.Background()

	flatCtx := openfeature.FlattenedContext{
		"targetingKey": "user-123",
	}

	t.Run("boolean evaluation without config returns error", func(t *testing.T) {
		result := provider.BooleanEvaluation(ctx, "bool-flag", false, flatCtx)
		if result.Reason != openfeature.ErrorReason {
			t.Errorf("expected ErrorReason, got %s", result.Reason)
		}
	})

	t.Run("string evaluation without config returns error", func(t *testing.T) {
		result := provider.StringEvaluation(ctx, "string-flag", "default", flatCtx)
		if result.Reason != openfeature.ErrorReason {
			t.Errorf("expected ErrorReason, got %s", result.Reason)
		}
	})
}

func TestProviderConfigurationUpdate(t *testing.T) {
	provider := newDatadogProvider(ProviderConfig{})

	// Initially no config
	if provider.getConfiguration() != nil {
		t.Error("expected nil configuration initially")
	}

	// Update config
	config := createTestConfig()
	provider.updateConfiguration(config)

	// Verify config was updated
	if provider.getConfiguration() == nil {
		t.Error("expected configuration to be set")
	}

	if provider.getConfiguration().Environment.Name != "test" {
		t.Errorf("expected environment 'test', got %q", provider.getConfiguration().Environment.Name)
	}
}

func TestConcurrentEvaluations(t *testing.T) {
	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	ctx := context.Background()
	flatCtx := openfeature.FlattenedContext{
		"targetingKey": "user-123",
		"country":      "US",
	}

	// Run multiple concurrent evaluations
	done := make(chan bool)
	for range 10 {
		go func() {
			result := provider.BooleanEvaluation(ctx, "bool-flag", false, flatCtx)
			if result.Value != true {
				t.Errorf("expected true, got %v", result.Value)
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for range 10 {
		<-done
	}
}

func TestSetProviderWithContextAndWaitNoConfig(t *testing.T) {
	// A provider whose RC subscription is live but has not yet received a
	// configuration must still become ready — flag evaluations resolve to
	// their caller-supplied defaults until the first config arrives. This
	// is the steady state for services with no flags targeted at them and
	// for the brief window after container restart before the agent
	// re-delivers the config; either case previously deadlocked
	// InitWithContext until its own timeout, defeating startup probes.
	provider := newDatadogProvider(ProviderConfig{})

	// Short caller deadline — well under initialConfigWait — proves Init
	// does not consume its full opportunistic wait when the caller wants
	// out sooner.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := openfeature.SetProviderWithContextAndWait(ctx, provider)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected no error becoming ready without a configuration, got: %v", err)
	}
	// Give the caller ctx some slack for scheduling — we care that Init
	// respected it rather than waiting the full initialConfigWait budget.
	if elapsed > 500*time.Millisecond {
		t.Errorf("Init took %v — expected it to honor the caller's 50ms deadline, not wait for initialConfigWait", elapsed)
	}
}

func TestSetProviderWithContextAndWaitConfigArrivesDuringInit(t *testing.T) {
	// A configuration update that arrives while Init is waiting should
	// unblock Init early — the opportunistic wait exists precisely to
	// pick up real config when it lands within a normal poll interval.
	provider := newDatadogProvider(ProviderConfig{})

	go func() {
		time.Sleep(20 * time.Millisecond)
		provider.updateConfiguration(createTestConfig())
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	err := openfeature.SetProviderWithContextAndWait(ctx, provider)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("Init took %v — expected it to unblock as soon as the config arrived (~20ms)", elapsed)
	}
	if provider.getConfiguration() == nil {
		t.Error("configuration should be set after Init")
	}
}

func TestSetProviderWithContextAndWaitSuccess(t *testing.T) {
	// Create a provider and set up its configuration immediately
	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	// Use a reasonable timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Set the provider with context and wait - should succeed quickly
	err := openfeature.SetProviderWithContextAndWait(ctx, provider)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify the provider was set by doing a flag evaluation
	client := openfeature.NewClient("test-client")
	evalCtx := openfeature.NewEvaluationContext("user-123", map[string]any{
		"country": "US",
	})
	result, err := client.BooleanValue(context.Background(), "bool-flag", false, evalCtx)

	if err != nil {
		t.Fatalf("flag evaluation failed: %v", err)
	}

	if result != true {
		t.Errorf("expected flag evaluation to return true, got %v", result)
	}

	t.Log("Successfully set provider with context and performed flag evaluation")
}

func TestShutdownWithContextTimeout(t *testing.T) {
	// Create and configure a provider
	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	// Use a very short timeout context for shutdown (1ms)
	// This tests the timeout behavior of ShutdownWithContext
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// Try to shutdown with context - should respect the timeout
	err := provider.ShutdownWithContext(ctx)

	// For test providers without remote config, we expect a specific error
	// The key is that the method respects the context and doesn't hang
	if err != nil {
		t.Logf("Shutdown returned error: %v", err)

		// We expect either a timeout error OR a remote config error (both are valid)
		// The important thing is that the method doesn't hang and respects context
		if errors.Is(err, context.DeadlineExceeded) {
			t.Log("Got expected timeout error")
		} else if err.Error() == "failed to unregister OpenFeature product: remote config client not started" {
			t.Log("Got expected remote config error (test provider has no remote config)")
		} else {
			t.Errorf("unexpected error type: %v", err)
		}
	} else {
		t.Log("Shutdown completed within timeout")
	}
}

func TestShutdownWithContextSuccess(t *testing.T) {
	// Create and configure a provider
	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	// Use a reasonable timeout context for shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Shutdown with adequate timeout
	err := provider.ShutdownWithContext(ctx)

	// For test providers without remote config, we expect a specific error
	// but the shutdown should complete without timing out
	if err != nil {
		if err.Error() == "failed to unregister OpenFeature product: remote config client not started" {
			t.Log("Got expected remote config error (test provider has no remote config client)")
		} else {
			t.Errorf("unexpected error: %v", err)
		}
	} else {
		t.Log("Shutdown completed successfully")
	}
}
