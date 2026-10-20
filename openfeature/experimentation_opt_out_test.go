// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const experimentOptOutTestTime = "2026-10-19T12:00:00Z"

func TestExperimentationOptOutInput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		valid bool
	}{
		{"timestamp", experimentOptOutTestTime, true},
		{"fractional seconds", "2026-10-19T12:00:00.123456789Z", true},
		{"offset", "2026-10-19T08:00:00-04:00", true},
		{"future is not a schedule", "2099-01-01T00:00:00Z", true},
		{"empty", "", false},
		{"invalid", "private-invalid-value", false},
		{"boolean", true, false},
		{"number", 123, false},
		{"null", nil, false},
		{"time object", time.Now(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attrs := map[string]any{experimentationOptOutAtAttribute: tc.value}
			exclude, err := ExperimentationOptOutImmediate.excludeExperiments(attrs)
			assert.Equal(t, tc.valid, exclude)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, errInvalidContext)
				assert.NotContains(t, err.Error(), "private-invalid-value")
			}
			exclude, err = ExperimentationOptOutDisabled.excludeExperiments(attrs)
			require.NoError(t, err)
			assert.False(t, exclude)
		})
	}
	exclude, err := ExperimentationOptOutImmediate.excludeExperiments(nil)
	require.NoError(t, err)
	assert.False(t, exclude)
}

func TestExperimentationOptOutInvalidMode(t *testing.T) {
	for _, mode := range []ExperimentationOptOutMode{-1, 2} {
		p, err := NewDatadogProvider(ProviderConfig{ExperimentationOptOutMode: mode})
		require.ErrorContains(t, err, "unsupported experimentation opt-out mode")
		assert.Nil(t, p)
	}
}

func TestExperimentationOptOutAllocations(t *testing.T) {
	for _, doLog := range []*bool{new(true), nil} {
		h := newOptOutHarness(t, ProviderConfig{ExperimentationOptOutMode: ExperimentationOptOutImmediate})
		f := h.provider.configuration.Flags["experiment"]
		f.Allocations[0].DoLog = doLog
		f.Variations["ordinary"] = &variant{Key: "ordinary", Value: true}
		f.Allocations = append(f.Allocations, &allocation{Key: "ordinary", DoLog: new(false),
			Splits: []*split{{VariationKey: "ordinary"}}})
		ctx := of.NewEvaluationContext("subject", map[string]any{experimentationOptOutAtAttribute: experimentOptOutTestTime})
		result, err := h.client.BooleanValueDetails(context.Background(), "experiment", false, ctx)
		require.NoError(t, err)
		assert.True(t, result.Value, "ordinary delivery is not forced to false")
		assert.Equal(t, "ordinary", result.Variant)
		assert.Equal(t, "ordinary", result.FlagMetadata[metadataAllocationKey])
		assert.Equal(t, false, result.FlagMetadata[metadataDoLogKey])
		h.provider.exposureWriter.flush()
		assert.Empty(t, h.sink.exposures)

		// Ordinary targeting still applies after exclusion. No match uses the caller default.
		f.Allocations[1].Rules = []*rule{{Conditions: []*condition{{
			Attribute: "beta", Operator: operatorOneOf, Value: []string{"true"},
		}}}}
		result, err = h.client.BooleanValueDetails(context.Background(), "experiment", false, ctx)
		require.NoError(t, err)
		assert.False(t, result.Value)
		assert.Equal(t, of.DefaultReason, result.Reason)
		assert.Empty(t, result.Variant)

		f.Enabled = false
		result, err = h.client.BooleanValueDetails(context.Background(), "experiment", false, ctx)
		require.NoError(t, err)
		assert.Equal(t, of.DisabledReason, result.Reason)
	}
}

func TestExperimentationOptOutIndependentTelemetry(t *testing.T) {
	for _, suppressTelemetry := range []bool{false, true} {
		t.Run(strconv.FormatBool(suppressTelemetry), func(t *testing.T) {
			h := newOptOutHarness(t, ProviderConfig{
				ExperimentationOptOutMode:                ExperimentationOptOutImmediate,
				EnableEvaluationTelemetryOptOutParameter: new(true),
			})
			// Enrollment still produces an ordinary exposure.
			h.evaluate(t, "subject", nil)
			result, err := h.client.BooleanValueDetails(context.Background(), "experiment", false,
				of.NewEvaluationContext("subject", map[string]any{
					experimentationOptOutAtAttribute: experimentOptOutTestTime,
					optOutTestParameter:              suppressTelemetry,
				}))
			require.NoError(t, err)
			assert.False(t, result.Value)
			assert.Equal(t, of.DefaultReason, result.Reason)
			h.provider.exposureWriter.flush()
			assert.Len(t, h.sink.exposures, 1, "no withdrawal exposure or sentinel")
			h.provider.flagEvalLoggingWriter.drainAndFlush()
			var count int64
			for _, e := range h.sink.events {
				count += e.EvaluationCount
			}
			want := int64(2)
			if suppressTelemetry {
				want = 1
			}
			assert.Equal(t, want, count, "evaluation telemetry remains a separate control")
		})
	}
}

func TestExperimentationOptOutInvalidContext(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{ExperimentationOptOutMode: ExperimentationOptOutImmediate})
	result, err := h.client.BooleanValueDetails(context.Background(), "experiment", false,
		of.NewEvaluationContext("subject", map[string]any{experimentationOptOutAtAttribute: true}))
	require.Error(t, err)
	assert.False(t, result.Value)
	assert.Equal(t, of.InvalidContextCode, result.ErrorCode)
	h.provider.exposureWriter.flush()
	assert.Empty(t, h.sink.exposures)
}

func TestExperimentationOptOutDisabled(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	h.evaluate(t, "subject", map[string]any{experimentationOptOutAtAttribute: "invalid"})
	h.assertCounts(t, 1, 1, 1)
}

func TestExperimentationOptOutPreservesEligibleBuckets(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{ExperimentationOptOutMode: ExperimentationOptOutImmediate})
	f := h.provider.configuration.Flags["experiment"]
	f.Variations["off"] = &variant{Key: "off", Value: false}
	f.Allocations[0].Splits = []*split{
		{VariationKey: "on", Shards: []*shard{{Salt: "experiment", TotalShards: 10000, Ranges: []*shardRange{{Start: 0, End: 5000}}}}},
		{VariationKey: "off", Shards: []*shard{{Salt: "experiment", TotalShards: 10000, Ranges: []*shardRange{{Start: 5000, End: 10000}}}}},
	}
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			attrs := of.FlattenedContext{of.TargetingKey: strconv.Itoa(i)}
			baseline := evaluateFlag(f, false, attrs, time.Now(), false)
			eligible := h.provider.BooleanEvaluation(context.Background(), "experiment", false, attrs)
			assert.Equal(t, baseline.VariantKey, eligible.Variant)
			assert.Equal(t, baseline.Reason, eligible.Reason)
			attrs[experimentationOptOutAtAttribute] = experimentOptOutTestTime
			excluded := h.provider.BooleanEvaluation(context.Background(), "experiment", false, attrs)
			assert.Equal(t, of.DefaultReason, excluded.Reason)
			assert.Empty(t, excluded.Variant)
			delete(attrs, experimentationOptOutAtAttribute)
			reincluded := h.provider.BooleanEvaluation(context.Background(), "experiment", false, attrs)
			assert.Equal(t, baseline.VariantKey, reincluded.Variant)
		})
	}
	wg.Wait()
}
