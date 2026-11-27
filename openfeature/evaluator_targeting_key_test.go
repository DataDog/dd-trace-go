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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// targetingKeyRuleFlag returns a STRING flag that serves "match" when the
// single condition matches, and the default otherwise.
func targetingKeyRuleFlag(key, attribute, operator string, value any) map[string]any {
	return map[string]any{
		"key":           key,
		"enabled":       true,
		"variationType": "STRING",
		"variations":    map[string]any{"match": map[string]any{"key": "match", "value": "match"}},
		"allocations": []any{map[string]any{
			"key": key + "-allocation",
			"rules": []any{map[string]any{"conditions": []any{map[string]any{
				"attribute": attribute, "operator": operator, "value": value,
			}}}},
			"splits": []any{map[string]any{"variationKey": "match", "shards": []any{}}},
			"doLog":  true,
		}},
	}
}

func targetingKeyTestConfig(t *testing.T) *universalFlagsConfiguration {
	t.Helper()
	flags := map[string]any{
		"tk-one-of":      targetingKeyRuleFlag("tk-one-of", "targetingKey", "ONE_OF", []string{"foo", "baz"}),
		"tk-not-one-of":  targetingKeyRuleFlag("tk-not-one-of", "targetingKey", "NOT_ONE_OF", []string{"foo"}),
		"tk-matches":     targetingKeyRuleFlag("tk-matches", "targetingKey", "MATCHES", "^user-"),
		"tk-not-matches": targetingKeyRuleFlag("tk-not-matches", "targetingKey", "NOT_MATCHES", "^user-"),
		"tk-is-null":     targetingKeyRuleFlag("tk-is-null", "targetingKey", "IS_NULL", true),
		"tk-is-not-null": targetingKeyRuleFlag("tk-is-not-null", "targetingKey", "IS_NULL", false),
		"id-one-of":      targetingKeyRuleFlag("id-one-of", "id", "ONE_OF", []string{"foo"}),
		"tk-rule-split": map[string]any{
			"key":           "tk-rule-split",
			"enabled":       true,
			"variationType": "STRING",
			"variations": map[string]any{
				"control":   map[string]any{"key": "control", "value": "control"},
				"treatment": map[string]any{"key": "treatment", "value": "treatment"},
			},
			"allocations": []any{map[string]any{
				"key": "targeting-key-rule-split",
				"rules": []any{map[string]any{"conditions": []any{map[string]any{
					"attribute": "targetingKey", "operator": "ONE_OF",
					"value": []string{"foo", "bar", "baz", "qux", "quux", "corge"},
				}}}},
				"splits": []any{
					map[string]any{"variationKey": "control", "shards": []any{map[string]any{
						"salt": "targeting-key-rule-split", "totalShards": 10000,
						"ranges": []any{map[string]any{"start": 0, "end": 5000}},
					}}},
					map[string]any{"variationKey": "treatment", "shards": []any{map[string]any{
						"salt": "targeting-key-rule-split", "totalShards": 10000,
						"ranges": []any{map[string]any{"start": 5000, "end": 10000}},
					}}},
				},
				"doLog": true,
			}},
		},
	}
	data, err := json.Marshal(map[string]any{
		"createdAt":   "2025-01-01T00:00:00Z",
		"format":      "SERVER",
		"environment": map[string]any{"name": "test"},
		"flags":       flags,
	})
	require.NoError(t, err)
	var cfg universalFlagsConfiguration
	require.NoError(t, json.Unmarshal(data, &cfg))
	return &cfg
}

