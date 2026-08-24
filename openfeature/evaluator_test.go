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
	"time"

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

func TestEvaluateSemverCondition(t *testing.T) {
	tests := []struct {
		name      string
		operator  conditionOperator
		attribute any
		comparand any
		want      bool
	}{
		{name: "equal", operator: operatorSemverEQ, attribute: "1.2.3", comparand: "1.2.3", want: true},
		{name: "equal mismatch", operator: operatorSemverEQ, attribute: "1.2.4", comparand: "1.2.3"},
		{name: "not equal", operator: operatorSemverNEQ, attribute: "1.2.4", comparand: "1.2.3", want: true},
		{name: "not equal mismatch", operator: operatorSemverNEQ, attribute: "1.2.3", comparand: "1.2.3"},
		{name: "less than", operator: operatorSemverLT, attribute: "1.9.9", comparand: "2.0.0", want: true},
		{name: "less than mismatch", operator: operatorSemverLT, attribute: "2.0.0", comparand: "2.0.0"},
		{name: "less than or equal", operator: operatorSemverLTE, attribute: "2.0.0", comparand: "2.0.0", want: true},
		{name: "less than or equal mismatch", operator: operatorSemverLTE, attribute: "2.0.1", comparand: "2.0.0"},
		{name: "greater than", operator: operatorSemverGT, attribute: "1.0.1", comparand: "1.0.0", want: true},
		{name: "greater than mismatch", operator: operatorSemverGT, attribute: "1.0.0", comparand: "1.0.0"},
		{name: "greater than or equal", operator: operatorSemverGTE, attribute: "1.0.0", comparand: "1.0.0", want: true},
		{name: "greater than or equal mismatch", operator: operatorSemverGTE, attribute: "0.9.9", comparand: "1.0.0"},
		{name: "prerelease before release", operator: operatorSemverLT, attribute: "1.0.0-beta.1", comparand: "1.0.0", want: true},
		{name: "numeric prerelease ordering", operator: operatorSemverLT, attribute: "1.0.0-beta.2", comparand: "1.0.0-beta.11", want: true},
		{name: "equal ignores build metadata", operator: operatorSemverEQ, attribute: "4.0.0+build.42", comparand: "4.0.0", want: true},
		{name: "equal ignores dotted build metadata", operator: operatorSemverEQ, attribute: "4.0.0+exp.sha.5114f85", comparand: "4.0.0", want: true},
		{name: "not equal ignores build metadata", operator: operatorSemverNEQ, attribute: "4.0.0+build.42", comparand: "4.0.0"},
		{name: "less than ignores build metadata", operator: operatorSemverLT, attribute: "4.0.0+build.42", comparand: "4.0.0"},
		{name: "less than or equal ignores build metadata", operator: operatorSemverLTE, attribute: "4.0.0+build.42", comparand: "4.0.0", want: true},
		{name: "greater than ignores build metadata", operator: operatorSemverGT, attribute: "4.0.0+build.42", comparand: "4.0.0"},
		{name: "greater than or equal ignores build metadata", operator: operatorSemverGTE, attribute: "4.0.0+build.42", comparand: "4.0.0", want: true},
		{name: "different build metadata has equal precedence", operator: operatorSemverEQ, attribute: "1.0.0+linux", comparand: "1.0.0+darwin", want: true},
		{name: "invalid attribute", operator: operatorSemverNEQ, attribute: "not-a-version", comparand: "1.0.0"},
		{name: "short attribute", operator: operatorSemverGTE, attribute: "1.2", comparand: "1.0.0"},
		{name: "prefixed attribute", operator: operatorSemverGTE, attribute: "v1.2.3", comparand: "1.0.0"},
		{name: "overflowing attribute", operator: operatorSemverGTE, attribute: "18446744073709551616.0.0", comparand: "1.0.0"},
		{name: "non-string attribute", operator: operatorSemverEQ, attribute: 1.2, comparand: "1.2.0"},
		{name: "invalid comparand", operator: operatorSemverNEQ, attribute: "1.2.3", comparand: "not-a-version"},
		{name: "non-string comparand", operator: operatorSemverEQ, attribute: "1.2.3", comparand: 1.2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comparand, _ := tt.comparand.(string)
			var parsedComparand *parsedSemver
			if parsed, ok := parseSemver(comparand); ok {
				parsedComparand = &parsed
			}
			condition := &condition{
				Operator:        tt.operator,
				Attribute:       "version",
				Value:           tt.comparand,
				semverComparand: parsedComparand,
			}
			context := map[string]any{"version": tt.attribute}
			if got := evaluateCondition(condition, context); got != tt.want {
				t.Errorf("evaluateCondition() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("missing attribute", func(t *testing.T) {
		comparand, ok := parseSemver("1.2.3")
		if !ok {
			t.Fatal("parseSemver failed")
		}
		condition := &condition{
			Operator:        operatorSemverEQ,
			Attribute:       "version",
			Value:           "1.2.3",
			semverComparand: &comparand,
		}
		if evaluateCondition(condition, map[string]any{}) {
			t.Error("expected a missing attribute not to match")
		}
	})

	t.Run("unsupported operator", func(t *testing.T) {
		comparand, ok := parseSemver("1.2.3")
		if !ok {
			t.Fatal("parseSemver failed")
		}
		if evaluateSemverCondition("1.2.3", &comparand, conditionOperator("UNKNOWN")) {
			t.Error("expected an unsupported operator not to match")
		}
	})
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

			result := evaluateFlag(flag, nil, map[string]any{"targetingKey": "user-123"}, time.Now())

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
		t.Fatalf("read canonical FFE fixtures: %v (initialize them with `git submodule update --init --recursive`)", err)
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
		t.Fatal("no canonical FFE evaluation-case fixtures found (initialize them with `git submodule update --init --recursive`)")
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

					result := evaluateConfiguredFlag(&cfg, tc.Flag, tc.DefaultValue, ctx, time.Now())

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
