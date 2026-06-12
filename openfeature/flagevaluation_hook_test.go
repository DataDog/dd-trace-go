// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"testing"
	"time"

	jsoniter "github.com/json-iterator/go"
	of "github.com/open-feature/go-sdk/openfeature"
)

// setupTestWriter creates a flagEvaluationWriter configured for unit testing.
// The writer uses a large flush interval (24 h) so no automatic flush fires during tests.
func setupTestWriter(t *testing.T) *flagEvaluationWriter {
	t.Helper()
	return &flagEvaluationWriter{
		flushInterval: 24 * time.Hour, // effectively disabled; tests control flush manually
		jsonConfig:    jsoniter.Config{}.Froze(),
		stopChan:      make(chan struct{}),
		events:        make(chan evalEvent, defaultEvalEventBufferSize),
		aggregator: flagEvaluationAggregator{
			full:        make(map[evaluationAggregationKey]*evaluationEntry),
			degraded:    make(map[evaluationDegradedKey]*evaluationEntry),
			ultraDeg:    make(map[evaluationUltraDegradedKey]*evaluationEntry),
			perFlagFull: make(map[string]int),
			globalCap:   10,
			perFlagCap:  3,
			degradedCap: 3,
		},
	}
}

// makeHookContext creates an of.HookContext for testing.
func makeHookContext(flagKey string, targetingKey string, attrs map[string]any) of.HookContext {
	evalCtx := of.NewEvaluationContext(targetingKey, attrs)
	return of.NewHookContext(
		flagKey,
		of.Boolean,
		false,
		of.ClientMetadata{},
		of.Metadata{},
		evalCtx,
	)
}

// makeEvalDetails constructs an InterfaceEvaluationDetails for hook testing.
func makeEvalDetails(variant string, reason of.Reason, errorCode of.ErrorCode, metadata ...of.FlagMetadata) of.InterfaceEvaluationDetails {
	d := of.InterfaceEvaluationDetails{
		EvaluationDetails: of.EvaluationDetails{
			ResolutionDetail: of.ResolutionDetail{
				Variant:   variant,
				Reason:    reason,
				ErrorCode: errorCode,
			},
		},
	}
	if len(metadata) > 0 {
		d.FlagMetadata = metadata[0]
	}
	return d
}

