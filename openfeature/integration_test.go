// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
)

// TestEndToEnd_BooleanFlag tests the complete flow from configuration to flag evaluation
// using the actual OpenFeature SDK client.
func TestEndToEnd_BooleanFlag(t *testing.T) {
	// Create provider and configuration
	provider := newDatadogProvider()
	config := createE2EBooleanConfig()
	provider.updateConfiguration(&config)

	// Register with OpenFeature SDK and wait for initialization
	err := of.SetProviderAndWait(provider)
	if err != nil {
		t.Fatalf("failed to set provider: %v", err)
	}

	client := of.NewClient("test-app")

	ctx := context.Background()

	t.Run("user in US gets enabled feature", func(t *testing.T) {
		evalCtx := of.NewEvaluationContext("user-123", map[string]interface{}{
			"country": "US",
		})

		value, err := client.BooleanValue(ctx, "feature-rollout", false, evalCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !value {
			t.Error("expected feature to be enabled for US user")
		}
	})

	t.Run("user in UK gets default value", func(t *testing.T) {
		evalCtx := of.NewEvaluationContext("user-456", map[string]interface{}{
			"country": "UK",
		})

		value, err := client.BooleanValue(ctx, "feature-rollout", false, evalCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if value {
			t.Error("expected feature to be disabled for UK user")
		}
	})

	t.Run("evaluation details include variant and reason", func(t *testing.T) {
		evalCtx := of.NewEvaluationContext("user-789", map[string]interface{}{
			"country": "US",
		})

		details, err := client.BooleanValueDetails(ctx, "feature-rollout", false, evalCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !details.Value {
			t.Error("expected feature to be enabled")
		}

		if details.Reason != of.TargetingMatchReason {
			t.Errorf("expected reason TARGETING_MATCH, got %v", details.Reason)
		}

		if details.Variant != "on" {
			t.Errorf("expected variant 'on', got %q", details.Variant)
		}
	})
}

// TestEndToEnd_StringFlag tests string flag evaluation with the OpenFeature SDK.
func TestEndToEnd_StringFlag(t *testing.T) {
	provider := newDatadogProvider()
	config := createE2EStringConfig()
	provider.updateConfiguration(config)

	err := of.SetProviderAndWait(provider)
	if err != nil {
		t.Fatalf("failed to set provider: %v", err)
	}
	client := of.NewClient("test-app")

	ctx := context.Background()

	t.Run("premium user gets v2", func(t *testing.T) {
		evalCtx := of.NewEvaluationContext("premium-user-1", map[string]interface{}{
			"tier": "premium",
		})

		value, err := client.StringValue(ctx, "api-version", "v1", evalCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if value != "v2" {
			t.Errorf("expected 'v2', got %q", value)
		}
	})

	t.Run("basic user gets default", func(t *testing.T) {
		evalCtx := of.NewEvaluationContext("basic-user-1", map[string]interface{}{
			"tier": "basic",
		})

		value, err := client.StringValue(ctx, "api-version", "v1", evalCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if value != "v1" {
			t.Errorf("expected 'v1', got %q", value)
		}
	})
}

// TestEndToEnd_IntegerFlag tests integer flag evaluation.
func TestEndToEnd_IntegerFlag(t *testing.T) {
	provider := newDatadogProvider()
	config := createE2EIntegerConfig()
	provider.updateConfiguration(config)

	err := of.SetProviderAndWait(provider)
	if err != nil {
		t.Fatalf("failed to set provider: %v", err)
	}
	client := of.NewClient("test-app")

	ctx := context.Background()

	t.Run("high traffic user gets higher limit", func(t *testing.T) {
		evalCtx := of.NewEvaluationContext("high-traffic-user", map[string]interface{}{
			"requests_per_day": 10000,
		})

		value, err := client.IntValue(ctx, "rate-limit", 100, evalCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if value != 1000 {
			t.Errorf("expected 1000, got %d", value)
		}
	})

	t.Run("low traffic user gets default", func(t *testing.T) {
		evalCtx := of.NewEvaluationContext("low-traffic-user", map[string]interface{}{
			"requests_per_day": 50,
		})

		value, err := client.IntValue(ctx, "rate-limit", 100, evalCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if value != 100 {
			t.Errorf("expected 100, got %d", value)
		}
	})
}

// TestEndToEnd_FloatFlag tests float flag evaluation.
func TestEndToEnd_FloatFlag(t *testing.T) {
	provider := newDatadogProvider()
	config := createE2EFloatConfig()
	provider.updateConfiguration(config)

	err := of.SetProviderAndWait(provider)
	if err != nil {
		t.Fatalf("failed to set provider: %v", err)
	}
	client := of.NewClient("test-app")

	ctx := context.Background()

	evalCtx := of.NewEvaluationContext("user-1", map[string]interface{}{
		"experiment_group": "test",
	})

	value, err := client.FloatValue(ctx, "discount-rate", 0.0, evalCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if value != 0.15 {
		t.Errorf("expected 0.15, got %f", value)
	}
}

// TestEndToEnd_ObjectFlag tests JSON/object flag evaluation.
func TestEndToEnd_ObjectFlag(t *testing.T) {
	provider := newDatadogProvider()
	config := createE2EObjectConfig()
	provider.updateConfiguration(config)

	err := of.SetProviderAndWait(provider)
	if err != nil {
		t.Fatalf("failed to set provider: %v", err)
	}
	client := of.NewClient("test-app")

	ctx := context.Background()

	t.Run("returns complex configuration object", func(t *testing.T) {
		evalCtx := of.NewEvaluationContext("user-1", map[string]interface{}{
			"feature_access": true,
		})

		value, err := client.ObjectValue(ctx, "feature-config", nil, evalCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		configMap, ok := value.(map[string]interface{})
		if !ok {
			t.Fatalf("expected map[string]interface{}, got %T", value)
		}

		if configMap["enabled"] != true {
			t.Errorf("expected enabled=true, got %v", configMap["enabled"])
		}

		// JSON unmarshal converts numbers to either int or float64
		timeout, ok := configMap["timeout"].(int)
		if !ok {
			timeoutFloat, ok := configMap["timeout"].(float64)
			if !ok || int(timeoutFloat) != 30 {
				t.Errorf("expected timeout=30, got %v (type %T)", configMap["timeout"], configMap["timeout"])
			}
		} else if timeout != 30 {
			t.Errorf("expected timeout=30, got %d", timeout)
		}
	})
}

// TestEndToEnd_DisabledFlag tests that disabled flags return defaults.
func TestEndToEnd_DisabledFlag(t *testing.T) {
	provider := newDatadogProvider()
	config := &universalFlagsConfiguration{
		Format: "SERVER",
		Environment: environment{
			Name: "test",
		},
		Flags: map[string]*flag{
			"disabled-feature": {
				Key:           "disabled-feature",
				Enabled:       false,
				VariationType: valueTypeBoolean,
				Variations: map[string]*variant{
					"on": {Key: "on", Value: true},
				},
				Allocations: []*allocation{},
			},
		},
	}
	provider.updateConfiguration(config)

	err := of.SetProviderAndWait(provider)
	if err != nil {
		t.Fatalf("failed to set provider: %v", err)
	}
	client := of.NewClient("test-app")

	ctx := context.Background()
	evalCtx := of.NewEvaluationContext("any-user", map[string]interface{}{})

	details, err := client.BooleanValueDetails(ctx, "disabled-feature", false, evalCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if details.Value != false {
		t.Error("expected disabled flag to return default false")
	}

	if details.Reason != of.DisabledReason {
		t.Errorf("expected DISABLED reason, got %v", details.Reason)
	}
}

// TestEndToEnd_MissingFlag tests error handling for non-existent flags.
func TestEndToEnd_MissingFlag(t *testing.T) {
	provider := newDatadogProvider()
	config := &universalFlagsConfiguration{
		Format: "SERVER",
		Environment: environment{
			Name: "test",
		},
		Flags: map[string]*flag{},
	}
	provider.updateConfiguration(config)

	err := of.SetProviderAndWait(provider)
	if err != nil {
		t.Fatalf("failed to set provider: %v", err)
	}
	client := of.NewClient("test-app")

	ctx := context.Background()
	evalCtx := of.NewEvaluationContext("any-user", map[string]interface{}{})

	details, _ := client.BooleanValueDetails(ctx, "nonexistent-flag", false, evalCtx)

	// The SDK may return an error, but we check the details object for the error information
	if details.Value != false {
		t.Error("expected missing flag to return default false")
	}

	if details.Reason != of.ErrorReason {
		t.Errorf("expected ERROR reason, got %v", details.Reason)
	}

	if details.ErrorCode != of.FlagNotFoundCode {
		t.Errorf("expected FLAG_NOT_FOUND error code, got %v", details.ErrorCode)
	}
}

// TestEndToEnd_ConfigurationUpdate tests that configuration updates are reflected in evaluations.
func TestEndToEnd_ConfigurationUpdate(t *testing.T) {
	provider := newDatadogProvider()
	provider.updateConfiguration(&universalFlagsConfiguration{})
	err := of.SetProviderAndWait(provider)
	if err != nil {
		t.Fatalf("failed to set provider: %v", err)
	}
	client := of.NewClient("test-app")

	ctx := context.Background()
	evalCtx := of.NewEvaluationContext("user-1", map[string]interface{}{
		"country": "US",
	})

	// Start with config where feature is OFF for US users
	config1 := createConfigWithFeatureOff()
	provider.updateConfiguration(config1)

	value1, _ := client.BooleanValue(ctx, "dynamic-feature", false, evalCtx)
	if value1 {
		t.Error("expected feature to be OFF initially")
	}

	// Update config where feature is ON for US users
	config2 := createConfigWithFeatureOn()
	provider.updateConfiguration(config2)

	value2, _ := client.BooleanValue(ctx, "dynamic-feature", false, evalCtx)
	if !value2 {
		t.Error("expected feature to be ON after configuration update")
	}
}

// TestEndToEnd_TrafficSharding tests that traffic distribution works correctly.
func TestEndToEnd_TrafficSharding(t *testing.T) {
	provider := newDatadogProvider()
	config := createE2EShardingConfig()
	provider.updateConfiguration(config)

	err := of.SetProviderAndWait(provider)
	if err != nil {
		t.Fatalf("failed to set provider: %v", err)
	}
	client := of.NewClient("test-app")

	ctx := context.Background()

	// Test multiple users to verify sharding distribution
	usersInVariantA := 0
	usersInVariantB := 0
	totalUsers := 100

	for i := 0; i < totalUsers; i++ {
		evalCtx := of.NewEvaluationContext(generateUserID(i), map[string]interface{}{
			"eligible": true,
		})

		details, err := client.StringValueDetails(ctx, "ab-test", "control", evalCtx)
		if err != nil {
			t.Fatalf("unexpected error for user %d: %v", i, err)
		}

		switch details.Value {
		case "variant-a":
			usersInVariantA++
		case "variant-b":
			usersInVariantB++
		}
	}

	// We expect roughly 50/50 split (allowing for variance)
	t.Logf("Distribution: A=%d, B=%d out of %d users", usersInVariantA, usersInVariantB, totalUsers)

	if usersInVariantA < 30 || usersInVariantA > 70 {
		t.Errorf("expected ~50%% in variant A, got %d%%", usersInVariantA)
	}

	if usersInVariantB < 30 || usersInVariantB > 70 {
		t.Errorf("expected ~50%% in variant B, got %d%%", usersInVariantB)
	}
}

// Helper functions to create test configurations

func createE2EBooleanConfig() universalFlagsConfiguration {
	return universalFlagsConfiguration{
		CreatedAt: time.Now(),
		Format:    "SERVER",
		Environment: environment{
			Name: "test",
		},
		Flags: map[string]*flag{
			"feature-rollout": {
				Key:           "feature-rollout",
				Enabled:       true,
				VariationType: valueTypeBoolean,
				Variations: map[string]*variant{
					"on":  {Key: "on", Value: true},
					"off": {Key: "off", Value: false},
				},
				Allocations: []*allocation{
					{
						Key: "us-rollout",
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
										Salt: "test-salt",
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
		},
	}
}

func createE2EStringConfig() *universalFlagsConfiguration {
	return &universalFlagsConfiguration{
		Format: "SERVER",
		Environment: environment{
			Name: "test",
		},
		Flags: map[string]*flag{
			"api-version": {
				Key:           "api-version",
				Enabled:       true,
				VariationType: valueTypeString,
				Variations: map[string]*variant{
					"v1": {Key: "v1", Value: "v1"},
					"v2": {Key: "v2", Value: "v2"},
				},
				Allocations: []*allocation{
					{
						Key: "premium-users",
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
										Salt: "api-version-salt",
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
		},
	}
}

func createE2EIntegerConfig() *universalFlagsConfiguration {
	return &universalFlagsConfiguration{
		Format: "SERVER",
		Environment: environment{
			Name: "test",
		},
		Flags: map[string]*flag{
			"rate-limit": {
				Key:           "rate-limit",
				Enabled:       true,
				VariationType: valueTypeInteger,
				Variations: map[string]*variant{
					"standard": {Key: "standard", Value: int64(100)},
					"high":     {Key: "high", Value: int64(1000)},
				},
				Allocations: []*allocation{
					{
						Key: "high-traffic-users",
						Rules: []*rule{
							{
								Conditions: []*condition{
									{
										Operator:  operatorGT,
										Attribute: "requests_per_day",
										Value:     1000.0,
									},
								},
							},
						},
						Splits: []*split{
							{
								Shards: []*shard{
									{
										Salt: "rate-limit-salt",
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
		},
	}
}

func createE2EFloatConfig() *universalFlagsConfiguration {
	return &universalFlagsConfiguration{
		Format: "SERVER",
		Environment: environment{
			Name: "test",
		},
		Flags: map[string]*flag{
			"discount-rate": {
				Key:           "discount-rate",
				Enabled:       true,
				VariationType: valueTypeNumeric,
				Variations: map[string]*variant{
					"standard": {Key: "standard", Value: 0.1},
					"special":  {Key: "special", Value: 0.15},
				},
				Allocations: []*allocation{
					{
						Key: "test-group",
						Rules: []*rule{
							{
								Conditions: []*condition{
									{
										Operator:  operatorMatches,
										Attribute: "experiment_group",
										Value:     "test",
									},
								},
							},
						},
						Splits: []*split{
							{
								Shards: []*shard{
									{
										Salt: "discount-salt",
										Ranges: []*shardRange{
											{Start: 0, End: 8192},
										},
										TotalShards: 8192,
									},
								},
								VariationKey: "special",
							},
						},
					},
				},
			},
		},
	}
}

func createE2EObjectConfig() *universalFlagsConfiguration {
	return &universalFlagsConfiguration{
		Format: "SERVER",
		Environment: environment{
			Name: "test",
		},
		Flags: map[string]*flag{
			"feature-config": {
				Key:           "feature-config",
				Enabled:       true,
				VariationType: valueTypeJSON,
				Variations: map[string]*variant{
					"default": {
						Key: "default",
						Value: map[string]interface{}{
							"enabled": false,
							"timeout": 10,
						},
					},
					"advanced": {
						Key: "advanced",
						Value: map[string]interface{}{
							"enabled": true,
							"timeout": 30,
							"retries": 3,
						},
					},
				},
				Allocations: []*allocation{
					{
						Key: "advanced-users",
						Rules: []*rule{
							{
								Conditions: []*condition{
									{
										Operator:  operatorIsNull,
										Attribute: "feature_access",
										Value:     false,
									},
								},
							},
						},
						Splits: []*split{
							{
								Shards: []*shard{
									{
										Salt: "config-salt",
										Ranges: []*shardRange{
											{Start: 0, End: 8192},
										},
										TotalShards: 8192,
									},
								},
								VariationKey: "advanced",
							},
						},
					},
				},
			},
		},
	}
}

func createConfigWithFeatureOff() *universalFlagsConfiguration {
	return &universalFlagsConfiguration{
		Format: "SERVER",
		Environment: environment{
			Name: "test",
		},
		Flags: map[string]*flag{
			"dynamic-feature": {
				Key:           "dynamic-feature",
				Enabled:       true,
				VariationType: valueTypeBoolean,
				Variations: map[string]*variant{
					"on":  {Key: "on", Value: true},
					"off": {Key: "off", Value: false},
				},
				Allocations: []*allocation{
					{
						Key: "other-countries",
						Rules: []*rule{
							{
								Conditions: []*condition{
									{
										Operator:  operatorNotOneOf,
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
										Salt: "dynamic-salt",
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
		},
	}
}

func createConfigWithFeatureOn() *universalFlagsConfiguration {
	config := createConfigWithFeatureOff()
	// Update the rule to target US users
	config.Flags["dynamic-feature"].Allocations[0].Rules[0].Conditions[0] = &condition{
		Operator:  operatorOneOf,
		Attribute: "country",
		Value:     []string{"US"},
	}
	return config
}

func createE2EShardingConfig() *universalFlagsConfiguration {
	return &universalFlagsConfiguration{
		Format: "SERVER",
		Environment: environment{
			Name: "test",
		},
		Flags: map[string]*flag{
			"ab-test": {
				Key:           "ab-test",
				Enabled:       true,
				VariationType: valueTypeString,
				Variations: map[string]*variant{
					"control":   {Key: "control", Value: "control"},
					"variant-a": {Key: "variant-a", Value: "variant-a"},
					"variant-b": {Key: "variant-b", Value: "variant-b"},
				},
				Allocations: []*allocation{
					{
						Key: "ab-test-allocation",
						Rules: []*rule{
							{
								Conditions: []*condition{
									{
										Operator:  operatorIsNull,
										Attribute: "eligible",
										Value:     false,
									},
								},
							},
						},
						Splits: []*split{
							{
								Shards: []*shard{
									{
										Salt: "ab-test-salt",
										Ranges: []*shardRange{
											{Start: 0, End: 4096}, // 50%
										},
										TotalShards: 8192,
									},
								},
								VariationKey: "variant-a",
							},
							{
								Shards: []*shard{
									{
										Salt: "ab-test-salt",
										Ranges: []*shardRange{
											{Start: 4096, End: 8192}, // 50%
										},
										TotalShards: 8192,
									},
								},
								VariationKey: "variant-b",
							},
						},
					},
				},
			},
		},
	}
}

func generateUserID(i int) string {
	return "user-" + string(rune('a'+i%26)) + string(rune('0'+i/26%10)) + string(rune('0'+i/260%10))
}

// TestEndToEnd_JSONSerialization verifies that configuration can be serialized and deserialized.
func TestEndToEnd_JSONSerialization(t *testing.T) {
	originalConfig := createE2EBooleanConfig()

	// Serialize to JSON
	data, err := json.Marshal(serverConfiguration{originalConfig})
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}

	// Deserialize from JSON
	var parsedConfig serverConfiguration
	if err := json.Unmarshal(data, &parsedConfig); err != nil {
		t.Fatalf("failed to unmarshal config: %v", err)
	}

	// Use the parsed config
	provider := newDatadogProvider()
	provider.updateConfiguration(&parsedConfig.FlagConfiguration)

	err = of.SetProviderAndWait(provider)
	if err != nil {
		t.Fatalf("failed to set provider: %v", err)
	}
	client := of.NewClient("test-app")

	ctx := context.Background()
	evalCtx := of.NewEvaluationContext("user-1", map[string]interface{}{
		"country": "US",
	})

	value, err := client.BooleanValue(ctx, "feature-rollout", false, evalCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !value {
		t.Error("expected feature to be enabled after JSON round-trip")
	}
}

// TestEndToEnd_EmptyRulesAllocation tests that an allocation with no rules matches all users.
// This covers the fix where empty rules should match everyone (no targeting restrictions).
func TestEndToEnd_EmptyRulesAllocation(t *testing.T) {
	provider := newDatadogProvider()
	config := &universalFlagsConfiguration{
		Format: "SERVER",
		Environment: environment{
			Name: "test",
		},
		Flags: map[string]*flag{
			"no-rules-flag": {
				Key:           "no-rules-flag",
				Enabled:       true,
				VariationType: valueTypeNumeric,
				Variations: map[string]*variant{
					"pi": {Key: "pi", Value: 3.1415926},
				},
				Allocations: []*allocation{
					{
						Key:   "rollout",
						Rules: []*rule{}, // Empty rules - should match everyone
						Splits: []*split{
							{
								Shards:       []*shard{}, // Empty shards - should match everyone
								VariationKey: "pi",
							},
						},
					},
				},
			},
		},
	}
	provider.updateConfiguration(config)

	err := of.SetProviderAndWait(provider)
	if err != nil {
		t.Fatalf("failed to set provider: %v", err)
	}
	client := of.NewClient("test-app")

	ctx := context.Background()

	t.Run("user with no attributes gets value from empty rules allocation", func(t *testing.T) {
		evalCtx := of.NewEvaluationContext("alice", map[string]interface{}{})

		value, err := client.FloatValue(ctx, "no-rules-flag", 0.0, evalCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if value != 3.1415926 {
			t.Errorf("expected 3.1415926, got %f", value)
		}
	})

	t.Run("user with attributes gets value from empty rules allocation", func(t *testing.T) {
		evalCtx := of.NewEvaluationContext("bob", map[string]interface{}{
			"country": "France",
			"age":     30,
		})

		value, err := client.FloatValue(ctx, "no-rules-flag", 0.0, evalCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if value != 3.1415926 {
			t.Errorf("expected 3.1415926, got %f", value)
		}
	})
}

// TestEndToEnd_ShardCalculationWithDash tests that the shard calculation uses
// salt + "-" + targetingKey (with dash separator) to match Eppo SDK implementation.
func TestEndToEnd_ShardCalculationWithDash(t *testing.T) {
	provider := newDatadogProvider()
	config := &universalFlagsConfiguration{
		Format: "SERVER",
		Environment: environment{
			Name: "test",
		},
		Flags: map[string]*flag{
			"50-50-split": {
				Key:           "50-50-split",
				Enabled:       true,
				VariationType: valueTypeInteger,
				Variations: map[string]*variant{
					"one": {Key: "one", Value: int64(1)},
					"two": {Key: "two", Value: int64(2)},
				},
				Allocations: []*allocation{
					{
						Key:   "split-allocation",
						Rules: []*rule{}, // No rules - matches everyone
						Splits: []*split{
							{
								// First 50% get value 1
								Shards: []*shard{
									{
										Salt: "split-numeric-flag-some-allocation",
										Ranges: []*shardRange{
											{Start: 0, End: 5000},
										},
										TotalShards: 10000,
									},
								},
								VariationKey: "one",
							},
							{
								// Second 50% get value 2
								Shards: []*shard{
									{
										Salt: "split-numeric-flag-some-allocation",
										Ranges: []*shardRange{
											{Start: 5000, End: 10000},
										},
										TotalShards: 10000,
									},
								},
								VariationKey: "two",
							},
						},
					},
				},
			},
		},
	}
	provider.updateConfiguration(config)

	err := of.SetProviderAndWait(provider)
	if err != nil {
		t.Fatalf("failed to set provider: %v", err)
	}
	client := of.NewClient("test-app")

	ctx := context.Background()

	// These test cases are based on the actual shard calculation with dash separator
	// The expected values match what the Eppo SDK returns with salt + "-" + targetingKey
	testCases := []struct {
		targetingKey  string
		expectedValue int64
		reason        string
	}{
		{
			targetingKey:  "eve",
			expectedValue: 1,
			reason:        "eve's shard index (732 with dash) should be in first half [0, 5000)",
		},
		{
			targetingKey:  "user-1",
			expectedValue: 1,
			reason:        "user-1's shard index (2895 with dash) should be in first half [0, 5000)",
		},
		{
			targetingKey:  "alice",
			expectedValue: 2,
			reason:        "alice's shard index (9136 with dash) should be in second half [5000, 10000)",
		},
		{
			targetingKey:  "bob",
			expectedValue: 2,
			reason:        "bob's shard index (8956 with dash) should be in second half [5000, 10000)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.targetingKey, func(t *testing.T) {
			evalCtx := of.NewEvaluationContext(tc.targetingKey, map[string]interface{}{})

			value, err := client.IntValue(ctx, "50-50-split", 0, evalCtx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if value != tc.expectedValue {
				t.Errorf("%s: expected %d, got %d", tc.reason, tc.expectedValue, value)
			}
		})
	}
}

// TestEndToEnd_IdAttributeFallback tests that when an attribute named "id" is not
// explicitly provided, the targeting key is used as the "id" value.
func TestEndToEnd_IdAttributeFallback(t *testing.T) {
	provider := newDatadogProvider()
	config := &universalFlagsConfiguration{
		Format: "SERVER",
		Environment: environment{
			Name: "test",
		},
		Flags: map[string]*flag{
			"id-based-flag": {
				Key:           "id-based-flag",
				Enabled:       true,
				VariationType: valueTypeString,
				Variations: map[string]*variant{
					"purple": {Key: "purple", Value: "purple"},
					"blue":   {Key: "blue", Value: "blue"},
				},
				Allocations: []*allocation{
					{
						Key: "id-rule",
						Rules: []*rule{
							{
								Conditions: []*condition{
									{
										Operator:  operatorMatches,
										Attribute: "id",
										Value:     "zach",
									},
								},
							},
						},
						Splits: []*split{
							{
								Shards:       []*shard{}, // Empty shards - matches everyone
								VariationKey: "purple",
							},
						},
					},
					{
						Key:   "fallback",
						Rules: []*rule{}, // No rules - matches everyone
						Splits: []*split{
							{
								Shards:       []*shard{},
								VariationKey: "blue",
							},
						},
					},
				},
			},
		},
	}
	provider.updateConfiguration(config)

	err := of.SetProviderAndWait(provider)
	if err != nil {
		t.Fatalf("failed to set provider: %v", err)
	}
	client := of.NewClient("test-app")

	ctx := context.Background()

	t.Run("targeting key used as id when no explicit id attribute", func(t *testing.T) {
		// User "zach" with no explicit "id" attribute should match the id rule
		evalCtx := of.NewEvaluationContext("zach", map[string]interface{}{
			"email":   "test@test.com",
			"country": "Mexico",
			"age":     25,
		})

		value, err := client.StringValue(ctx, "id-based-flag", "default", evalCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if value != "purple" {
			t.Errorf("expected 'purple' (targeting key used as id), got %q", value)
		}
	})

	t.Run("explicit id attribute overrides targeting key", func(t *testing.T) {
		// User "zach" WITH explicit "id" attribute that doesn't match
		evalCtx := of.NewEvaluationContext("zach", map[string]interface{}{
			"id":      "override-id",
			"email":   "test@test.com",
			"country": "Mexico",
			"age":     25,
		})

		value, err := client.StringValue(ctx, "id-based-flag", "default", evalCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if value != "blue" {
			t.Errorf("expected 'blue' (explicit id overrides targeting key), got %q", value)
		}
	})

	t.Run("targeting key not matching id rule gets fallback", func(t *testing.T) {
		// User "alice" should not match the id rule (id != "zach")
		evalCtx := of.NewEvaluationContext("alice", map[string]interface{}{
			"email": "alice@example.com",
		})

		value, err := client.StringValue(ctx, "id-based-flag", "default", evalCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if value != "blue" {
			t.Errorf("expected 'blue' (fallback allocation), got %q", value)
		}
	})
}

// TestEndToEnd_AllThreeFixes tests a complex scenario that exercises all three fixes together.
func TestEndToEnd_AllThreeFixes(t *testing.T) {
	provider := newDatadogProvider()
	config := &universalFlagsConfiguration{
		Format: "SERVER",
		Environment: environment{
			Name: "test",
		},
		Flags: map[string]*flag{
			"complex-flag": {
				Key:           "complex-flag",
				Enabled:       true,
				VariationType: valueTypeString,
				Variations: map[string]*variant{
					"vip":     {Key: "vip", Value: "vip"},
					"premium": {Key: "premium", Value: "premium"},
					"basic":   {Key: "basic", Value: "basic"},
				},
				Allocations: []*allocation{
					{
						Key: "vip-users",
						// Uses id matching (Fix #3: id fallback)
						Rules: []*rule{
							{
								Conditions: []*condition{
									{
										Operator:  operatorMatches,
										Attribute: "id",
										Value:     "vip-.*",
									},
								},
							},
						},
						Splits: []*split{
							{
								Shards:       []*shard{},
								VariationKey: "vip",
							},
						},
					},
					{
						Key: "premium-rollout",
						// Uses empty rules (Fix #1: empty rules match everyone)
						Rules: []*rule{},
						// Uses sharding with dash separator (Fix #2: salt + "-" + key)
						Splits: []*split{
							{
								Shards: []*shard{
									{
										Salt: "premium-salt",
										Ranges: []*shardRange{
											{Start: 0, End: 5000}, // 50%
										},
										TotalShards: 10000,
									},
								},
								VariationKey: "premium",
							},
							{
								Shards: []*shard{
									{
										Salt: "premium-salt",
										Ranges: []*shardRange{
											{Start: 5000, End: 10000}, // 50%
										},
										TotalShards: 10000,
									},
								},
								VariationKey: "basic",
							},
						},
					},
				},
			},
		},
	}
	provider.updateConfiguration(config)

	err := of.SetProviderAndWait(provider)
	if err != nil {
		t.Fatalf("failed to set provider: %v", err)
	}
	client := of.NewClient("test-app")

	ctx := context.Background()

	t.Run("vip user matches id rule via targeting key fallback", func(t *testing.T) {
		// Targeting key "vip-user-1" matches the regex "vip-.*"
		// Uses Fix #3: targeting key used as "id" when no explicit id attribute
		evalCtx := of.NewEvaluationContext("vip-user-1", map[string]interface{}{})

		value, err := client.StringValue(ctx, "complex-flag", "default", evalCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if value != "vip" {
			t.Errorf("expected 'vip', got %q", value)
		}
	})

	t.Run("regular user falls into sharded allocation with empty rules", func(t *testing.T) {
		// User "regular-user-1" doesn't match vip rule, falls to second allocation
		// Uses Fix #1: empty rules match everyone
		// Uses Fix #2: shard calculation with dash separator
		evalCtx := of.NewEvaluationContext("regular-user-1", map[string]interface{}{})

		value, err := client.StringValue(ctx, "complex-flag", "default", evalCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// The actual value depends on the shard calculation
		// Just verify it's one of the expected values from the second allocation
		if value != "premium" && value != "basic" {
			t.Errorf("expected 'premium' or 'basic', got %q", value)
		}
	})
}
