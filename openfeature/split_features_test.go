// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
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
		{"first_valid_key_wins", `[{"key":"a","value":{},"destinations":["HOOK"]},
			{"key":"a","value":"first","destinations":["HOOK"]},
			{"key":"a","value":"second","destinations":["EXPOSURE"]},
			{"key":"b","value":true,"destinations":["FUTURE"]},
			{"key":"b","value":false,"destinations":["HOOK"]}]`,
			splitFeatures{{"a", "first", []string{"HOOK"}}, {"b", true, []string{"FUTURE"}}}},
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

func TestSplitFeatureLimits(t *testing.T) {
	logger := withLogLevel(t, log.LevelDebug)
	boundary := strings.Repeat("x", maxFieldLength)
	features := make(splitFeatures, 0, 5+2*maxContextFields)
	features = append(features, splitFeatures{
		{boundary + "x", true, []string{"HOOK"}},
		{"too_long", boundary + "x", []string{"HOOK"}},
		{boundary, boundary, []string{"HOOK", "EXPOSURE", "EVALUATION"}},
		{"holdout.key", "q3", []string{"HOOK", "EXPOSURE"}},
		{"holdout.assignment_group", "status_quo", []string{"HOOK", "EXPOSURE"}},
	}...)
	// Invalid entries and duplicates must not consume the accepted-feature budget.
	for i := range maxContextFields {
		features = append(features, splitFeature{"holdout.key", "ignored", []string{"HOOK"}})
		features = append(features, splitFeature{fmt.Sprintf("feature%d", i), true, []string{"HOOK"}})
	}
	encoded, err := json.Marshal(features)
	require.NoError(t, err)
	var parsed splitFeatures
	require.NoError(t, json.Unmarshal(encoded, &parsed))
	require.Len(t, parsed, maxContextFields)
	assert.Equal(t, features[2:5], parsed[:3])
	assert.Equal(t, fmt.Sprintf("feature%d", maxContextFields-4), parsed[len(parsed)-1].Key)
	metadata := of.FlagMetadata{}
	parsed.addMetadata(metadata)
	assert.Equal(t, boundary, evaluationFeatureValues(metadata)[boundary])
	assert.Equal(t, "status_quo", splitFeatureValues(metadata, metadataExposureFeaturePrefix)["holdout.assignment_group"])
	assert.Equal(t, 2, logger.countContaining("ignoring malformed split feature entry"))
	assert.Equal(t, 1, logger.countContaining("entry limit reached"))

	// Metadata rejection must not change the selected variant or doLog.
	h := newOptOutHarness(t, ProviderConfig{})
	allocation := h.provider.configuration.Flags["experiment"].Allocations[0]
	before := evaluateFlag(h.provider.configuration.Flags["experiment"], false, map[string]any{}, time.Now())
	allocation.Splits[0].Features = parsed
	after := evaluateFlag(h.provider.configuration.Flags["experiment"], false, map[string]any{}, time.Now())
	assert.Equal(t, before.Value, after.Value)
	assert.Equal(t, before.VariantKey, after.VariantKey)
	assert.Equal(t, before.Metadata[metadataDoLogKey], after.Metadata[metadataDoLogKey])
}

var benchmarkExposureHooks []of.Hook

func BenchmarkExposureHooks(b *testing.B) {
	p := &DatadogProvider{hooks: []of.Hook{
		of.UnimplementedHook{}, of.UnimplementedHook{}, of.UnimplementedHook{}, newExposureHook(nil),
	}}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkExposureHooks = p.Hooks()
	}
}

func TestMalformedSplitFeaturesLogAtParseTime(t *testing.T) {
	logger := withLogLevel(t, log.LevelDebug)
	var parsed splitFeatures
	require.NoError(t, json.Unmarshal([]byte(`[
		1,
		{"key":"","value":true,"destinations":["HOOK"]},
		{"key":"secret","value":{},"destinations":["HOOK"]},
		{"key":"secret","value":true,"destinations":[]},
		{"key":"good","value":true,"destinations":["HOOK"]}
	]`), &parsed))
	require.Len(t, parsed, 1)
	assert.Equal(t, 4, logger.countContaining("ignoring malformed split feature entry"))
	assert.Zero(t, logger.countContaining("secret"), "diagnostics must not disclose feature contents")
	for range 2 {
		parsed.addMetadata(of.FlagMetadata{})
	}
	assert.Equal(t, 4, logger.countContaining("ignoring malformed split feature entry"))
}

