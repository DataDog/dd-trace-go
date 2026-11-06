// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/open-feature/go-sdk/openfeature/isolated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
)

const optOutTestParameter = "dd_evaluation_telemetry_opt_out"

type optOutSink struct {
	mu        sync.Mutex
	events    []flagEvalLoggingEvent
	exposures []exposureEvent
}

func (s *optOutSink) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer r.Body.Close()
	if strings.HasSuffix(r.URL.Path, flagEvalLoggingEndpoint) {
		var payload flagEvalLoggingPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		s.events = append(s.events, payload.FlagEvaluations...)
	} else {
		var payload exposurePayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		s.exposures = append(s.exposures, payload.Exposures...)
	}
	return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
}

type optOutHarness struct {
	provider *DatadogProvider
	client   *of.Client
	sink     *optOutSink
	reader   *metric.ManualReader
	tracer   mocktracer.Tracer
}

func newOptOutHarness(t *testing.T, config ProviderConfig) *optOutHarness {
	t.Helper()
	t.Setenv(spanEnrichmentEnvVar, "true")
	t.Setenv(flagEvalCountsEnabledEnvVar, "true")
	t.Setenv("DD_METRICS_OTEL_ENABLED", "false")
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	sink := &optOutSink{}
	evp := newEVPClient()
	evp.httpClient = &http.Client{Transport: sink}
	p := newDatadogProviderWithSourceAndEVP(config, internalffe.SourceAgentless, evp)
	t.Cleanup(p.Shutdown)
	m, reader := setupTestMetrics(t)
	p.flagEvalMetricsHook.metrics = m
	var flags universalFlagsConfiguration
	require.NoError(t, json.Unmarshal([]byte(`{
		"format":"SERVER","environment":{"name":"test"},
		"flags":{"experiment":{"key":"experiment","enabled":true,"variationType":"BOOLEAN",
		"variations":{"on":{"key":"on","value":true}},
		"allocations":[{"key":"allocation","doLog":true,
		"splits":[{"variationKey":"on","serialId":42,"shards":[]}]}]}}
	}`), &flags))
	// Install configuration before registration. Keep the real writers synchronous
	// here so the test controls the queue drain and observes exact serialized counts.
	p.configuration = &flags
	require.NoError(t, of.SetNamedProviderAndWait(t.Name(), p))
	return &optOutHarness{p, of.NewClient(t.Name()), sink, reader, mt}
}

func (h *optOutHarness) evaluate(t *testing.T, subject string, attrs map[string]any) of.BooleanEvaluationDetails {
	t.Helper()
	span := tracer.StartSpan("evaluation")
	defer span.Finish()
	ctx := tracer.ContextWithSpan(context.Background(), span)
	details, err := h.client.BooleanValueDetails(ctx, "experiment", false, of.NewEvaluationContext(subject, attrs))
	require.NoError(t, err)
	require.True(t, details.Value)
	require.Equal(t, "on", details.Variant)
	require.Equal(t, "allocation", details.FlagMetadata[metadataAllocationKey])
	return details
}

func (h *optOutHarness) assertCounts(t *testing.T, evaluations, exposures, spans int) {
	t.Helper()
	h.provider.flagEvalLoggingWriter.drainAndFlush()
	h.provider.exposureWriter.flush()
	var eventCount int64
	for _, event := range h.sink.events {
		eventCount += event.EvaluationCount
	}
	var metricCount int64
	for _, point := range findCounter(t, collectMetrics(t, h.reader)) {
		metricCount += point.Value
	}
	var enriched int
	for _, span := range h.tracer.FinishedSpans() {
		for key := range span.Tags() {
			if strings.HasPrefix(key, "ffe_") {
				enriched++
				break
			}
		}
	}
	t.Logf("serialized EVP=%d, metric=%d, enriched spans=%d, exposures=%d, total spans=%d", eventCount, metricCount, enriched, len(h.sink.exposures), len(h.tracer.FinishedSpans()))
	assert.Equal(t, int64(evaluations), eventCount, "serialized evaluation count")
	assert.Equal(t, int64(evaluations), metricCount, "evaluation metric count")
	assert.Equal(t, evaluations, enriched, "enriched root span count")
	assert.Len(t, h.sink.exposures, exposures, "exposure count")
	assert.Len(t, h.tracer.FinishedSpans(), spans, "ordinary spans remain")
}

