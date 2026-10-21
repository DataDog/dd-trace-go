// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExperimentationOptOutFutureExposures(t *testing.T) {
	cutoff, err := time.Parse(time.RFC3339Nano, experimentOptOutTestTime)
	require.NoError(t, err)
	for _, tc := range []struct {
		name       string
		activation *time.Time
		doLog      *bool
		continues  bool
		exposures  int
	}{
		{"older experiment", new(cutoff.Add(-time.Second)), new(true), true, 2},
		{"older experiment with default doLog", new(cutoff.Add(-time.Second)), nil, true, 2},
		{"equal activation", new(cutoff), new(true), false, 1},
		{"newer activation", new(cutoff.Add(time.Second)), new(true), false, 1},
		{"unknown activation", nil, new(true), false, 1},
		{"ordinary allocation", nil, new(false), true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newOptOutHarness(t, ProviderConfig{
				ExperimentationOptOutMode:                ExperimentationOptOutFutureExperimentsOnly,
				EnableEvaluationTelemetryOptOutParameter: new(true),
			})
			a := h.provider.configuration.Flags["experiment"].Allocations[0]
			a.RandomizationActivatedAt, a.DoLog = tc.activation, tc.doLog
			h.evaluate(t, "enrolled", nil)
			attrs := map[string]any{experimentationOptOutAtAttribute: experimentOptOutTestTime}
			for _, subject := range []string{"enrolled", "enrolled", "first-evaluation"} {
				details, err := h.client.BooleanValueDetails(context.Background(), "experiment", false,
					of.NewEvaluationContext(subject, attrs))
				require.NoError(t, err)
				assert.Equal(t, tc.continues, details.Value)
			}
			// Restoring consent does not invalidate an unchanged exposure cache entry.
			h.evaluate(t, "enrolled", nil)
			h.provider.exposureWriter.flush()
			assert.Len(t, h.sink.exposures, tc.exposures, "normal deduplication; no withdrawal event")
			for _, exposure := range h.sink.exposures {
				assert.Equal(t, "allocation", exposure.Allocation.Key)
				assert.Equal(t, "on", exposure.Variant.Key)
			}
			h.provider.flagEvalLoggingWriter.drainAndFlush()
			var count int64
			for _, event := range h.sink.events {
				count += event.EvaluationCount
			}
			assert.Equal(t, int64(5), count, "experimentation opt-out does not suppress evaluations")
		})
	}
}

func TestExperimentationOptOutFutureTelemetryIndependence(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{
		ExperimentationOptOutMode:                ExperimentationOptOutFutureExperimentsOnly,
		EnableEvaluationTelemetryOptOutParameter: new(true),
	})
	old := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	h.provider.configuration.Flags["experiment"].Allocations[0].RandomizationActivatedAt = &old
	h.evaluate(t, "subject", map[string]any{
		experimentationOptOutAtAttribute: experimentOptOutTestTime,
		optOutTestParameter:              true,
	})
	h.assertCounts(t, 0, 1, 1)
}

func TestExperimentationOptOutFutureConfigurationRefresh(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{ExperimentationOptOutMode: ExperimentationOptOutFutureExperimentsOnly})
	old := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	h.provider.configuration.Flags["experiment"].Allocations[0].RandomizationActivatedAt = &old
	attrs := map[string]any{experimentationOptOutAtAttribute: experimentOptOutTestTime}
	h.evaluate(t, "subject", attrs)

	// A later configuration date and an edited schedule do not create a new run.
	data, err := json.Marshal(h.provider.configuration)
	require.NoError(t, err)
	var refreshed universalFlagsConfiguration
	require.NoError(t, json.Unmarshal(data, &refreshed))
	refreshed.CreatedAt = old.AddDate(1, 0, 0)
	refreshed.Flags["experiment"].Allocations[0].StartAt = new(old.AddDate(-1, 0, 0))
	h.provider.mu.Lock()
	h.provider.configuration = &refreshed
	h.provider.mu.Unlock()
	h.evaluate(t, "subject", attrs)
	h.evaluate(t, "new-subject", attrs)

	// A new allocation has its own activation timestamp.
	refreshed.Flags["experiment"].Allocations[0].Key = "new-allocation"
	refreshed.Flags["experiment"].Allocations[0].RandomizationActivatedAt = new(old.AddDate(0, 1, 0))
	result, err := h.client.BooleanValueDetails(context.Background(), "experiment", false, of.NewEvaluationContext("subject", attrs))
	require.NoError(t, err)
	assert.False(t, result.Value)
	assert.Equal(t, of.DefaultReason, result.Reason)
	h.provider.exposureWriter.flush()
	assert.Len(t, h.sink.exposures, 2, "refresh did not create a new exposure for the existing subject")
}

func TestExperimentationOptOutFutureInput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"malformed", "invalid"},
		{"empty", ""},
		{"null", nil},
		{"boolean", false},
		{"number", 123},
		{"array", []any{experimentOptOutTestTime}},
		{"object", map[string]any{"timestamp": experimentOptOutTestTime}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exclusion, err := ExperimentationOptOutFutureExperimentsOnly.exclusion(map[string]any{experimentationOptOutAtAttribute: tc.value})
			require.ErrorIs(t, err, errInvalidContext)
			assert.Equal(t, experimentationExclusion{}, exclusion)
		})
	}
}

