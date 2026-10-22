// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testtracer_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/testtracer"
	"github.com/DataDog/dd-trace-go/v2/llmobs"
)

// startLLMObs mirrors the start pattern of the dd-source suites that use this
// package: LLMObs enabled, an ML app set, and the agent info response that the
// old package needed to open the LLMObs agent gate.
func startLLMObs(t testing.TB, opts ...testtracer.Option) *testtracer.TestTracer {
	t.Helper()
	opts = append([]testtracer.Option{
		testtracer.WithTracerStartOpts(
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp("testtracer-app"),
			tracer.WithLogStartup(false),
		),
		testtracer.WithAgentInfoResponse(testtracer.AgentInfo{Endpoints: []string{"/evp_proxy/v2/"}}),
	}, opts...)
	tt := testtracer.Start(t, opts...)
	t.Cleanup(tt.Stop)
	return tt
}

func TestLLMObsToolSpan(t *testing.T) {
	tt := startLLMObs(t)

	span, _ := llmobs.StartToolSpan(context.Background(), "get_weather")
	span.Finish()

	spans := tt.WaitForLLMObsSpans(t, 1)
	require.Len(t, spans, 1)
	assert.Equal(t, "get_weather", spans[0].Name)
	assert.Equal(t, "tool", spans[0].Meta["span.kind"])
}

func TestAPMSpans(t *testing.T) {
	tt := testtracer.Start(t)
	t.Cleanup(tt.Stop)

	span := tracer.StartSpan("source.span", tracer.WithTags(map[string]any{
		"scorer.name":          "test-scorer",
		"runtime_signals.keys": 1,
	}))
	span.Finish()

	var found bool
	for _, span := range tt.WaitForSpans(t, 1) {
		if span.Name != "source.span" {
			continue
		}
		found = true
		assert.Equal(t, "test-scorer", span.Meta["scorer.name"])
		assert.Equal(t, float64(1), span.Metrics["runtime_signals.keys"])
	}
	assert.True(t, found, "span %q not found among captured spans", "source.span")
}

func TestEvaluationMetrics(t *testing.T) {
	tt := startLLMObs(t)

	span, _ := llmobs.StartLLMSpan(context.Background(), "test-eval-span")
	span.Finish()
	llmobs.SubmitEvaluationFromSpan("accuracy", "correct", span)

	metrics := tt.WaitForLLMObsMetrics(t, 1)
	require.Len(t, metrics, 1)
	assert.Equal(t, "accuracy", metrics[0].Label)
	require.NotNil(t, metrics[0].CategoricalValue)
	assert.Equal(t, "correct", *metrics[0].CategoricalValue)
}

func TestWaitForAndSentPayloads(t *testing.T) {
	tt := startLLMObs(t)

	for _, name := range []string{"first", "second"} {
		span, _ := llmobs.StartTaskSpan(context.Background(), name)
		span.Finish()
	}

	p := tt.WaitFor(t, 5*time.Second, func(p *testtracer.Payloads) bool {
		return len(p.LLMSpans) >= 2
	})
	require.NotNil(t, p)

	sent := tt.SentPayloads()
	require.Len(t, sent.LLMSpans, 2)
	// Each LLMObs span wraps an APM span, so the agent collected two APM spans.
	require.Len(t, sent.Spans, 2)
}

func TestStartError(t *testing.T) {
	// LLMObs enabled with an empty ML app makes the tracer start fail. The
	// explicit empty value overrides DD_LLMOBS_ML_APP when the test
	// environment sets it, so the error path stays deterministic.
	tt := testtracer.Start(t,
		testtracer.WithTracerStartOpts(
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp(""),
			tracer.WithLogStartup(false),
		),
		testtracer.WithRequireNoTracerStartError(false),
	)
	t.Cleanup(tt.Stop)
	require.Error(t, tt.StartError())
}

func TestRequestDelay(t *testing.T) {
	tt := startLLMObs(t, testtracer.WithRequestDelay(10*time.Millisecond))

	span, _ := llmobs.StartToolSpan(context.Background(), "delayed_tool")
	span.Finish()

	spans := tt.WaitForLLMObsSpans(t, 1)
	require.Len(t, spans, 1)
	assert.Equal(t, "delayed_tool", spans[0].Name)
}