func TestEvaluationTelemetryOptOut(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)})
	details := h.evaluate(t, "opted-out", map[string]any{optOutTestParameter: true})
	assert.Equal(t, of.StaticReason, details.Reason)
	h.assertCounts(t, 0, 1, 1)
	assert.Equal(t, true, h.sink.exposures[0].Subject.Attributes[optOutTestParameter])
}

func TestEvaluationTelemetryOptOutRemainsTargetingAttribute(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(enabled)})
			h.provider.configuration.Flags["experiment"].Allocations[0].Rules = []*rule{{
				Conditions: []*condition{{Attribute: optOutTestParameter, Operator: operatorOneOf, Value: []string{"true"}}},
			}}
			h.evaluate(t, "opted-out", map[string]any{optOutTestParameter: true})
			want := 1
			if enabled {
				want = 0
			}
			h.assertCounts(t, want, 1, 1)
			assert.Equal(t, true, h.sink.exposures[0].Subject.Attributes[optOutTestParameter])
			result, err := h.client.BooleanValueDetails(context.Background(), "experiment", false,
				of.NewEvaluationContext("not-opted-out", map[string]any{optOutTestParameter: false}))
			require.NoError(t, err)
			assert.False(t, result.Value)
			assert.NotEqual(t, "allocation", result.FlagMetadata[metadataAllocationKey])
		})
	}
}

func TestEvaluationTelemetryOptOutEnvironment(t *testing.T) {
	const key = "DD_FEATURE_FLAGS_EVALUATION_TELEMETRY_PER_SUBJECT_OPT_OUT_ENABLED"
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() {
		internalconfig.SetUseFreshConfig(false)
		internalconfig.CreateNew()
	})
	for _, env := range []struct {
		name  string
		value *string
	}{
		{"unset", nil},
		{"empty", new("")},
		{"true", new("true")},
		{"false", new("false")},
		{"invalid", new("invalid")},
	} {
		for _, option := range []struct {
			name  string
			value *bool
		}{
			{"inherit", nil},
			{"enable", new(true)},
			{"disable", new(false)},
		} {
			t.Run(env.name+"/"+option.name, func(t *testing.T) {
				t.Setenv(key, "")
				if env.value == nil {
					require.NoError(t, os.Unsetenv(key))
				} else {
					t.Setenv(key, *env.value)
				}
				enabled := env.value != nil && *env.value == "true"
				if option.value != nil {
					enabled = *option.value
				}
				h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: option.value})
				optedOut := h.evaluate(t, "opted-out", map[string]any{optOutTestParameter: true})
				allowed := h.evaluate(t, "allowed", map[string]any{optOutTestParameter: false})
				h.evaluate(t, "missing", nil)
				h.evaluate(t, "string", map[string]any{optOutTestParameter: "true"})
				assert.Equal(t, optedOut.Value, allowed.Value)
				assert.Equal(t, optedOut.Variant, allowed.Variant)
				assert.Equal(t, optedOut.Reason, allowed.Reason)
				want := 4
				if enabled {
					want = 3
				}
				h.assertCounts(t, want, 4, 4)
			})
		}
	}
}