func TestUnknownFeatureDestinationsWarnOnceAndStayBounded(t *testing.T) {
	logger := withLogLevel(t, log.LevelWarn)
	unknownFeatureDestinations.mu.Lock()
	previous := unknownFeatureDestinations.names
	unknownFeatureDestinations.names = make(map[string]struct{})
	unknownFeatureDestinations.mu.Unlock()
	t.Cleanup(func() {
		unknownFeatureDestinations.mu.Lock()
		unknownFeatureDestinations.names = previous
		unknownFeatureDestinations.mu.Unlock()
	})
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			for range 2 {
				warnUnknownFeatureDestination(fmt.Sprintf("FUTURE_%d", i))
			}
		})
	}
	wg.Wait()
	unknownFeatureDestinations.mu.Lock()
	count := len(unknownFeatureDestinations.names)
	unknownFeatureDestinations.mu.Unlock()
	assert.Equal(t, 64, count)
	assert.Equal(t, 64, logger.countContaining("ignoring unknown feature destination"))
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
	encoded, err := json.Marshal(features)
	require.NoError(t, err)
	var parsed splitFeatures
	require.NoError(t, json.Unmarshal(encoded, &parsed))
	parsed.addMetadata(metadata)
	e := ExposureEvaluation{Details: of.InterfaceEvaluationDetails{EvaluationDetails: of.EvaluationDetails{
		ResolutionDetail: of.ResolutionDetail{FlagMetadata: metadata},
	}}}
	assert.Equal(t, map[string]any{"holdout.key": "q3", "holdout.assignment_group": "status_quo", "shared": true}, e.Features())
	assert.Equal(t, map[string]any{"shared": true, "exposure": float64(2)}, splitFeatureValues(metadata, metadataExposureFeaturePrefix))
	assert.Equal(t, map[string]any{"shared": true, "evaluation": "all_shipped"}, evaluationFeatureValues(metadata))
	e.Features()["holdout.key"] = "customer edit"
	assert.Equal(t, "q3", e.Features()["holdout.key"])
	assert.Nil(t, (ExposureEvaluation{}).Features())
	assert.Nil(t, evaluationFeatureValues(of.FlagMetadata{metadataEvaluationFeaturePrefix + "x": "v"}))
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
	assert.Equal(t, rows[0].Features(), rows[1].Features())
	require.Len(t, h.sink.exposures, 1)
	assert.Equal(t, map[string]any{"only_exposure": true}, h.sink.exposures[0].Features)
	require.Len(t, h.sink.events, 1)
	assert.Equal(t, int64(2), h.sink.events[0].EvaluationCount)
	assert.Equal(t, map[string]any{"only_evaluation": float64(3)}, h.sink.events[0].Features)
	assert.Nil(t, h.sink.events[0].Context, "features do not require subject-data consent")
}

func TestEvaluationAggregationIncludesFeatures(t *testing.T) {
	for _, fullData := range []bool{false, true} {
		for _, tier := range []string{"full", "global_cap", "per_flag_cap"} {
			t.Run(fmt.Sprintf("full_data=%t/tier=%s", fullData, tier), func(t *testing.T) {
				w := newFlagEvalLoggingWriter(ProviderConfig{})
				switch tier {
				case "global_cap":
					w.aggregator.globalCap = 0
				case "per_flag_cap":
					w.aggregator.perFlagCap = 0
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
					if !fullData || tier != "full" {
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
	for _, count := range []int{2, 64, maxContextFields} {
		b.Run(fmt.Sprintf("features=%d", count), func(b *testing.B) {
			features := make(splitFeatures, count)
			for i := range features {
				features[i] = splitFeature{fmt.Sprintf("feature%d", i), true, []string{"HOOK", "EXPOSURE", "EVALUATION"}}
			}
			b.ReportAllocs()
			for b.Loop() {
				metadata := of.FlagMetadata{}
				features.addMetadata(metadata)
				_ = evaluationFeatureValues(metadata)
			}
		})
	}
}

func BenchmarkFeatureAggregation(b *testing.B) {
	for _, degraded := range []bool{false, true} {
		b.Run(fmt.Sprintf("degraded=%t", degraded), func(b *testing.B) {
			w := newFlagEvalLoggingWriter(ProviderConfig{})
			if degraded {
				w.aggregator.globalCap = 0
			}
			d := evalDetails{flagKey: "flag", variant: "on", allocationKey: "allocation", targetingKey: "subject", features: make(map[string]any)}
			for i := range 64 {
				d.features[fmt.Sprintf("feature%d", i)] = "all_shipped"
			}
			b.ReportAllocs()
			for b.Loop() {
				w.aggregator.add(d, nil, 100)
			}
		})
	}
}
