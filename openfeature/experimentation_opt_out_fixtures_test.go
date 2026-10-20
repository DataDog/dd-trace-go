// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type immediateOptOutFixtureEvaluation struct {
	Flag            string         `json:"flag"`
	VariationType   string         `json:"variationType"`
	DefaultValue    any            `json:"defaultValue"`
	TargetingKey    string         `json:"targetingKey"`
	Attributes      map[string]any `json:"attributes"`
	ExposureCount   int            `json:"exposureCount"`
	EvaluationCount int64          `json:"evaluationCount"`
	Result          struct {
		Value         any          `json:"value"`
		Reason        of.Reason    `json:"reason"`
		ErrorCode     of.ErrorCode `json:"errorCode"`
		AllocationKey *string      `json:"allocationKey"`
	} `json:"result"`
}

func TestExperimentationOptOutSharedFixtures(t *testing.T) {
	dir := filepath.Join("ffe-system-test-data", "experimentation-opt-out", "immediate-v1")
	configData, err := os.ReadFile(filepath.Join(dir, "ufc-config.json"))
	require.NoError(t, err, "initialize the pinned ffe-system-test-data submodule")
	caseData, err := os.ReadFile(filepath.Join(dir, "cases.json"))
	require.NoError(t, err)
	var cases []struct {
		ID              string `json:"id"`
		Description     string `json:"description"`
		ProviderOptions struct {
			Mode             string `json:"experimentationOptOutMode"`
			TelemetryEnabled bool   `json:"enableEvaluationTelemetryOptOutParameter"`
		} `json:"providerOptions"`
		Evaluations []immediateOptOutFixtureEvaluation `json:"evaluations"`
	}
	require.NoError(t, json.Unmarshal(caseData, &cases))
	require.NotEmpty(t, cases)
	for _, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			t.Log(tc.Description)
			config := ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(tc.ProviderOptions.TelemetryEnabled)}
			switch tc.ProviderOptions.Mode {
			case "", "DISABLED":
			case "IMMEDIATE":
				config.ExperimentationOptOutMode = ExperimentationOptOutImmediate
			default:
				t.Fatalf("unsupported fixture mode %q", tc.ProviderOptions.Mode)
			}
			h := newOptOutHarness(t, config)
			var cfg universalFlagsConfiguration
			require.NoError(t, json.Unmarshal(configData, &cfg))
			require.Empty(t, cfg.invalidFlags)
			// Keep the harness writers synchronous so each step observes a complete flush.
			h.provider.mu.Lock()
			h.provider.configuration = &cfg
			h.provider.mu.Unlock()
			require.NotEmpty(t, tc.Evaluations)
			for i, evaluation := range tc.Evaluations {
				t.Run(strconv.Itoa(i), func(t *testing.T) {
					value, details, err := evaluateImmediateOptOutFixture(t, h.client, evaluation)
					if evaluation.Result.ErrorCode == "" {
						require.NoError(t, err)
					} else {
						require.Error(t, err)
					}
					assert.Equal(t, evaluation.Result.ErrorCode, details.ErrorCode)
					assert.Equal(t, evaluation.Result.Reason, details.Reason)
					assert.EqualValues(t, evaluation.Result.Value, value)
					if evaluation.Result.AllocationKey == nil {
						assert.NotContains(t, details.FlagMetadata, metadataAllocationKey)
						assert.Empty(t, details.Variant)
					} else {
						assert.Equal(t, *evaluation.Result.AllocationKey, details.FlagMetadata[metadataAllocationKey])
						variant, exists := cfg.Flags[evaluation.Flag].Variations[details.Variant]
						require.True(t, exists, "result must identify a configured variant")
						assert.EqualValues(t, evaluation.Result.Value, variant.Value)
					}

					previousExposures := len(h.sink.exposures)
					h.provider.exposureWriter.flush()
					h.provider.flagEvalLoggingWriter.drainAndFlush()
					assert.Len(t, h.sink.exposures, evaluation.ExposureCount)
					for _, exposure := range h.sink.exposures[previousExposures:] {
						assert.Equal(t, evaluation.TargetingKey, exposure.Subject.ID)
						assert.Equal(t, evaluation.Flag, exposure.Flag.Key)
						assert.Equal(t, details.FlagMetadata[metadataAllocationKey], exposure.Allocation.Key)
						assert.Equal(t, details.Variant, exposure.Variant.Key)
					}
					var count int64
					for _, event := range h.sink.events {
						count += event.EvaluationCount
					}
					assert.Equal(t, evaluation.EvaluationCount, count)
				})
			}
		})
	}
}

func evaluateImmediateOptOutFixture(t *testing.T, client *of.Client, e immediateOptOutFixtureEvaluation) (any, of.EvaluationDetails, error) {
	t.Helper()
	ctx := context.Background()
	attributes := of.NewEvaluationContext(e.TargetingKey, e.Attributes)
	switch e.VariationType {
	case "BOOLEAN":
		details, err := client.BooleanValueDetails(ctx, e.Flag, e.DefaultValue.(bool), attributes)
		return details.Value, details.EvaluationDetails, err
	case "STRING":
		details, err := client.StringValueDetails(ctx, e.Flag, e.DefaultValue.(string), attributes)
		return details.Value, details.EvaluationDetails, err
	case "INTEGER":
		details, err := client.IntValueDetails(ctx, e.Flag, int64(e.DefaultValue.(float64)), attributes)
		return details.Value, details.EvaluationDetails, err
	case "NUMERIC":
		details, err := client.FloatValueDetails(ctx, e.Flag, e.DefaultValue.(float64), attributes)
		return details.Value, details.EvaluationDetails, err
	case "JSON":
		details, err := client.ObjectValueDetails(ctx, e.Flag, e.DefaultValue, attributes)
		return details.Value, details.EvaluationDetails, err
	default:
		t.Fatalf("unsupported fixture variation type %q", e.VariationType)
		return nil, of.EvaluationDetails{}, nil
	}
}