func TestEvaluationTelemetryOptOutConfigurationSnapshot(t *testing.T) {
	const key = "DD_FEATURE_FLAGS_EVALUATION_TELEMETRY_PER_SUBJECT_OPT_OUT_ENABLED"
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() {
		internalconfig.SetUseFreshConfig(false)
		internalconfig.CreateNew()
	})
	for _, code := range []bool{false, true} {
		for _, initial := range []bool{false, true} {
			t.Run(fmt.Sprintf("code=%t/initial=%t", code, initial), func(t *testing.T) {
				t.Setenv(key, strconv.FormatBool(initial))
				option := new(initial)
				config := ProviderConfig{}
				if code {
					config.EnableEvaluationTelemetryOptOutParameter = option
				}
				h := newOptOutHarness(t, config)
				t.Setenv(key, strconv.FormatBool(!initial))
				*option = !initial
				internalconfig.CreateNew()
				h.evaluate(t, "opted-out", map[string]any{optOutTestParameter: true})
				h.evaluate(t, "allowed", nil)
				want := 2
				if initial {
					want = 1
				}
				h.assertCounts(t, want, 2, 2)
			})
		}
	}
}

func TestEvaluationTelemetryOptOutStrictBoolean(t *testing.T) {
	tests := []struct {
		name  string
		attrs map[string]any
		want  int
	}{
		{"true", map[string]any{optOutTestParameter: true}, 0},
		{"false", map[string]any{optOutTestParameter: false}, 1},
		{"missing", nil, 1},
		{"null", map[string]any{optOutTestParameter: nil}, 1},
		{"string", map[string]any{optOutTestParameter: "true"}, 1},
		{"integer", map[string]any{optOutTestParameter: 1}, 1},
		{"float", map[string]any{optOutTestParameter: 1.0}, 1},
		{"pointer", map[string]any{optOutTestParameter: new(true)}, 1},
		{"list", map[string]any{optOutTestParameter: []any{true}}, 1},
		{"object", map[string]any{optOutTestParameter: map[string]any{"value": true}}, 1},
		{"old key", map[string]any{"dd_telemetry_opt_out": true}, 1},
		{"case sensitive", map[string]any{"DD_EVALUATION_TELEMETRY_OPT_OUT": true}, 1},
	}
	for _, full := range []bool{false, true} {
		for _, tc := range tests {
			t.Run(fmt.Sprintf("full=%t/%s", full, tc.name), func(t *testing.T) {
				h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)})
				h.provider.configuration.ObserveFullEvaluationData = full
				attrs := map[string]any{"test": "context"}
				maps.Copy(attrs, tc.attrs)
				h.evaluate(t, "subject", attrs)
				h.assertCounts(t, tc.want, 1, 1)
				if tc.want > 0 {
					require.Len(t, h.sink.events, 1)
					if full {
						assert.Equal(t, "subject", h.sink.events[0].TargetingKey)
						assert.NotNil(t, h.sink.events[0].Context)
					} else {
						assert.NotEqual(t, "subject", h.sink.events[0].TargetingKey)
						assert.Nil(t, h.sink.events[0].Context)
					}
				}
			})
		}
	}
}

func TestEvaluationTelemetryOptOutDisabledByDefault(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	h.evaluate(t, "subject", map[string]any{optOutTestParameter: true})
	h.assertCounts(t, 1, 1, 1)
}

func TestEvaluationTelemetryOptOutIsNotRememberedForSubject(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)})
	h.evaluate(t, "subject", map[string]any{optOutTestParameter: true})
	h.assertCounts(t, 0, 1, 1)
	h.evaluate(t, "subject", nil)
	h.assertCounts(t, 1, 1, 2)
}

func TestEvaluationTelemetryOptOutTransactionContext(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)})
	span := tracer.StartSpan("transaction")
	ctx := of.WithTransactionContext(tracer.ContextWithSpan(context.Background(), span),
		of.NewEvaluationContext("subject", map[string]any{optOutTestParameter: true}))
	value, err := h.client.BooleanValue(ctx, "experiment", false, of.EvaluationContext{})
	span.Finish()
	require.NoError(t, err)
	require.True(t, value)
	h.assertCounts(t, 0, 1, 1)
	assert.Equal(t, "subject", h.sink.exposures[0].Subject.ID)
}