// findToolSpan mirrors the dd-source helpers that address spans by name. It
// holds the type positions that dd-source suites use, so a signature change in
// this package fails here instead of in the consumer.
func findToolSpan(spans []testtracer.LLMObsSpan, name string) *testtracer.LLMObsSpan {
	for i := range spans {
		if spans[i].Name == name {
			return &spans[i]
		}
	}
	return nil
}

// withDefaults mirrors the dd-source helpers that append options to a default
// set before starting the tracer.
func withDefaults(opts ...testtracer.Option) []testtracer.Option {
	return append([]testtracer.Option{
		testtracer.WithAgentInfoResponse(testtracer.AgentInfo{Endpoints: []string{"/evp_proxy/v2/"}}),
	}, opts...)
}

func TestSourceCompat(t *testing.T) {
	// Start with a defaulted option slice, the way dd-source helpers do.
	tt := startLLMObs(t, withDefaults(testtracer.WithRequestDelay(0))...)

	span, _ := llmobs.StartToolSpan(context.Background(), "compat_tool")
	span.Finish()

	toolSpan := findToolSpan(tt.WaitForLLMObsSpans(t, 1), "compat_tool")
	require.NotNil(t, toolSpan)
	assert.Equal(t, "tool", toolSpan.Meta["span.kind"])
	// Field reads that dd-source suites make on the captured spans.
	assert.NotEqual(t, "error", toolSpan.Status)
	assert.Contains(t, toolSpan.Tags, "ml_app:testtracer-app")
	assert.Empty(t, toolSpan.SessionID)
	sent := tt.SentPayloads()
	require.Len(t, sent.LLMSpans, 1)
	assert.Equal(t, "compat_tool", sent.LLMSpans[0].Name)
}

// TestWaitForTimesOutDespiteRequestDelay verifies that a slow transport does
// not stall the timeout: WaitFor fails at its deadline while the flush is
// still answering the delayed request. The cleanup that Start registered
// waits for the in-flight flush.
func TestWaitForTimesOutDespiteRequestDelay(t *testing.T) {
	tt := startLLMObs(t, testtracer.WithRequestDelay(2*time.Second))

	// Buffer a span so the flush actually sends a request the delay slows.
	span, _ := llmobs.StartToolSpan(context.Background(), "delayed_tool")
	span.Finish()

	done := make(chan struct{})
	start := time.Now()
	fresh := new(testing.T)
	go func() {
		defer close(done)
		tt.WaitFor(fresh, 200*time.Millisecond, func(*testtracer.Payloads) bool {
			return false
		})
	}()
	<-done
	elapsed := time.Since(start)

	assert.True(t, fresh.Failed(), "WaitFor must fail the test on timeout")
	assert.Less(t, elapsed, 1500*time.Millisecond,
		"WaitFor must fail at its deadline, not when the delayed flush returns")
}

// TestWaitForRejectsConditionAfterDeadline verifies that a condition the
// deadline overtook does not count as success: WaitFor must fail at its
// timeout even when cond itself reports true once it finally runs.
func TestWaitForRejectsConditionAfterDeadline(t *testing.T) {
	tt := startLLMObs(t)

	done := make(chan struct{})
	fresh := new(testing.T)
	go func() {
		defer close(done)
		tt.WaitFor(fresh, 200*time.Millisecond, func(*testtracer.Payloads) bool {
			// Run past the deadline, then claim success.
			time.Sleep(500 * time.Millisecond)
			return true
		})
	}()
	<-done
	assert.True(t, fresh.Failed(), "WaitFor must reject a condition that the deadline overtook")
}

// TestWaitForFlushesBeforeAccepting verifies that WaitFor does not accept a
// condition on a stale snapshot: the old package flushed synchronously before
// its first check, so spans the test created before the call must be in the
// returned payloads even when an earlier payload already satisfies the
// condition.
func TestWaitForFlushesBeforeAccepting(t *testing.T) {
	tt := startLLMObs(t)

	// Capture a first span, so later snapshots already satisfy a count-based
	// condition.
	span, _ := llmobs.StartToolSpan(context.Background(), "first")
	span.Finish()
	require.Len(t, tt.WaitForLLMObsSpans(t, 1), 1)

	// The condition below is satisfied by the first span alone.
	span, _ = llmobs.StartToolSpan(context.Background(), "second")
	span.Finish()

	p := tt.WaitFor(t, 5*time.Second, func(p *testtracer.Payloads) bool {
		return len(p.LLMSpans) >= 1
	})
	require.NotNil(t, p)
	assert.Len(t, p.LLMSpans, 2, "WaitFor must flush spans created before the call")
}
