// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const experimentOptOutTestTime = "2026-10-19T12:00:00Z"

func TestExperimentationOptOutInput(t *testing.T) {
	for _, tc := range []struct {
		name       string
		attributes map[string]any
		exclude    bool
		invalid    bool
	}{
		{name: "no attributes"},
		{name: "missing attribute", attributes: map[string]any{"country": "US"}},
		{"timestamp", map[string]any{experimentationOptOutAtAttribute: experimentOptOutTestTime}, true, false},
		{"fractional seconds", map[string]any{experimentationOptOutAtAttribute: "2026-10-19T12:00:00.123456789Z"}, true, false},
		{"offset", map[string]any{experimentationOptOutAtAttribute: "2026-10-19T08:00:00-04:00"}, true, false},
		{"maximum offset", map[string]any{experimentationOptOutAtAttribute: "2026-10-19T08:00:00+23:59"}, true, false},
		{"unknown zero offset", map[string]any{experimentationOptOutAtAttribute: "2026-10-19T08:00:00-00:00"}, true, false},
		{"long fraction", map[string]any{experimentationOptOutAtAttribute: "2026-10-19T08:00:00.123456789123Z"}, true, false},
		{"leap day", map[string]any{experimentationOptOutAtAttribute: "2024-02-29T08:00:00Z"}, true, false},
		{"future is not a schedule", map[string]any{experimentationOptOutAtAttribute: "2099-01-01T00:00:00Z"}, true, false},
		{"empty", map[string]any{experimentationOptOutAtAttribute: ""}, false, true},
		{"invalid", map[string]any{experimentationOptOutAtAttribute: "private-invalid-value"}, false, true},
		{"boolean", map[string]any{experimentationOptOutAtAttribute: true}, false, true},
		{"number", map[string]any{experimentationOptOutAtAttribute: 123}, false, true},
		{"null", map[string]any{experimentationOptOutAtAttribute: nil}, false, true},
		{"time object", map[string]any{experimentationOptOutAtAttribute: time.Now()}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exclude, err := ExperimentationOptOutImmediate.exclusion(tc.attributes)
			assert.Equal(t, tc.exclude, exclude.mode == ExperimentationOptOutImmediate)
			if tc.invalid {
				require.ErrorIs(t, err, errInvalidContext)
				assert.NotContains(t, err.Error(), "private-invalid-value")
			} else {
				require.NoError(t, err)
			}
			exclude, err = ExperimentationOptOutDisabled.exclusion(tc.attributes)
			require.NoError(t, err)
			assert.Equal(t, experimentationExclusion{}, exclude)
		})
	}
}

func TestExperimentationOptOutInvalidMode(t *testing.T) {
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() { internalconfig.SetUseFreshConfig(false); internalconfig.CreateNew() })
	t.Setenv("DD_FEATURE_FLAGS_ENABLED", "true")
	t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "remote_config")
	for _, tc := range []struct {
		name string
		mode ExperimentationOptOutMode
	}{
		{"negative", -1},
		{"unknown", 99},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := NewDatadogProvider(ProviderConfig{ExperimentationOptOutMode: tc.mode})
			require.ErrorContains(t, err, "unsupported experimentation opt-out mode")
			assert.Nil(t, p)
		})
	}
}