func TestEvaluateFlag_TargetingKeyAttribute(t *testing.T) {
	cfg := targetingKeyTestConfig(t)

	tests := []struct {
		name      string
		flag      string
		context   map[string]any
		wantValue string
	}{
		{"one of matches targeting key", "tk-one-of", map[string]any{"targetingKey": "foo"}, "match"},
		{"one of matches other listed key", "tk-one-of", map[string]any{"targetingKey": "baz"}, "match"},
		{"one of does not match unlisted key", "tk-one-of", map[string]any{"targetingKey": "bar"}, "default"},
		{"one of without targeting key", "tk-one-of", map[string]any{}, "default"},
		{"not one of matches unlisted key", "tk-not-one-of", map[string]any{"targetingKey": "bar"}, "match"},
		{"not one of does not match listed key", "tk-not-one-of", map[string]any{"targetingKey": "foo"}, "default"},
		{"not one of without targeting key", "tk-not-one-of", map[string]any{}, "default"},
		{"matches", "tk-matches", map[string]any{"targetingKey": "user-1"}, "match"},
		{"matches does not match", "tk-matches", map[string]any{"targetingKey": "admin-1"}, "default"},
		{"not matches", "tk-not-matches", map[string]any{"targetingKey": "admin-1"}, "match"},
		{"not matches does not match", "tk-not-matches", map[string]any{"targetingKey": "user-1"}, "default"},
		{"is null true without targeting key", "tk-is-null", map[string]any{}, "match"},
		{"is null true with targeting key", "tk-is-null", map[string]any{"targetingKey": "foo"}, "default"},
		{"is null false with targeting key", "tk-is-not-null", map[string]any{"targetingKey": "foo"}, "match"},
		{"is null false without targeting key", "tk-is-not-null", map[string]any{}, "default"},
		{"id falls back to targeting key", "id-one-of", map[string]any{"targetingKey": "foo"}, "match"},
		{"explicit id wins over targeting key", "id-one-of", map[string]any{"targetingKey": "foo", "id": "bar"}, "default"},
		// Bucketing still shards on the targeting key; expected variants match the shared fixtures.
		{"split buckets foo", "tk-rule-split", map[string]any{"targetingKey": "foo"}, "treatment"},
		{"split buckets bar", "tk-rule-split", map[string]any{"targetingKey": "bar"}, "control"},
		{"split buckets baz", "tk-rule-split", map[string]any{"targetingKey": "baz"}, "treatment"},
		{"split buckets qux", "tk-rule-split", map[string]any{"targetingKey": "qux"}, "control"},
		{"split buckets quux", "tk-rule-split", map[string]any{"targetingKey": "quux"}, "control"},
		{"split buckets corge", "tk-rule-split", map[string]any{"targetingKey": "corge"}, "treatment"},
		{"split without targeting key", "tk-rule-split", map[string]any{}, "default"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := evaluateConfiguredFlag(cfg, tt.flag, "default", tt.context, time.Now(), experimentationExclusion{})
			require.NoError(t, result.Error)
			assert.Equal(t, tt.wantValue, result.Value)
			if tt.wantValue == "default" {
				assert.Equal(t, of.DefaultReason, result.Reason)
			} else {
				assert.Equal(t, of.TargetingMatchReason, result.Reason)
			}
		})
	}
}

// TestProvider_TargetingKeyAttribute evaluates through the OpenFeature client,
// which flattens the evaluation context before it reaches the provider. The
// flattened context holds the targeting key under "targetingKey", so a context
// that sets both a targeting key and a "targetingKey" attribute reaches the
// provider with only the targeting key.
func TestProvider_TargetingKeyAttribute(t *testing.T) {
	provider := newDatadogProvider(ProviderConfig{})
	cleanupProvider(t, provider)
	provider.updateConfiguration(targetingKeyTestConfig(t))
	require.NoError(t, of.SetProviderAndWait(provider))
	client := of.NewClient("targeting-key-test")

	tests := []struct {
		name      string
		evalCtx   of.EvaluationContext
		wantValue string
	}{
		{"targeting key matches", of.NewEvaluationContext("foo", nil), "match"},
		{"targeting key does not match", of.NewEvaluationContext("bar", nil), "default"},
		{"no targeting key", of.NewTargetlessEvaluationContext(nil), "default"},
		{"explicit attribute without targeting key", of.NewTargetlessEvaluationContext(map[string]any{"targetingKey": "foo"}), "match"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details, err := client.StringValueDetails(context.Background(), "tk-one-of", "default", tt.evalCtx)
			require.NoError(t, err)
			assert.Equal(t, tt.wantValue, details.Value)
		})
	}
}
