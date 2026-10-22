// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/open-feature/go-sdk/openfeature/isolated"
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
	maps.Copy(metadata, (&split{Features: parsed}).featureMetadata())
	assert.Equal(t, boundary, decodedFeatureValues(metadata, metadataEvaluationFeaturesKey)[boundary])
	assert.Equal(t, "status_quo", decodedFeatureValues(metadata, metadataExposureFeaturesKey)["holdout.assignment_group"])
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
		(&split{Features: parsed}).featureMetadata()
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
	maps.Copy(metadata, (&split{Features: parsed}).featureMetadata())
	e := ExposureEvaluation{Details: of.InterfaceEvaluationDetails{EvaluationDetails: of.EvaluationDetails{
		ResolutionDetail: of.ResolutionDetail{FlagMetadata: metadata},
	}}}
	assert.Equal(t, map[string]any{"holdout.key": "q3", "holdout.assignment_group": "status_quo", "shared": true}, e.Features())
	assert.Equal(t, map[string]any{"shared": true, "exposure": float64(2)}, decodedFeatureValues(metadata, metadataExposureFeaturesKey))
	assert.Equal(t, map[string]any{"shared": true, "evaluation": "all_shipped"}, decodedFeatureValues(metadata, metadataEvaluationFeaturesKey))
	e.Features()["holdout.key"] = "customer edit"
	assert.Equal(t, "q3", e.Features()["holdout.key"])
	assert.Nil(t, (ExposureEvaluation{}).Features())
	assert.Nil(t, decodedFeatureValues(of.FlagMetadata{"__dd_evaluation_feature.x": "v"}, metadataEvaluationFeaturesKey))
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
					d.featuresKey = encodeFeatureValues(map[string]any{"holdout.assignment_group": group})
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
			s := &split{Features: features}
			s.featureMetadata() // compile outside the timed loop
			b.ReportAllocs()
			for b.Loop() {
				metadata := of.FlagMetadata{}
				maps.Copy(metadata, s.featureMetadata())
				_ = featureValuesKey(metadata, metadataEvaluationFeaturesKey)
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
			d := evalDetails{flagKey: "flag", variant: "on", allocationKey: "allocation", targetingKey: "subject"}
			features := make(map[string]any)
			for i := range 64 {
				features[fmt.Sprintf("feature%d", i)] = "all_shipped"
			}
			d.featuresKey = encodeFeatureValues(features)
			b.ReportAllocs()
			for b.Loop() {
				w.aggregator.add(d, nil, 100)
			}
		})
	}
}

// Decode only in tests; production writers share these maps per flush window.
func decodedFeatureValues(metadata of.FlagMetadata, key string) map[string]any {
	var cache featureValuesCache
	return cache.values(featureValuesKey(metadata, key))
}

func TestCompiledSplitFeaturesConcurrentIsolation(t *testing.T) {
	s := &split{Features: splitFeatures{
		{"holdout.key", "q3", []string{"HOOK"}},
		{"shared", true, []string{"HOOK", "EXPOSURE", "EVALUATION"}},
	}}
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			metadata := of.FlagMetadata{}
			maps.Copy(metadata, s.featureMetadata())
			for _, value := range metadata {
				switch value.(type) {
				case string, bool, float64:
				default:
					t.Errorf("non-scalar feature metadata: %T", value)
				}
			}
			metadata[metadataHookFeaturePrefix+"holdout.key"] = "customer edit"
			metadata[metadataEvaluationFeaturesKey] = "customer edit"
		})
	}
	wg.Wait()
	metadata := of.FlagMetadata{}
	maps.Copy(metadata, s.featureMetadata())
	assert.Equal(t, "q3", metadata[metadataHookFeaturePrefix+"holdout.key"])
	assert.Equal(t, map[string]any{"shared": true}, decodedFeatureValues(metadata, metadataEvaluationFeaturesKey))
}

func TestSplitFeatureSnapshotsSurviveConfigurationChange(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	allocation := h.provider.configuration.Flags["experiment"].Allocations[0]
	makeSplit := func(group string) *split {
		return &split{VariationKey: "on", Features: splitFeatures{
			{"group", group, []string{"HOOK", "EXPOSURE", "EVALUATION"}},
		}}
	}
	allocation.Splits[0] = makeSplit("old")
	var rows []ExposureEvaluation
	h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) {
		rows = append(rows, e)
		e.Features()["group"] = "customer edit"
		e.Details.FlagMetadata[metadataExposureFeaturesKey] = "customer edit"
		e.Details.FlagMetadata[metadataEvaluationFeaturesKey] = "customer edit"
	}))
	h.evaluate(t, "first-subject", nil)
	// Replace the split before draining either writer. Old queued evaluations must
	// retain the old feature snapshot, independently of the live configuration.
	allocation.Splits[0] = makeSplit("new")
	h.evaluate(t, "second-subject", nil)
	h.provider.exposureWriter.flush()
	h.provider.flagEvalLoggingWriter.drainAndFlush()
	require.Len(t, h.sink.exposures, 2)
	assert.Equal(t, "old", h.sink.exposures[0].Features["group"])
	assert.Equal(t, "new", h.sink.exposures[1].Features["group"])
	require.Len(t, h.sink.events, 2)
	groups := make(map[string]int64)
	for _, event := range h.sink.events {
		groups[event.Features["group"].(string)] += event.EvaluationCount
	}
	assert.Equal(t, map[string]int64{"old": 1, "new": 1}, groups)
	assert.Equal(t, "old", rows[0].Features()["group"])
	assert.Equal(t, "new", rows[1].Features()["group"])
}