func TestExperimentationOptOutTimestampMatchesAllocationStartAt(t *testing.T) {
	for _, stamp := range []string{
		experimentOptOutTestTime,
		"2026-10-19T08:00:00-04:00",
		"2026-10-19T08:00:00.123456789Z",
		"2026-10-19T8:00:00Z",
		"2026-10-19T08:00:00,1Z",
		"2026-10-19T08:00:00+24:00",
		"2026-10-19T08:00:00+00:60",
		"2026-02-30T08:00:00Z",
		"2026-10-19",
		"2026-10-19T08:00:00",
		"not-a-timestamp",
		"2026-10-19t08:00:00z",
		" 2026-10-19T08:00:00Z",
		"2026-10-19T08:00:00Z\n",
		"2026-10-19T24:00:00Z",
		"2026-12-31T23:59:60Z",
		"",
	} {
		t.Run(stamp, func(t *testing.T) {
			var a allocation
			allocationErr := json.Unmarshal([]byte(`{"startAt":`+strconv.Quote(stamp)+`}`), &a)
			exclude, optOutErr := ExperimentationOptOutImmediate.exclusion(
				map[string]any{experimentationOptOutAtAttribute: stamp},
			)
			assert.Equal(t, allocationErr == nil, optOutErr == nil, "use the existing allocation timestamp contract")
			assert.Equal(t, allocationErr == nil, exclude.mode == ExperimentationOptOutImmediate)
			if optOutErr != nil {
				assert.ErrorIs(t, optOutErr, errInvalidContext)
			}
		})
	}
}

func TestExperimentationOptOutConfiguration(t *testing.T) {
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() {
		internalconfig.SetUseFreshConfig(false)
		internalconfig.CreateNew()
	})
	backend := newFakeUFCBackend(t)
	t.Setenv("DD_FEATURE_FLAGS_ENABLED", "true")
	t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "agentless")
	t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE_AGENTLESS_BASE_URL", backend.server.URL)

	for _, tc := range []struct {
		name string
		env  *string
		mode ExperimentationOptOutMode
		want ExperimentationOptOutMode
		err  bool
	}{
		{name: "unset", want: ExperimentationOptOutDisabled},
		{name: "empty", env: new(""), want: ExperimentationOptOutDisabled},
		{name: "whitespace", env: new(" \t"), want: ExperimentationOptOutDisabled},
		{name: "disabled", env: new("disabled"), want: ExperimentationOptOutDisabled},
		{name: "immediate", env: new("immediate"), want: ExperimentationOptOutImmediate},
		{name: "normalized immediate", env: new(" IMMEDIATE "), want: ExperimentationOptOutImmediate},
		{name: "future experiments", env: new("future_experiments_only"), want: ExperimentationOptOutFutureExperimentsOnly},
		{name: "normalized future experiments", env: new(" FUTURE_EXPERIMENTS_ONLY "), want: ExperimentationOptOutFutureExperimentsOnly},
		{name: "explicit future experiments", mode: ExperimentationOptOutFutureExperimentsOnly, want: ExperimentationOptOutFutureExperimentsOnly},
		{name: "future overrides immediate", env: new("immediate"), mode: ExperimentationOptOutFutureExperimentsOnly, want: ExperimentationOptOutFutureExperimentsOnly},
		{name: "immediate overrides future", env: new("future_experiments_only"), mode: ExperimentationOptOutImmediate, want: ExperimentationOptOutImmediate},
		{name: "disabled overrides future", env: new("future_experiments_only"), mode: ExperimentationOptOutDisabled, want: ExperimentationOptOutDisabled},
		{name: "future overrides invalid", env: new("private-invalid-value"), mode: ExperimentationOptOutFutureExperimentsOnly, want: ExperimentationOptOutFutureExperimentsOnly},
		{name: "unsupported environment", env: new("private-invalid-value"), err: true},
		{name: "explicit disabled", mode: ExperimentationOptOutDisabled, want: ExperimentationOptOutDisabled},
		{name: "explicit immediate", mode: ExperimentationOptOutImmediate, want: ExperimentationOptOutImmediate},
		{name: "disabled overrides environment", env: new("immediate"), mode: ExperimentationOptOutDisabled, want: ExperimentationOptOutDisabled},
		{name: "immediate overrides environment", env: new("disabled"), mode: ExperimentationOptOutImmediate, want: ExperimentationOptOutImmediate},
		{name: "disabled overrides invalid environment", env: new("private-invalid-value"), mode: ExperimentationOptOutDisabled, want: ExperimentationOptOutDisabled},
		{name: "immediate overrides invalid environment", env: new("private-invalid-value"), mode: ExperimentationOptOutImmediate, want: ExperimentationOptOutImmediate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const key = "DD_FEATURE_FLAGS_EXPERIMENTATION_OPT_OUT_MODE"
			t.Setenv(key, "")
			if tc.env == nil {
				require.NoError(t, os.Unsetenv(key))
			} else {
				t.Setenv(key, *tc.env)
			}
			provider, err := NewDatadogProvider(ProviderConfig{ExperimentationOptOutMode: tc.mode})
			if tc.err {
				require.ErrorContains(t, err, key+" must be disabled, immediate, or future_experiments_only")
				assert.NotContains(t, err.Error(), "private-invalid-value")
				assert.Nil(t, provider)
				return
			}
			require.NoError(t, err)
			p := provider.(*DatadogProvider)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				require.NoError(t, p.ShutdownWithContext(ctx))
			})
			assert.Equal(t, tc.want, p.experimentationOptOutMode)

			// An existing provider keeps its resolved setting if central configuration changes.
			t.Setenv(key, "immediate")
			if tc.want == ExperimentationOptOutImmediate {
				t.Setenv(key, "disabled")
			}
			internalconfig.CreateNew()
			assert.Equal(t, tc.want, p.experimentationOptOutMode)
			exclude, err := p.experimentationOptOutMode.exclusion(map[string]any{
				experimentationOptOutAtAttribute: experimentOptOutTestTime,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.want != ExperimentationOptOutDisabled, exclude.active())
		})
	}
}

