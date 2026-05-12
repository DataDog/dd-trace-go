// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"
)

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
		context := map[string]any{
			"targetingKey": targetingKey,
		}

		result, err := evaluateShard(shard, context)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
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
		context := map[string]any{
			"targetingKey": targetingKey,
		}

		result, err := evaluateShard(shard, context)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
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
		context := map[string]any{}

		result, err := evaluateShard(shard, context)
		if result {
			t.Errorf("expected shard not to match when no targeting key present")
		}
		if !errors.Is(err, errTargetingKeyMissing) {
			t.Errorf("expected errTargetingKeyMissing, got %v", err)
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
		context := map[string]any{
			"targetingKey": "any-user",
		}

		result, err := evaluateShard(shard, context)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result {
			t.Errorf("expected shard to match when range covers all shards")
		}
	})
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
		value        any
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
		{"json accepts anything", map[string]any{"key": "value"}, valueTypeJSON, false},
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

func TestEvaluateFlag_VariantTypeMismatchReturnsParseError(t *testing.T) {
	// When the configuration declares a flag type (e.g., INTEGER) but the variant
	// value doesn't match (e.g., a string), we should return errParseError so that
	// toResolutionError maps it to PARSE_ERROR.
	tests := []struct {
		name          string
		variationType valueType
		variantValue  any
	}{
		{
			name:          "INTEGER flag with string value",
			variationType: valueTypeInteger,
			variantValue:  "not-an-integer",
		},
		{
			name:          "BOOLEAN flag with string value",
			variationType: valueTypeBoolean,
			variantValue:  "true",
		},
		{
			name:          "NUMERIC flag with string value",
			variationType: valueTypeNumeric,
			variantValue:  "42.5",
		},
		{
			name:          "STRING flag with integer value",
			variationType: valueTypeString,
			variantValue:  123,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flag := &flag{
				Key:           "test-flag",
				Enabled:       true,
				VariationType: tt.variationType,
				Variations: map[string]*variant{
					"v1": {Key: "v1", Value: tt.variantValue},
				},
				Allocations: []*allocation{
					{
						Key: "allocation1",
						Splits: []*split{
							{
								VariationKey: "v1",
							},
						},
					},
				},
			}

			result := evaluateFlag(flag, nil, map[string]any{"targetingKey": "user-123"})

			if result.Reason != of.ErrorReason {
				t.Errorf("expected ErrorReason, got %s", result.Reason)
			}
			if result.Error == nil {
				t.Fatal("expected error, got nil")
			}
			// Verify the error wraps errParseError so toResolutionError maps to PARSE_ERROR
			if !errors.Is(result.Error, errParseError) {
				t.Errorf("expected error to wrap errParseError, got: %v", result.Error)
			}
		})
	}
}

func TestEvaluateFlag_JSONFixtures(t *testing.T) {
	fixtureDir := "ffe-system-test-data"

	configData, err := os.ReadFile(filepath.Join(fixtureDir, "ufc-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg universalFlagsConfiguration
	if err := json.Unmarshal(configData, &cfg); err != nil {
		t.Fatal(err)
	}

	files, err := filepath.Glob(filepath.Join(fixtureDir, "evaluation-cases", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no evaluation-case fixture files found")
	}

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var cases []struct {
				Flag         string         `json:"flag"`
				DefaultValue any            `json:"defaultValue"`
				TargetingKey *string        `json:"targetingKey"`
				Attributes   map[string]any `json:"attributes"`
				Result       struct {
					Value  any    `json:"value"`
					Reason string `json:"reason"`
				} `json:"result"`
			}
			if err := json.Unmarshal(data, &cases); err != nil {
				t.Fatalf("parse error: %v", err)
			}
			for i, tc := range cases {
				tkLabel := "<nil>"
				if tc.TargetingKey != nil {
					tkLabel = *tc.TargetingKey
				}
				t.Run(fmt.Sprintf("case%d/%s", i, tkLabel), func(t *testing.T) {
					ctx := make(map[string]any, len(tc.Attributes)+1)
					maps.Copy(ctx, tc.Attributes)
					if tc.TargetingKey != nil {
						ctx["targetingKey"] = *tc.TargetingKey
					}

					result := evaluateFlag(cfg.Flags[tc.Flag], tc.DefaultValue, ctx)

					if fmt.Sprintf("%v", result.Value) != fmt.Sprintf("%v", tc.Result.Value) {
						t.Errorf("value: got %v, want %v", result.Value, tc.Result.Value)
					}
					if tc.Result.Reason != "" && result.Reason != of.Reason(tc.Result.Reason) {
						t.Errorf("reason: got %q, want %q", result.Reason, tc.Result.Reason)
					}
				})
			}
		})
	}
}