func TestEvaluationTelemetryOptOutProviderNotReady(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)})
	p := newDatadogProviderWithSourceAndEVP(ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)},
		internalffe.SourceAgentless, h.provider.exposureWriter.evp)
	// Shutdown also releases initialization while it waits for configuration.
	t.Cleanup(p.Shutdown)
	p.flagEvalMetricsHook.metrics = h.provider.flagEvalMetricsHook.metrics
	var beforeCalls int
	p.hooks = append(p.hooks, optOutTestHook{before: func(context.Context, of.HookContext) (*of.EvaluationContext, error) {
		beforeCalls++
		return nil, nil
	}})
	// Named domains can inherit the global default provider's READY state.
	// A fresh API keeps this test independent from other provider registrations.
	api := isolated.NewAPI()
	t.Cleanup(func() { require.NoError(t, api.Shutdown(context.Background())) })
	require.NoError(t, api.SetProvider(context.Background(), p))
	client := api.NewClient()
	require.Equal(t, of.NotReadyState, client.State())
	for _, optedOut := range []bool{true, false} {
		span := tracer.StartSpan("not-ready")
		_, err := client.BooleanValue(tracer.ContextWithSpan(context.Background(), span), "experiment", false,
			of.NewEvaluationContext("subject", map[string]any{optOutTestParameter: optedOut}))
		span.Finish()
		require.ErrorIs(t, err, of.ProviderNotReadyError)
	}
	require.Zero(t, beforeCalls, "the SDK must skip Before hooks on its not-ready path")
	p.flagEvalLoggingWriter.drainAndFlush()
	h.assertCounts(t, 1, 0, 2)
}

// The gate delegates Before, After, and Error unchanged. Guard the assumption
// that each wrapped telemetry producer emits only in Finally.
func TestEvaluationTelemetryOptOutHooksEmitOnlyInFinally(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)})
	hookContext := makeHookContext("experiment", "subject", nil)
	details := makeEvalDetails("on", of.StaticReason, "", of.FlagMetadata{
		metadataSplitSerialIDKey: uint32(42),
		metadataDoLogKey:         true,
	})
	details.FlagKey = "experiment"
	details.Value = true
	var hooks []of.Hook
	for _, hook := range h.provider.hooks {
		if gate, ok := hook.(evaluationTelemetryOptOutHook); ok {
			hooks = append(hooks, gate.Hook)
		}
	}
	require.Len(t, hooks, 3)
	span := tracer.StartSpan("other-hook-stages")
	ctx := tracer.ContextWithSpan(context.Background(), span)
	for _, hook := range hooks {
		_, err := hook.Before(ctx, hookContext, of.HookHints{})
		require.NoError(t, err)
		require.NoError(t, hook.After(ctx, hookContext, details, of.HookHints{}))
		hook.Error(ctx, hookContext, errors.New("evaluation failed"), of.HookHints{})
	}
	span.Finish()
	h.assertCounts(t, 0, 0, 1)
	span = tracer.StartSpan("finally-hook-stage")
	ctx = tracer.ContextWithSpan(context.Background(), span)
	for _, hook := range hooks {
		hook.Finally(ctx, hookContext, details, of.HookHints{})
	}
	span.Finish()
	h.assertCounts(t, 1, 0, 2)
}

func TestEvaluationTelemetryOptOutPreservesQueuedDataAndDeduplication(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)})
	first := h.evaluate(t, "subject", nil)
	second := h.evaluate(t, "subject", map[string]any{optOutTestParameter: true})
	third := h.evaluate(t, "subject", map[string]any{optOutTestParameter: false})
	assert.Equal(t, first.Value, second.Value)
	assert.Equal(t, first.Variant, second.Variant)
	assert.Equal(t, first.Reason, second.Reason)
	assert.Equal(t, first.Value, third.Value)
	h.assertCounts(t, 2, 1, 3)
}

type optOutTestHook struct {
	of.UnimplementedHook
	before  func(context.Context, of.HookContext) (*of.EvaluationContext, error)
	after   func(context.Context, of.HookContext) error
	onError func(of.HookContext)
	finally func(of.HookContext)
}

func (h optOutTestHook) Before(ctx context.Context, hc of.HookContext, _ of.HookHints) (*of.EvaluationContext, error) {
	if h.before != nil {
		return h.before(ctx, hc)
	}
	return nil, nil
}