func TestExperimentationOptOutAllocations(t *testing.T) {
	for _, logging := range []struct {
		name  string
		doLog *bool
	}{
		{"explicit doLog", new(true)},
		{"default doLog", nil},
	} {
		t.Run(logging.name, func(t *testing.T) {
			for _, tc := range []struct {
				name     string
				rules    []*rule
				disabled bool
				value    bool
				reason   of.Reason
				variant  string
			}{
				{name: "ordinary fallback", value: true, reason: of.StaticReason, variant: "ordinary"},
				{name: "fallback targeting does not match", reason: of.DefaultReason,
					rules: []*rule{{Conditions: []*condition{{Attribute: "beta", Operator: operatorOneOf, Value: []string{"true"}}}}}},
				{name: "disabled flag", disabled: true, reason: of.DisabledReason},
			} {
				t.Run(tc.name, func(t *testing.T) {
					h := newOptOutHarness(t, ProviderConfig{ExperimentationOptOutMode: ExperimentationOptOutImmediate})
					f := h.provider.configuration.Flags["experiment"]
					f.Enabled = !tc.disabled
					f.Allocations[0].DoLog = logging.doLog
					f.Variations["ordinary"] = &variant{Key: "ordinary", Value: true}
					f.Allocations = append(f.Allocations, &allocation{Key: "ordinary", DoLog: new(false),
						Rules: tc.rules, Splits: []*split{{VariationKey: "ordinary"}}})
					ctx := of.NewEvaluationContext("subject", map[string]any{experimentationOptOutAtAttribute: experimentOptOutTestTime})
					result, err := h.client.BooleanValueDetails(context.Background(), "experiment", false, ctx)
					require.NoError(t, err)
					assert.Equal(t, tc.value, result.Value)
					assert.Equal(t, tc.reason, result.Reason)
					assert.Equal(t, tc.variant, result.Variant)
					if tc.variant != "" {
						assert.Equal(t, tc.variant, result.FlagMetadata[metadataAllocationKey])
						assert.Equal(t, false, result.FlagMetadata[metadataDoLogKey])
					}
					h.provider.exposureWriter.flush()
					assert.Empty(t, h.sink.exposures)
				})
			}
		})
	}
}

