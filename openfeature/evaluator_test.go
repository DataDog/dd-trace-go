// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
)

func TestEvaluateCondition_IsNull(t *testing.T) {
	tests := []struct {
		name      string
		condition *condition
		context   map[string]interface{}
		expected  bool
	}{
		{
			name: "attribute missing, expect null",
			condition: &condition{
				Operator:  operatorIsNull,
				Attribute: "missing_attr",
				Value:     true,
			},
			context:  map[string]interface{}{},
			expected: true,
		},
		{
			name: "attribute missing, expect not null",
			condition: &condition{
				Operator:  operatorIsNull,
				Attribute: "missing_attr",
				Value:     false,
			},
			context:  map[string]interface{}{},
			expected: false,
		},
		{
			name: "attribute present, expect null",
			condition: &condition{
				Operator:  operatorIsNull,
				Attribute: "present_attr",
				Value:     true,
			},
			context:  map[string]interface{}{"present_attr": "value"},
			expected: false,
		},
		{
			name: "attribute present, expect not null",
			condition: &condition{
				Operator:  operatorIsNull,
				Attribute: "present_attr",
				Value:     false,
			},
			context:  map[string]interface{}{"present_attr": "value"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := evaluateCondition(tt.condition, tt.context)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestEvaluateCondition_NumericComparison(t *testing.T) {
	tests := []struct {
		name      string
		condition *condition
		context   map[string]interface{}
		expected  bool
	}{
		{
			name: "GT - greater than",
			condition: &condition{
				Operator:  operatorGT,
				Attribute: "age",
				Value:     18.0,
			},
			context:  map[string]interface{}{"age": 25},
			expected: true,
		},
		{
			name: "GT - not greater than",
			condition: &condition{
				Operator:  operatorGT,
				Attribute: "age",
				Value:     30.0,
			},
			context:  map[string]interface{}{"age": 25},
			expected: false,
		},
		{
			name: "GTE - greater than or equal",
			condition: &condition{
				Operator:  operatorGTE,
				Attribute: "age",
				Value:     25.0,
			},
			context:  map[string]interface{}{"age": 25},
			expected: true,
		},
		{
			name: "LT - less than",
			condition: &condition{
				Operator:  operatorLT,
				Attribute: "age",
				Value:     30.0,
			},
			context:  map[string]interface{}{"age": 25},
			expected: true,
		},
		{
			name: "LTE - less than or equal",
			condition: &condition{
				Operator:  operatorLTE,
				Attribute: "age",
				Value:     25.0,
			},
			context:  map[string]interface{}{"age": 25},
			expected: true,
		},
		{
			name: "numeric with float attribute",
			condition: &condition{
				Operator:  operatorGT,
				Attribute: "score",
				Value:     85.5,
			},
			context:  map[string]interface{}{"score": 90.7},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := evaluateCondition(tt.condition, tt.context)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestEvaluateCondition_RegexMatching(t *testing.T) {
	tests := []struct {
		name      string
		condition *condition
		context   map[string]interface{}
		expected  bool
	}{
		{
			name: "MATCHES - matches pattern",
			condition: &condition{
				Operator:  operatorMatches,
				Attribute: "email",
				Value:     ".*@example\\.com$",
			},
			context:  map[string]interface{}{"email": "user@example.com"},
			expected: true,
		},
		{
			name: "MATCHES - does not match pattern",
			condition: &condition{
				Operator:  operatorMatches,
				Attribute: "email",
				Value:     ".*@example\\.com$",
			},
			context:  map[string]interface{}{"email": "user@other.com"},
			expected: false,
		},
		{
			name: "NOT_MATCHES - does not match pattern",
			condition: &condition{
				Operator:  operatorNotMatches,
				Attribute: "email",
				Value:     ".*@spam\\.com$",
			},
			context:  map[string]interface{}{"email": "user@example.com"},
			expected: true,
		},
		{
			name: "NOT_MATCHES - matches pattern",
			condition: &condition{
				Operator:  operatorNotMatches,
				Attribute: "email",
				Value:     ".*@example\\.com$",
			},
			context:  map[string]interface{}{"email": "user@example.com"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := evaluateCondition(tt.condition, tt.context)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestEvaluateCondition_SetMembership(t *testing.T) {
	tests := []struct {
		name      string
		condition *condition
		context   map[string]interface{}
		expected  bool
	}{
		{
			name: "ONE_OF - in list",
			condition: &condition{
				Operator:  operatorOneOf,
				Attribute: "country",
				Value:     []string{"US", "CA", "MX"},
			},
			context:  map[string]interface{}{"country": "US"},
			expected: true,
		},
		{
			name: "ONE_OF - not in list",
			condition: &condition{
				Operator:  operatorOneOf,
				Attribute: "country",
				Value:     []string{"US", "CA", "MX"},
			},
			context:  map[string]interface{}{"country": "UK"},
			expected: false,
		},
		{
			name: "NOT_ONE_OF - not in list",
			condition: &condition{
				Operator:  operatorNotOneOf,
				Attribute: "country",
				Value:     []string{"CN", "RU"},
			},
			context:  map[string]interface{}{"country": "US"},
			expected: true,
		},
		{
			name: "NOT_ONE_OF - in list",
			condition: &condition{
				Operator:  operatorNotOneOf,
				Attribute: "country",
				Value:     []string{"US", "CA"},
			},
			context:  map[string]interface{}{"country": "US"},
			expected: false,
		},
		{
			name: "ONE_OF - with interface{} slice",
			condition: &condition{
				Operator:  operatorOneOf,
				Attribute: "tier",
				Value:     []interface{}{"gold", "platinum"},
			},
			context:  map[string]interface{}{"tier": "gold"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := evaluateCondition(tt.condition, tt.context)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestEvaluateRule(t *testing.T) {
	tests := []struct {
		name     string
		rule     *rule
		context  map[string]interface{}
		expected bool
	}{
		{
			name: "all conditions match",
			rule: &rule{
				Conditions: []*condition{
					{
						Operator:  operatorGTE,
						Attribute: "age",
						Value:     18.0,
					},
					{
						Operator:  operatorOneOf,
						Attribute: "country",
						Value:     []string{"US", "CA"},
					},
				},
			},
			context: map[string]interface{}{
				"age":     25,
				"country": "US",
			},
			expected: true,
		},
		{
			name: "one condition fails",
			rule: &rule{
				Conditions: []*condition{
					{
						Operator:  operatorGTE,
						Attribute: "age",
						Value:     18.0,
					},
					{
						Operator:  operatorOneOf,
						Attribute: "country",
						Value:     []string{"US", "CA"},
					},
				},
			},
			context: map[string]interface{}{
				"age":     25,
				"country": "UK",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := evaluateRule(tt.rule, tt.context)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestEvaluateShard(t *testing.T) {
	t.Run("targeting key hashes to correct shard", func(t *testing.T) {
		targetingKey := "user-123"
		salt := "test-salt"
		totalShards := 8192

		// First compute where this key actually hashes
		actualShard := computeShardIndex(salt, targetingKey, totalShards)

		// Test that a range containing this shard matches
		shard := &shard{
			Salt: salt,
			Ranges: []*shardRange{
				{Start: actualShard, End: actualShard + 1},
			},
			TotalShards: totalShards,
		}
		context := map[string]interface{}{
			"targetingKey": targetingKey,
		}

		result := evaluateShard(shard, context)
		if !result {
			t.Errorf("expected shard to match when range includes computed shard %d", actualShard)
		}
	})

	t.Run("targeting key not in range", func(t *testing.T) {
		targetingKey := "user-123"
		salt := "test-salt"
		totalShards := 8192

		// Compute where this key hashes
		actualShard := computeShardIndex(salt, targetingKey, totalShards)

		// Create a range that definitely doesn't include this shard
		excludedStart := (actualShard + 100) % totalShards
		excludedEnd := (actualShard + 110) % totalShards
		if excludedEnd < excludedStart {
			excludedEnd = totalShards
		}

		shard := &shard{
			Salt: salt,
			Ranges: []*shardRange{
				{Start: excludedStart, End: excludedEnd},
			},
			TotalShards: totalShards,
		}
		context := map[string]interface{}{
			"targetingKey": targetingKey,
		}

		result := evaluateShard(shard, context)
		if result {
			t.Errorf("expected shard not to match when range excludes computed shard")
		}
	})

	t.Run("no targeting key", func(t *testing.T) {
		shard := &shard{
			Salt: "test-salt",
			Ranges: []*shardRange{
				{Start: 0, End: 8192},
			},
			TotalShards: 8192,
		}
		context := map[string]interface{}{}

		result := evaluateShard(shard, context)
		if result {
			t.Errorf("expected shard not to match when no targeting key present")
		}
	})

	t.Run("full range always matches", func(t *testing.T) {
		shard := &shard{
			Salt: "test-salt",
			Ranges: []*shardRange{
				{Start: 0, End: 8192}, // 100% of traffic
			},
			TotalShards: 8192,
		}
		context := map[string]interface{}{
			"targetingKey": "any-user",
		}

		result := evaluateShard(shard, context)
		if !result {
			t.Errorf("expected shard to match when range covers all shards")
		}
	})
}

func TestEvaluateAllocation(t *testing.T) {
	now := time.Now()
	past := now.Add(-1 * time.Hour)
	future := now.Add(1 * time.Hour)

	tests := []struct {
		name              string
		allocation        *allocation
		context           map[string]interface{}
		currentTime       time.Time
		expectMatch       bool
		expectedVariation string
	}{
		{
			name: "allocation matches with time window",
			allocation: &allocation{
				Key:     "allocation1",
				StartAt: &past,
				EndAt:   &future,
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
									{Start: 0, End: 8192}, // All traffic
								},
								TotalShards: 8192,
							},
						},
						VariationKey: "variant-a",
					},
				},
			},
			context: map[string]interface{}{
				"country":      "US",
				"targetingKey": "user-123",
			},
			currentTime:       now,
			expectMatch:       true,
			expectedVariation: "variant-a",
		},
		{
			name: "allocation outside time window (before start)",
			allocation: &allocation{
				Key:     "allocation1",
				StartAt: &future,
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
						VariationKey: "variant-a",
					},
				},
			},
			context: map[string]interface{}{
				"country":      "US",
				"targetingKey": "user-123",
			},
			currentTime: now,
			expectMatch: false,
		},
		{
			name: "allocation outside time window (after end)",
			allocation: &allocation{
				Key:   "allocation1",
				EndAt: &past,
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
						VariationKey: "variant-a",
					},
				},
			},
			context: map[string]interface{}{
				"country":      "US",
				"targetingKey": "user-123",
			},
			currentTime: now,
			expectMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			split, matched := evaluateAllocation(tt.allocation, tt.context, tt.currentTime)
			if matched != tt.expectMatch {
				t.Errorf("expected match=%v, got match=%v", tt.expectMatch, matched)
			}
			if matched && split != nil && split.VariationKey != tt.expectedVariation {
				t.Errorf("expected variation=%s, got variation=%s", tt.expectedVariation, split.VariationKey)
			}
		})
	}
}

func TestEvaluateFlag(t *testing.T) {
	tests := []struct {
		name           string
		flag           *flag
		defaultValue   interface{}
		context        map[string]interface{}
		expectedValue  interface{}
		expectedReason of.Reason
	}{
		{
			name: "disabled flag returns default",
			flag: &flag{
				Key:           "test-flag",
				Enabled:       false,
				VariationType: valueTypeBoolean,
			},
			defaultValue:   false,
			context:        map[string]interface{}{},
			expectedValue:  false,
			expectedReason: of.DisabledReason,
		},
		{
			name: "enabled flag with matching allocation",
			flag: &flag{
				Key:           "test-flag",
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
			defaultValue: false,
			context: map[string]interface{}{
				"country":      "US",
				"targetingKey": "user-123",
			},
			expectedValue:  true,
			expectedReason: of.TargetingMatchReason,
		},
		{
			name: "enabled flag with no matching allocation",
			flag: &flag{
				Key:           "test-flag",
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
										Value:     []string{"CA"},
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
			defaultValue: false,
			context: map[string]interface{}{
				"country":      "US",
				"targetingKey": "user-123",
			},
			expectedValue:  false,
			expectedReason: of.DefaultReason,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := evaluateFlag(tt.flag, tt.defaultValue, tt.context)
			if result.Value != tt.expectedValue {
				t.Errorf("expected value=%v, got value=%v", tt.expectedValue, result.Value)
			}
			if result.Reason != tt.expectedReason {
				t.Errorf("expected reason=%s, got reason=%s", tt.expectedReason, result.Reason)
			}
		})
	}
}

func TestComputeShardIndex(t *testing.T) {
	// Test consistency: same input should always produce same output
	key1 := computeShardIndex("salt1", "user-123", 8192)
	key2 := computeShardIndex("salt1", "user-123", 8192)
	if key1 != key2 {
		t.Errorf("shard index should be consistent: %d != %d", key1, key2)
	}

	// Test different inputs produce different outputs
	key3 := computeShardIndex("salt2", "user-123", 8192)
	if key1 == key3 {
		t.Logf("warning: different salts produced same shard (possible but unlikely)")
	}

	// Test that output is within bounds
	if key1 < 0 || key1 >= 8192 {
		t.Errorf("shard index out of bounds: %d", key1)
	}
}

func TestValidateVariantType(t *testing.T) {
	tests := []struct {
		name         string
		value        interface{}
		expectedType valueType
		expectError  bool
	}{
		{"boolean valid", true, valueTypeBoolean, false},
		{"boolean invalid", "true", valueTypeBoolean, true},
		{"string valid", "hello", valueTypeString, false},
		{"string invalid", 123, valueTypeString, true},
		{"integer valid int", 42, valueTypeInteger, false},
		{"integer valid int64", int64(42), valueTypeInteger, false},
		{"integer valid float64 whole", float64(42), valueTypeInteger, false},
		{"integer invalid float64 decimal", 42.5, valueTypeInteger, true},
		{"numeric valid int", 42, valueTypeNumeric, false},
		{"numeric valid float", 42.5, valueTypeNumeric, false},
		{"numeric invalid", "42", valueTypeNumeric, true},
		{"json accepts anything", map[string]interface{}{"key": "value"}, valueTypeJSON, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVariantType(tt.value, tt.expectedType)
			if (err != nil) != tt.expectError {
				t.Errorf("expected error=%v, got error=%v", tt.expectError, err)
			}
		})
	}
}