func (h optOutTestHook) After(ctx context.Context, hc of.HookContext, _ of.InterfaceEvaluationDetails, _ of.HookHints) error {
	if h.after != nil {
		return h.after(ctx, hc)
	}
	return nil
}

func (h optOutTestHook) Error(_ context.Context, hc of.HookContext, _ error, _ of.HookHints) {
	if h.onError != nil {
		h.onError(hc)
	}
}

func (h optOutTestHook) Finally(_ context.Context, hc of.HookContext, _ of.InterfaceEvaluationDetails, _ of.HookHints) {
	if h.finally != nil {
		h.finally(hc)
	}
}

func TestEvaluationTelemetryOptOutContextPrecedence(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)})
	of.SetEvaluationContext(of.NewEvaluationContext("", map[string]any{optOutTestParameter: true}))
	t.Cleanup(func() { of.SetEvaluationContext(of.EvaluationContext{}) })
	h.evaluate(t, "global", nil)
	h.client.SetEvaluationContext(of.NewEvaluationContext("", map[string]any{optOutTestParameter: false}))
	h.evaluate(t, "client", nil)
	h.evaluate(t, "invocation", map[string]any{optOutTestParameter: true})
	h.client.SetEvaluationContext(of.EvaluationContext{})
	span := tracer.StartSpan("transaction")
	ctx := of.WithTransactionContext(tracer.ContextWithSpan(context.Background(), span), of.NewEvaluationContext("", map[string]any{optOutTestParameter: false}))
	value, err := h.client.BooleanValue(ctx, "experiment", false, of.NewEvaluationContext("transaction", nil))
	span.Finish()
	require.NoError(t, err)
	require.True(t, value)
	h.assertCounts(t, 2, 4, 4)
}

func TestEvaluationTelemetryOptOutBeforeHooks(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%t", partial), func(t *testing.T) {
			h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)})
			var after, finally int
			h.client.AddHooks(optOutTestHook{
				before: func(_ context.Context, _ of.HookContext) (*of.EvaluationContext, error) {
					attrs := map[string]any{optOutTestParameter: true}
					if partial {
						attrs = map[string]any{"other": "value"}
					}
					ec := of.NewEvaluationContext("", attrs)
					return &ec, nil
				},
				after: func(_ context.Context, hc of.HookContext) error {
					after++
					// Attributes returns a copy; application hooks cannot undo the decision.
					hc.EvaluationContext().Attributes()[optOutTestParameter] = false
					return nil
				},
				finally: func(of.HookContext) { finally++ },
			})
			attrs := map[string]any{}
			if partial {
				attrs[optOutTestParameter] = true
			}
			h.evaluate(t, "subject", attrs)
			h.assertCounts(t, 0, 1, 1)
			assert.Equal(t, 1, after)
			assert.Equal(t, 1, finally)
		})
	}
}

func TestEvaluationTelemetryOptOutHookErrors(t *testing.T) {
	for _, stage := range []string{"before", "after"} {
		t.Run(stage, func(t *testing.T) {
			h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)})
			var errorCalls, finallyCalls int
			hook := optOutTestHook{
				onError: func(hc of.HookContext) {
					errorCalls++
					hc.EvaluationContext().Attributes()[optOutTestParameter] = false
				},
				finally: func(of.HookContext) { finallyCalls++ },
			}
			if stage == "before" {
				hook.before = func(_ context.Context, _ of.HookContext) (*of.EvaluationContext, error) {
					ec := of.NewEvaluationContext("", map[string]any{optOutTestParameter: true})
					return &ec, errors.New("application before hook")
				}
			} else {
				hook.after = func(context.Context, of.HookContext) error { return errors.New("application after hook") }
			}
			h.client.AddHooks(hook)
			span := tracer.StartSpan("hook-error")
			// Only the failing Before hook's returned context can opt out this arm.
			_, err := h.client.BooleanValue(tracer.ContextWithSpan(context.Background(), span), "experiment", false, of.NewEvaluationContext("subject", map[string]any{optOutTestParameter: stage != "before"}))
			span.Finish()
			require.Error(t, err)
			h.assertCounts(t, 0, 0, 1)
			assert.Equal(t, 1, errorCalls)
			assert.Equal(t, 1, finallyCalls)
		})
	}
}

