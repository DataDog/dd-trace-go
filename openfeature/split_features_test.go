// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"encoding/json"
	"fmt"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitFeaturesParsing(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  splitFeatures
	}{
		{"null", `null`, nil},
		{"object", `{}`, nil},
		{"scalar", `true`, nil},
		{"malformed_entries", `[null,1,"x",{}, {"key":4,"value":true,"destinations":["HOOK"]},
			{"key":"a","value":null,"destinations":["HOOK"]},
			{"key":"b","value":[],"destinations":["HOOK"]},
			{"key":"c","value":{},"destinations":["HOOK"]},
			{"key":"d","value":true,"destinations":"HOOK"},
			{"key":"e","value":true,"destinations":[]},
			{"key":"","value":true,"destinations":["HOOK"]}]`, nil},
		{"valid_neighbors", `[{"key":"bad","value":{}},
			{"key":"s","value":"holdout","destinations":["HOOK",null,2,"",false],"future":{}},
			{"key":"n","value":0.5,"destinations":["EXPOSURE","EVALUATION"]},
			{"key":"b","value":false,"destinations":["FUTURE"]}]`,
			splitFeatures{{"s", "holdout", []string{"HOOK"}}, {"n", 0.5, []string{"EXPOSURE", "EVALUATION"}}, {"b", false, []string{"FUTURE"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var parsed split
			require.NoError(t, json.Unmarshal([]byte(`{"variationKey":"on","features":`+tt.input+`}`), &parsed))
			assert.Equal(t, "on", parsed.VariationKey)
			assert.Equal(t, tt.want, parsed.Features)
		})
	}
}

func TestSplitFeatureRouting(t *testing.T) {
	metadata := of.FlagMetadata{}
	features := splitFeatures{
		{"holdout.key", "q3", []string{"HOOK", "HOOK"}},
		{"holdout.assignment_group", "status_quo", []string{"HOOK"}},
		{"shared", true, []string{"HOOK", "EXPOSURE", "EVALUATION", "FUTURE"}},
		{"exposure", float64(2), []string{"EXPOSURE"}},
		{"evaluation", "all_shipped", []string{"EVALUATION"}},
		{"holdout.key", "ignored_duplicate", []string{"HOOK", "EXPOSURE"}},
	}
	features.addMetadata(metadata)
	e := ExposureEvaluation{Details: of.InterfaceEvaluationDetails{EvaluationDetails: of.EvaluationDetails{
		ResolutionDetail: of.ResolutionDetail{FlagMetadata: metadata},
	}}}
	assert.Equal(t, map[string]any{"holdout.key": "q3", "holdout.assignment_group": "status_quo", "shared": true}, e.Features())
	assert.Equal(t, map[string]any{"shared": true, "exposure": float64(2)}, splitFeatureValues(metadata, metadataExposureFeaturePrefix))
	assert.Equal(t, map[string]any{"shared": true, "evaluation": "all_shipped"}, evaluationFeatureValues(metadata))
	e.Features()["holdout.key"] = "customer edit"
	assert.Equal(t, "q3", e.Features()["holdout.key"])
	assert.Nil(t, (ExposureEvaluation{}).Features())
}

func TestSplitFeaturesDeliveredByProvider(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	allocation := h.provider.configuration.Flags["experiment"].Allocations[0]
	allocation.Splits[0].Features = splitFeatures{
		{"holdout.key", "q3", []string{"HOOK"}},
		{"only_exposure", true, []string{"EXPOSURE"}},
		{"only_evaluation", float64(3), []string{"EVALUATION"}},
	}
	var rows []ExposureEvaluation
	h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) { rows = append(rows, e) }))
	h.evaluate(t, "subject", nil)
	h.evaluate(t, "subject", nil)
	h.provider.exposureWriter.flush()
	h.provider.flagEvalLoggingWriter.drainAndFlush()
	require.Len(t, rows, 2)
	assert.Equal(t, map[string]any{"holdout.key": "q3"}, rows[0].Features())
	assert.True(t, rows[0].ShouldSend())
	assert.True(t, rows[1].IsCacheHit())
	require.Len(t, h.sink.exposures, 1)
	assert.Equal(t, map[string]any{"only_exposure": true}, h.sink.exposures[0].Features)
	require.Len(t, h.sink.events, 1)
	assert.Equal(t, int64(2), h.sink.events[0].EvaluationCount)
	assert.Equal(t, map[string]any{"only_evaluation": float64(3)}, h.sink.events[0].Features)
	assert.Nil(t, h.sink.events[0].Context, "features do not require subject-data consent")
}

func TestEvaluationAggregationIncludesFeatures(t *testing.T) {
	for _, fullData := range []bool{false, true} {
		for _, degraded := range []bool{false, true} {
			t.Run(fmt.Sprintf("full_data=%t/degraded=%t", fullData, degraded), func(t *testing.T) {
				w := newFlagEvalLoggingWriter(ProviderConfig{})
				if degraded {
					w.aggregator.globalCap = 0
				}
				d := evalDetails{flagKey: "flag", variant: "on", allocationKey: "allocation", targetingKey: "subject", observeFullEvaluationData: fullData}
				for _, group := range []string{"status_quo", "status_quo", "all_shipped"} {
					d.features = map[string]any{"holdout.assignment_group": group}
					w.aggregator.add(d, map[string]any{"country": "FR"}, 100)
				}
				events := w.buildFlushEvents(200)
				require.Len(t, events, 2)
				counts := map[string]int64{}
				for _, event := range events {
					counts[event.Features["holdout.assignment_group"].(string)] = event.EvaluationCount
					if !fullData || degraded {
						assert.Nil(t, event.Context)
					}
				}
				assert.Equal(t, map[string]int64{"status_quo": 2, "all_shipped": 1}, counts)
			})
		}
	}
}

func TestFeaturelessEventsOmitFeatures(t *testing.T) {
	for _, event := range []any{exposureEvent{}, flagEvalLoggingEvent{}} {
		encoded, err := json.Marshal(event)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), `"features"`)
	}
}

func BenchmarkSplitFeatureRouting(b *testing.B) {
	features := splitFeatures{{"holdout.key", "q3", []string{"HOOK"}}, {"shared", true, []string{"HOOK", "EXPOSURE", "EVALUATION"}}}
	for b.Loop() {
		metadata := of.FlagMetadata{}
		features.addMetadata(metadata)
		_ = evaluationFeatureValues(metadata)
	}
}