func TestExperimentationOptOutFutureActivationParsing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		valid   bool
		unknown bool
	}{
		{"missing", "", true, true},
		{"null", ",\"randomizationActivatedAt\":null", true, true},
		{"timestamp", ",\"randomizationActivatedAt\":\"2026-10-01T12:00:00Z\"", true, false},
		{"malformed", ",\"randomizationActivatedAt\":\"invalid\"", false, false},
		{"empty", ",\"randomizationActivatedAt\":\"\"", false, false},
		{"number", ",\"randomizationActivatedAt\":123", false, false},
		{"boolean", ",\"randomizationActivatedAt\":true", false, false},
		{"array", ",\"randomizationActivatedAt\":[]", false, false},
		{"object", ",\"randomizationActivatedAt\":{}", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var config universalFlagsConfiguration
			require.NoError(t, json.Unmarshal([]byte(`{"format":"SERVER","flags":{
				"test":{"key":"test","enabled":true,"variationType":"BOOLEAN",
				"variations":{"on":{"key":"on","value":true}},
				"allocations":[{"key":"a","doLog":true,"splits":[{"variationKey":"on","shards":[]}]`+tc.raw+`}]},
				"neighbor":{"key":"neighbor","enabled":true,"variationType":"BOOLEAN",
				"variations":{"on":{"key":"on","value":true}},
				"allocations":[{"key":"a","doLog":false,"splits":[{"variationKey":"on","shards":[]}]}]}
			}}`), &config))
			assert.Contains(t, config.Flags, "neighbor")
			if tc.valid {
				require.Contains(t, config.Flags, "test")
				assert.Equal(t, tc.unknown, config.Flags["test"].Allocations[0].RandomizationActivatedAt == nil)
			} else {
				require.NotContains(t, config.Flags, "test")
				for _, mode := range []ExperimentationOptOutMode{
					ExperimentationOptOutDisabled,
					ExperimentationOptOutImmediate,
					ExperimentationOptOutFutureExperimentsOnly,
				} {
					exclusion, err := mode.exclusion(map[string]any{experimentationOptOutAtAttribute: experimentOptOutTestTime})
					require.NoError(t, err)
					result := evaluateConfiguredFlag(&config, "test", false, nil, time.Now(), exclusion)
					assert.ErrorIs(t, result.Error, errParseError)
					assert.Equal(t, false, result.Value)
					neighbor := evaluateConfiguredFlag(&config, "neighbor", false, nil, time.Now(), exclusion)
					assert.NoError(t, neighbor.Error)
					assert.Equal(t, true, neighbor.Value)
				}
			}
		})
	}
}

func TestExperimentationOptOutFutureConcurrentBuckets(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{ExperimentationOptOutMode: ExperimentationOptOutFutureExperimentsOnly})
	f := h.provider.configuration.Flags["experiment"]
	activation := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	f.Allocations[0].RandomizationActivatedAt = &activation
	f.Variations["off"] = &variant{Key: "off", Value: false}
	f.Allocations[0].Splits = []*split{
		{VariationKey: "on", Shards: []*shard{{Salt: "holdout", TotalShards: 10000, Ranges: []*shardRange{{Start: 0, End: 1000}}}}},
		{VariationKey: "off", Shards: []*shard{{Salt: "holdout", TotalShards: 10000, Ranges: []*shardRange{{Start: 1000, End: 10000}}}}},
	}
	var wg sync.WaitGroup
	var groups sync.Map
	for i := range 100 {
		wg.Go(func() {
			attrs := of.FlattenedContext{of.TargetingKey: strconv.Itoa(i)}
			baseline := h.provider.BooleanEvaluation(context.Background(), "experiment", false, attrs)
			assert.NotEmpty(t, baseline.Variant)
			groups.Store(baseline.Variant, true)
			attrs[experimentationOptOutAtAttribute] = activation.Add(time.Second).Format(time.RFC3339Nano)
			continued := h.provider.BooleanEvaluation(context.Background(), "experiment", false, attrs)
			assert.Equal(t, baseline.Value, continued.Value)
			assert.Equal(t, baseline.Variant, continued.Variant)
			assert.Equal(t, baseline.Reason, continued.Reason)
			attrs[experimentationOptOutAtAttribute] = activation.Format(time.RFC3339Nano)
			excluded := h.provider.BooleanEvaluation(context.Background(), "experiment", false, attrs)
			assert.Equal(t, of.DefaultReason, excluded.Reason)
			assert.Empty(t, excluded.Variant)
		})
	}
	wg.Wait()
	_, on := groups.Load("on")
	_, off := groups.Load("off")
	assert.True(t, on && off, "both randomized groups must be exercised")
}

func BenchmarkExperimentationExclusion(b *testing.B) {
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	a := &allocation{DoLog: new(true), RandomizationActivatedAt: &at}
	for name, mode := range map[string]ExperimentationOptOutMode{
		"disabled":  ExperimentationOptOutDisabled,
		"immediate": ExperimentationOptOutImmediate,
		"future":    ExperimentationOptOutFutureExperimentsOnly,
	} {
		b.Run(name, func(b *testing.B) {
			attrs := map[string]any{experimentationOptOutAtAttribute: at.Format(time.RFC3339Nano)}
			b.ReportAllocs()
			for b.Loop() {
				exclusion, err := mode.exclusion(attrs)
				if err != nil {
					b.Fatal(err)
				}
				if exclusion.excludes(a) != (mode != ExperimentationOptOutDisabled) {
					b.Fatal("unexpected exclusion")
				}
			}
		})
	}
}