func TestEvaluationTelemetryOptOutNested(t *testing.T) {
	for _, outerOptOut := range []bool{false, true} {
		t.Run(fmt.Sprintf("outer=%t", outerOptOut), func(t *testing.T) {
			h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)})
			h.client.AddHooks(optOutTestHook{after: func(_ context.Context, hc of.HookContext) error {
				if hc.EvaluationContext().TargetingKey() == "outer" {
					h.evaluate(t, "inner", map[string]any{optOutTestParameter: !outerOptOut})
				}
				return nil
			}})
			h.evaluate(t, "outer", map[string]any{optOutTestParameter: outerOptOut})
			h.assertCounts(t, 1, 2, 2)
		})
	}
}

func TestEvaluationTelemetryOptOutConcurrent(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)})
	const count = 100
	var entered, finished sync.WaitGroup
	entered.Add(count)
	finished.Add(count)
	h.client.AddHooks(optOutTestHook{after: func(context.Context, of.HookContext) error {
		entered.Done()
		entered.Wait()
		return nil
	}})
	errors := make(chan error, count)
	for i := range count {
		go func() {
			defer finished.Done()
			span := tracer.StartSpan("concurrent")
			defer span.Finish()
			details, err := h.client.BooleanValueDetails(tracer.ContextWithSpan(context.Background(), span), "experiment", false,
				of.NewEvaluationContext(fmt.Sprintf("subject-%d", i), map[string]any{optOutTestParameter: i%2 == 0}))
			if err != nil {
				errors <- err
				return
			}
			if !details.Value || details.Variant != "on" || details.Reason != of.StaticReason {
				errors <- fmt.Errorf("unexpected evaluation: %+v", details)
			}
		}()
	}
	finished.Wait()
	close(errors)
	for err := range errors {
		assert.NoError(t, err)
	}
	h.assertCounts(t, count/2, count, count)
}

func checkOptOutTypedEvaluation[T any](t *testing.T, kind valueType, value, fallback T, evaluate func(*of.Client, context.Context, string, T, of.EvaluationContext, ...of.Option) (of.GenericEvaluationDetails[T], error)) {
	t.Helper()
	h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)})
	flag := h.provider.configuration.Flags["experiment"]
	flag.VariationType = kind
	flag.Variations["on"].Value = value
	for _, optedOut := range []bool{false, true} {
		span := tracer.StartSpan("typed-evaluation")
		details, err := evaluate(h.client, tracer.ContextWithSpan(context.Background(), span), "experiment", fallback,
			of.NewEvaluationContext(fmt.Sprintf("subject-%t", optedOut), map[string]any{optOutTestParameter: optedOut}))
		span.Finish()
		require.NoError(t, err)
		assert.Equal(t, value, details.Value)
		assert.Equal(t, "on", details.Variant)
		assert.Equal(t, of.StaticReason, details.Reason)
		assert.Equal(t, "allocation", details.FlagMetadata[metadataAllocationKey])
	}
	h.assertCounts(t, 1, 2, 2)
}

func TestEvaluationTelemetryOptOutAllTypes(t *testing.T) {
	t.Run("boolean", func(t *testing.T) {
		checkOptOutTypedEvaluation(t, valueTypeBoolean, true, false, (*of.Client).BooleanValueDetails)
	})
	t.Run("string", func(t *testing.T) {
		checkOptOutTypedEvaluation(t, valueTypeString, "enabled", "default", (*of.Client).StringValueDetails)
	})
	t.Run("integer", func(t *testing.T) {
		checkOptOutTypedEvaluation(t, valueTypeInteger, int64(42), int64(0), (*of.Client).IntValueDetails)
	})
	t.Run("float", func(t *testing.T) {
		checkOptOutTypedEvaluation(t, valueTypeNumeric, 1.5, 0.0, (*of.Client).FloatValueDetails)
	})
	t.Run("object", func(t *testing.T) {
		checkOptOutTypedEvaluation[any](t, valueTypeJSON, map[string]any{"enabled": true}, nil, (*of.Client).ObjectValueDetails)
	})
}