func TestExperimentationOptOutIndependentTelemetry(t *testing.T) {
	for _, tc := range []struct {
		name              string
		suppressTelemetry bool
		evaluationCount   int64
	}{
		{"evaluation telemetry enabled", false, 2},
		{"evaluation telemetry suppressed", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newOptOutHarness(t, ProviderConfig{
				ExperimentationOptOutMode:                ExperimentationOptOutImmediate,
				EnableEvaluationTelemetryOptOutParameter: new(true),
			})
			// Enrollment still produces an ordinary exposure.
			h.evaluate(t, "subject", nil)
			result, err := h.client.BooleanValueDetails(context.Background(), "experiment", false,
				of.NewEvaluationContext("subject", map[string]any{
					experimentationOptOutAtAttribute: experimentOptOutTestTime,
					optOutTestParameter:              tc.suppressTelemetry,
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
			assert.Equal(t, tc.evaluationCount, count, "evaluation telemetry remains a separate control")
		})
	}
}

func TestExperimentationOptOutInvalidContext(t *testing.T) {
	for _, flagCase := range []struct {
		name  string
		doLog bool
	}{
		{"experiment", true},
		{"ordinary", false},
	} {
		t.Run(flagCase.name, func(t *testing.T) {
			for _, input := range []struct {
				name  string
				value any
			}{
				{"malformed", "private-invalid-value"},
				{"empty", ""},
				{"boolean true", true},
				{"boolean false", false},
				{"null", nil},
				{"number", 1792411200},
				{"object", map[string]any{"timestamp": experimentOptOutTestTime}},
				{"array", []any{experimentOptOutTestTime}},
			} {
				t.Run(input.name, func(t *testing.T) {
					h := newOptOutHarness(t, ProviderConfig{ExperimentationOptOutMode: ExperimentationOptOutImmediate})
					f := h.provider.configuration.Flags["experiment"]
					f.Allocations[0].DoLog = new(flagCase.doLog)
					// A matching ordinary fallback must not hide invalid context.
					f.Allocations = append(f.Allocations, &allocation{Key: "fallback", DoLog: new(false),
						Splits: []*split{{VariationKey: "on"}}})
					result, err := h.client.BooleanValueDetails(context.Background(), "experiment", false,
						of.NewEvaluationContext("subject", map[string]any{experimentationOptOutAtAttribute: input.value}))
					require.Error(t, err)
					assert.False(t, result.Value)
					assert.Equal(t, of.ErrorReason, result.Reason)
					assert.Equal(t, of.InvalidContextCode, result.ErrorCode)
					assert.Empty(t, result.Variant)
					assert.Contains(t, result.ErrorMessage, "dd_experimentation_opt_out_at must be an RFC3339 timestamp string")
					assert.Contains(t, result.ErrorMessage, "omit the attribute to permit experimentation")
					assert.NotContains(t, result.ErrorMessage, "private-invalid-value")
					h.provider.exposureWriter.flush()
					assert.Empty(t, h.sink.exposures)

					// Correcting the context restores evaluation for the same subject.
					recovered, err := h.client.BooleanValueDetails(context.Background(), "experiment", false,
						of.NewEvaluationContext("subject", nil))
					require.NoError(t, err)
					assert.True(t, recovered.Value)
					assert.Equal(t, "allocation", recovered.FlagMetadata[metadataAllocationKey])
				})
			}
		})
	}
}

func TestExperimentationOptOutDefaultBehavior(t *testing.T) {
	for _, tc := range []struct {
		name       string
		config     ProviderConfig
		attributes map[string]any
	}{
		{name: "unset mode and no attribute"},
		{name: "unset mode ignores timestamp", attributes: map[string]any{experimentationOptOutAtAttribute: experimentOptOutTestTime}},
		{name: "unset mode ignores invalid timestamp", attributes: map[string]any{experimentationOptOutAtAttribute: "invalid"}},
		{name: "immediate mode without attribute", config: ProviderConfig{ExperimentationOptOutMode: ExperimentationOptOutImmediate}},
		{name: "future mode without attribute", config: ProviderConfig{ExperimentationOptOutMode: ExperimentationOptOutFutureExperimentsOnly}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newOptOutHarness(t, tc.config)
			h.evaluate(t, "subject", tc.attributes)
			h.assertCounts(t, 1, 1, 1)
		})
	}
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
	variants := make([]string, 100)
	for i := range 100 {
		wg.Go(func() {
			attrs := of.FlattenedContext{of.TargetingKey: strconv.Itoa(i)}
			baseline := evaluateFlag(f, false, attrs, time.Now(), experimentationExclusion{})
			assert.Contains(t, []string{"on", "off"}, baseline.VariantKey)
			variants[i] = baseline.VariantKey
			eligible := h.provider.BooleanEvaluation(context.Background(), "experiment", false, attrs)
			assert.Equal(t, baseline.Value, eligible.Value)
			assert.Equal(t, baseline.VariantKey, eligible.Variant)
			assert.Equal(t, baseline.Reason, eligible.Reason)
			attrs[experimentationOptOutAtAttribute] = experimentOptOutTestTime
			excluded := h.provider.BooleanEvaluation(context.Background(), "experiment", false, attrs)
			assert.Equal(t, of.DefaultReason, excluded.Reason)
			assert.Empty(t, excluded.Variant)
			delete(attrs, experimentationOptOutAtAttribute)
			reincluded := h.provider.BooleanEvaluation(context.Background(), "experiment", false, attrs)
			assert.Equal(t, baseline.Value, reincluded.Value)
			assert.Equal(t, baseline.VariantKey, reincluded.Variant)
		})
	}
	wg.Wait()
	assert.Contains(t, variants, "on")
	assert.Contains(t, variants, "off")
}

func TestExperimentationOptOutDisabledProviderWinsOverInvalidMode(t *testing.T) {
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() { internalconfig.SetUseFreshConfig(false); internalconfig.CreateNew() })
	for _, tc := range []struct{ name, enabled, source, legacy string }{
		{"kill switch", "false", "remote_config", "true"},
		{"offline", "true", "offline", "true"},
		{"unrecognized source", "true", "unknown", "true"},
		{"legacy disabled", "", "", "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range map[string]string{"DD_FEATURE_FLAGS_ENABLED": tc.enabled, "DD_FEATURE_FLAGS_CONFIGURATION_SOURCE": tc.source, "DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED": tc.legacy} {
				t.Setenv(key, value)
				if value == "" {
					require.NoError(t, os.Unsetenv(key))
				}
			}
			t.Setenv("DD_FEATURE_FLAGS_EXPERIMENTATION_OPT_OUT_MODE", "immediat")
			for _, mode := range []ExperimentationOptOutMode{ExperimentationOptOutDefault, -1} {
				p, err := NewDatadogProvider(ProviderConfig{ExperimentationOptOutMode: mode})
				require.NoError(t, err)
				assert.IsType(t, &of.NoopProvider{}, p)
			}
		})
	}
}

func TestExperimentationOptOutModeString(t *testing.T) {
	for _, tc := range []struct {
		mode ExperimentationOptOutMode
		want string
	}{
		{ExperimentationOptOutDefault, "default"}, {ExperimentationOptOutDisabled, "disabled"},
		{ExperimentationOptOutImmediate, "immediate"}, {-1, "ExperimentationOptOutMode(-1)"}, {99, "ExperimentationOptOutMode(99)"},
		{ExperimentationOptOutFutureExperimentsOnly, "future_experiments_only"},
	} {
		assert.Equal(t, tc.want, tc.mode.String())
	}
}

func TestExperimentationOptOutInvalidErrorReused(t *testing.T) {
	for _, mode := range []ExperimentationOptOutMode{ExperimentationOptOutImmediate, ExperimentationOptOutFutureExperimentsOnly} {
		for _, value := range []any{"private-invalid-value", nil, 42} {
			_, err := mode.exclusion(map[string]any{experimentationOptOutAtAttribute: value})
			require.Same(t, errInvalidExperimentationOptOutAt, err)
			require.ErrorIs(t, err, errInvalidContext)
			require.NotContains(t, err.Error(), "private-invalid-value")
		}
	}
}