func TestFeatureMapsSharedOnlyWithinFlushWindow(t *testing.T) {
	for _, degraded := range []bool{false, true} {
		t.Run(fmt.Sprintf("degraded=%t", degraded), func(t *testing.T) {
			w := newFlagEvalLoggingWriter(ProviderConfig{})
			if degraded {
				w.aggregator.globalCap = 0
				w.aggregator.degradedCap = 2
			} else {
				w.aggregator.globalCap = 2
				w.aggregator.degradedCap = 0
			}
			key := encodeFeatureValues(map[string]any{"shared": true})
			for _, flag := range []string{"first", "second"} {
				w.aggregator.add(evalDetails{flagKey: flag, featuresKey: key}, nil, 100)
			}
			// A dropped observation must not grow the per-window feature cache.
			w.aggregator.add(evalDetails{flagKey: "overflow", featuresKey: `{"other":true}`}, nil, 100)
			assert.Len(t, w.aggregator.featureValues, 1)
			events := w.buildFlushEvents(200)
			require.Len(t, events, 2)
			assert.Equal(t, reflect.ValueOf(events[0].Features).Pointer(), reflect.ValueOf(events[1].Features).Pointer(), "buckets share the immutable map")
			assert.Empty(t, w.aggregator.featureValues)
			w.aggregator.add(evalDetails{flagKey: "next-window", featuresKey: key}, nil, 300)
			next := w.buildFlushEvents(400)
			require.Len(t, next, 1)
			assert.NotEqual(t, reflect.ValueOf(events[0].Features).Pointer(), reflect.ValueOf(next[0].Features).Pointer())
			assert.Equal(t, events[0].Features, next[0].Features)
		})
	}

	h := newOptOutHarness(t, ProviderConfig{})
	w := h.provider.exposureWriter
	key := encodeFeatureValues(map[string]any{"shared": true})
	for _, subject := range []string{"first", "second"} {
		assert.True(t, w.append(exposureEvent{Subject: exposureSubject{ID: subject}, featuresKey: key}))
	}
	require.Len(t, w.buffer, 2)
	assert.Equal(t, reflect.ValueOf(w.buffer[0].Features).Pointer(), reflect.ValueOf(w.buffer[1].Features).Pointer())
	assert.Len(t, w.featureValues, 1)
	assert.False(t, w.append(exposureEvent{Subject: exposureSubject{ID: "first"}, featuresKey: `{"other":true}`}))
	assert.Len(t, w.featureValues, 1, "cache hits do not decode or retain more feature maps")
	w.flush()
	assert.Empty(t, w.featureValues)
}

func TestFeatureEncodingCanonicalAndScalar(t *testing.T) {
	first := splitFeatures{{"a", true, []string{"EVALUATION"}}, {"b", "1", []string{"EVALUATION"}}}.compile()
	second := splitFeatures{{"b", "1", []string{"EVALUATION"}}, {"a", true, []string{"EVALUATION"}}}.compile()
	assert.Equal(t, first.metadata[metadataEvaluationFeaturesKey], second.metadata[metadataEvaluationFeaturesKey])
	keys := make(map[string]bool)
	for _, value := range []any{true, "true", float64(1), "1", "a\nb=c", `"a":true`} {
		key := encodeFeatureValues(map[string]any{"key": value})
		assert.False(t, keys[key], "different scalar types and delimiters cannot alias")
		keys[key] = true
	}
	var cache featureValuesCache
	assert.Nil(t, cache.values("invalid"))
	assert.Equal(t, map[string]any{"scalar": true}, cache.values(`{"scalar":true,"object":{},"array":[],"null":null}`))
}

// BenchmarkClientSplitFeatures includes the real client, provider hooks and a
// synchronous queue drain. Repeated subjects exercise exposure cache hits without
// letting a full evaluation queue turn the benchmark into its overflow fast path.
func BenchmarkClientSplitFeatures(b *testing.B) {
	for _, tc := range []struct {
		name     string
		features bool
		customer bool
	}{
		{name: "none"},
		{name: "two", features: true},
		{name: "two_with_customer", features: true, customer: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			p := newDatadogProvider(ProviderConfig{})
			p.configuration = createTestConfig()
			if tc.features {
				p.configuration.Flags["bool-flag"].Allocations[0].Splits[0].Features = splitFeatures{
					{"holdout.key", "q3", []string{"HOOK"}},
					{"shared", true, []string{"HOOK", "EXPOSURE", "EVALUATION"}},
				}
			}
			api := isolated.NewAPI()
			require.NoError(b, api.SetProviderAndWait(context.Background(), p))
			b.Cleanup(func() { _ = api.Shutdown(context.Background()) })
			client := api.NewClient()
			if tc.customer {
				client.AddHooks(NewExposureHook(func(e ExposureEvaluation) {
					if e.ShouldSend() {
						_ = e.Features()
					}
				}))
			}
			ctx := context.Background()
			ec := of.NewEvaluationContext("subject", map[string]any{"country": "US"})
			evaluate := func() {
				_, err := client.BooleanValue(ctx, "bool-flag", false, ec)
				if err != nil {
					b.Fatal(err)
				}
				if w := p.flagEvalLoggingWriter; w != nil {
					select {
					case event := <-w.events:
						w.aggregator.add(event.d, event.contextAttrs, event.evaluationTimeMs)
					default:
					}
				}
			}
			evaluate()
			b.ReportAllocs()
			for b.Loop() {
				evaluate()
			}
		})
	}
}
