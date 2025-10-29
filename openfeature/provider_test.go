// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
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
						Value: map[string]interface{}{
							"timeout": 30,
							"retries": 3,
						},
					},
					"config2": {
						Key: "config2",
						Value: map[string]interface{}{
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
	provider := newDatadogProvider()
	if provider == nil {
		t.Fatal("expected provider to be non-nil")
	}

	metadata := provider.Metadata()
	if metadata.Name != "Datadog Remote Config Provider" {
		t.Errorf("expected provider name to be 'Datadog Remote Config Provider', got %q", metadata.Name)
	}

	hooks := provider.Hooks()
	if len(hooks) != 0 {
		t.Errorf("expected no hooks, got %d", len(hooks))
	}
}

func TestBooleanEvaluation(t *testing.T) {
	provider := newDatadogProvider()
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
	provider := newDatadogProvider()
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
	provider := newDatadogProvider()
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
	provider := newDatadogProvider()
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
	provider := newDatadogProvider()
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

		objValue, ok := result.Value.(map[string]interface{})
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

		defaultObj := map[string]interface{}{"default": true}
		result := provider.ObjectEvaluation(ctx, "json-flag", defaultObj, flatCtx)

		if result.Reason != openfeature.DefaultReason {
			t.Errorf("expected DefaultReason, got %s", result.Reason)
		}
	})
}

func TestProviderWithoutConfiguration(t *testing.T) {
	provider := newDatadogProvider()
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
	provider := newDatadogProvider()

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
	provider := newDatadogProvider()
	config := createTestConfig()
	provider.updateConfiguration(config)

	ctx := context.Background()
	flatCtx := openfeature.FlattenedContext{
		"targetingKey": "user-123",
		"country":      "US",
	}

	// Run multiple concurrent evaluations
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			result := provider.BooleanEvaluation(ctx, "bool-flag", false, flatCtx)
			if result.Value != true {
				t.Errorf("expected true, got %v", result.Value)
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}