func TestEvaluationTelemetryOptOutErrorsAndDefaults(t *testing.T) {
	for _, name := range []string{"missing flag", "wrong type", "missing configuration", "cancelled context", "disabled flag", "no allocation"} {
		for _, optedOut := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/optOut=%t", name, optedOut), func(t *testing.T) {
				h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)})
				span := tracer.StartSpan("default-evaluation")
				ctx := tracer.ContextWithSpan(context.Background(), span)
				key := "experiment"
				switch name {
				case "missing flag":
					key = "missing"
				case "wrong type":
					h.provider.configuration.Flags[key].Variations["on"].Value = "wrong"
				case "missing configuration":
					h.provider.configuration = nil
				case "cancelled context":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				case "disabled flag":
					h.provider.configuration.Flags[key].Enabled = false
				case "no allocation":
					h.provider.configuration.Flags[key].Allocations = nil
				}
				details, err := h.client.BooleanValueDetails(ctx, key, false, of.NewEvaluationContext("subject", map[string]any{optOutTestParameter: optedOut}))
				span.Finish()
				assert.False(t, details.Value, "caller default")
				switch name {
				case "disabled flag":
					require.NoError(t, err)
					assert.Equal(t, of.DisabledReason, details.Reason)
				case "no allocation":
					require.NoError(t, err)
					assert.Equal(t, of.DefaultReason, details.Reason)
				default:
					require.Error(t, err)
					assert.Equal(t, of.ErrorReason, details.Reason)
				}
				want := 1
				if optedOut {
					want = 0
				}
				h.assertCounts(t, want, 0, 1)
			})
		}
	}
}

func TestEvaluationTelemetryOptOutCallerMutation(t *testing.T) {
	for _, initial := range []bool{false, true} {
		t.Run(fmt.Sprintf("initial=%t", initial), func(t *testing.T) {
			h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)})
			attrs := map[string]any{optOutTestParameter: initial}
			ec := of.NewEvaluationContext("subject", attrs)
			h.client.AddHooks(optOutTestHook{after: func(context.Context, of.HookContext) error {
				attrs[optOutTestParameter] = !initial
				return nil
			}})
			span := tracer.StartSpan("mutation")
			value, err := h.client.BooleanValue(tracer.ContextWithSpan(context.Background(), span), "experiment", false, ec)
			span.Finish()
			require.NoError(t, err)
			assert.True(t, value)
			want := 1
			if initial {
				want = 0
			}
			h.assertCounts(t, want, 1, 1)
		})
	}
}

func TestEvaluationTelemetryOptOutIndependentProviders(t *testing.T) {
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() {
		internalconfig.SetUseFreshConfig(false)
		internalconfig.CreateNew()
	})
	t.Setenv("DD_FEATURE_FLAGS_EVALUATION_TELEMETRY_PER_SUBJECT_OPT_OUT_ENABLED", "true")
	h := newOptOutHarness(t, ProviderConfig{})
	second := newDatadogProviderWithSourceAndEVP(ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(false)}, internalffe.SourceAgentless, h.provider.exposureWriter.evp)
	t.Cleanup(second.Shutdown)
	second.configuration = h.provider.configuration
	second.flagEvalMetricsHook.metrics = h.provider.flagEvalMetricsHook.metrics
	require.NoError(t, of.SetNamedProviderAndWait(t.Name()+"/disabled", second))
	h.evaluate(t, "enabled", map[string]any{optOutTestParameter: true})
	h.client = of.NewClient(t.Name() + "/disabled")
	h.evaluate(t, "disabled", map[string]any{optOutTestParameter: true})
	second.flagEvalLoggingWriter.drainAndFlush()
	second.exposureWriter.flush()
	h.assertCounts(t, 1, 2, 2)
}