// TestIsRuntimeDefault verifies the runtime-default detection rule.
// Signal: absent variant key. Our evaluator sets a variant ONLY on a matched
// allocation (TARGETING_MATCH/SPLIT/STATIC); every DEFAULT/DISABLED/ERROR path
// leaves the variant empty. A present variant therefore means a real assignment,
// never a default — regardless of the reported reason.
func TestIsRuntimeDefault(t *testing.T) {
	tests := []struct {
		name    string
		details of.InterfaceEvaluationDetails
		want    bool
	}{
		{
			name:    "empty variant is runtime default",
			details: makeEvalDetails("", of.TargetingMatchReason, ""),
			want:    true,
		},
		{
			name:    "empty variant with DEFAULT reason is runtime default",
			details: makeEvalDetails("", of.DefaultReason, ""),
			want:    true,
		},
		{
			name:    "empty variant with DISABLED reason is runtime default",
			details: makeEvalDetails("", of.DisabledReason, ""),
			want:    true,
		},
		{
			name:    "empty variant with ERROR reason is runtime default",
			details: makeEvalDetails("", of.ErrorReason, of.FlagNotFoundCode),
			want:    true,
		},
		{
			name:    "variant present with TARGETING_MATCH is NOT runtime default",
			details: makeEvalDetails("on", of.TargetingMatchReason, ""),
			want:    false,
		},
		{
			name:    "variant present with SPLIT is NOT runtime default",
			details: makeEvalDetails("variant-a", of.SplitReason, ""),
			want:    false,
		},
		{
			name:    "variant present with STATIC is NOT runtime default",
			details: makeEvalDetails("on", of.StaticReason, ""),
			want:    false,
		},
		{
			// Divergent case: a present variant means a real assignment even if the
			// reason is DISABLED. The old secondary reason-clause wrongly returned true.
			name:    "variant present with DISABLED reason is NOT runtime default",
			details: makeEvalDetails("on", of.DisabledReason, ""),
			want:    false,
		},
		{
			// Divergent case: a present variant means a real assignment even if the
			// reason is DEFAULT. The old secondary reason-clause wrongly returned true.
			name:    "variant present with DEFAULT reason is NOT runtime default",
			details: makeEvalDetails("on", of.DefaultReason, ""),
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isRuntimeDefault(tc.details)
			if got != tc.want {
				t.Errorf("isRuntimeDefault() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestFlagEvaluationHookFinally verifies that the Finally hook records an entry for
// success, error-reason, and provider-not-ready paths.
//
// It must fail RED: the hook's Finally method panics with "not implemented".
func TestFlagEvaluationHookFinally(t *testing.T) {
	runtimeDefaultTrue := true

	tests := []struct {
		name               string
		flagKey            string
		targetingKey       string
		attrs              map[string]any
		variant            string
		reason             of.Reason
		errorCode          of.ErrorCode
		metadata           []of.FlagMetadata
		wantRuntimeDefault *bool // when set, assert the recorded full-tier entry's runtimeDefault
	}{
		{
			name:         "success path records entry",
			flagKey:      "test-flag",
			targetingKey: "user-123",
			attrs:        map[string]any{"country": "US"},
			variant:      "on",
			reason:       of.TargetingMatchReason,
			metadata:     []of.FlagMetadata{{metadataAllocationKey: "default-alloc"}},
		},
		{
			name:         "error-reason path records entry (flag-not-found)",
			flagKey:      "missing-flag",
			targetingKey: "user-123",
			reason:       of.ErrorReason,
			errorCode:    of.FlagNotFoundCode,
		},
		{
			name:      "provider-not-ready path records entry",
			flagKey:   "any-flag",
			reason:    of.ErrorReason,
			errorCode: of.ProviderNotReadyCode,
		},
		{
			name:               "DEFAULT reason path records entry with runtime_default_used=true",
			flagKey:            "absent-flag",
			targetingKey:       "user-456",
			reason:             of.DefaultReason,
			wantRuntimeDefault: &runtimeDefaultTrue,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := setupTestWriter(t)
			hook := newFlagEvaluationHook(w)

			hookCtx := makeHookContext(tc.flagKey, tc.targetingKey, tc.attrs)
			details := makeEvalDetails(tc.variant, tc.reason, tc.errorCode, tc.metadata...)

			hook.Finally(context.Background(), hookCtx, details, of.HookHints{})

			// record() enqueues asynchronously; drain the one event into the aggregator so the
			// assertions below observe it deterministically (no worker runs in this test).
			w.aggregate(<-w.events)

			w.aggregator.mu.Lock()
			defer w.aggregator.mu.Unlock()

			total := len(w.aggregator.full) + len(w.aggregator.degraded) + len(w.aggregator.ultraDeg)
			if total == 0 {
				t.Error("expected Finally to record an entry, got none")
			}

			if tc.wantRuntimeDefault != nil {
				for _, e := range w.aggregator.full {
					if e.runtimeDefault != *tc.wantRuntimeDefault {
						t.Errorf("expected runtimeDefault=%v, got %v", *tc.wantRuntimeDefault, e.runtimeDefault)
					}
				}
			}
		})
	}
}

// TestFlagEvaluationHookContextCancelled verifies that a cancelled context does NOT
// drop the evaluation: record() is a non-blocking enqueue that ignores the request
// context, so a cancelled request must still be counted.
func TestFlagEvaluationHookContextCancelled(t *testing.T) {
	w := setupTestWriter(t)
	hook := newFlagEvaluationHook(w)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel BEFORE calling Finally

	hookCtx := makeHookContext("test-flag", "user-123", nil)
	details := makeEvalDetails("on", of.TargetingMatchReason, "")

	hook.Finally(ctx, hookCtx, details, of.HookHints{})

	// record() enqueues asynchronously; drain the one event so the assertion sees it.
	w.aggregate(<-w.events)

	w.aggregator.mu.Lock()
	defer w.aggregator.mu.Unlock()

	total := len(w.aggregator.full) + len(w.aggregator.degraded) + len(w.aggregator.ultraDeg)
	if total != 1 {
		t.Errorf("expected the cancelled-context evaluation to still be counted, got %d entries", total)
	}
}

// TestFlagEvaluationBackpressureDrops verifies the explicit backpressure policy: when the
// async hand-off queue is full, record() drops the event and counts it (observable) rather
// than blocking the evaluation. No worker drains in this test, so the queue fills after
// defaultEvalEventBufferSize enqueues and every further record() is a counted drop.
func TestFlagEvaluationBackpressureDrops(t *testing.T) {
	w := setupTestWriter(t)
	hookCtx := makeHookContext("bp-flag", "user-1", nil)
	details := makeEvalDetails("on", of.TargetingMatchReason, "")

	const overflow = 100
	for range defaultEvalEventBufferSize + overflow {
		w.record(hookCtx, details) // must never block, even once the queue is full
	}

	if got := w.dropped.Load(); got != overflow {
		t.Errorf("expected exactly %d dropped evaluations once the queue filled, got %d", overflow, got)
	}
}
