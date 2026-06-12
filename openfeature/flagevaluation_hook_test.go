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

// TestIsRuntimeDefault verifies the runtime-default detection rule (CONT-07 / source_comment_id 3395344504).
// Primary signal: variant=="" → true.
// Secondary signal: reason==DEFAULT or reason==DISABLED → true even if variant is non-empty.
//
// It must fail RED: isRuntimeDefault panics with "not implemented".
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
			name:    "reason DEFAULT is runtime default",
			details: makeEvalDetails("", of.DefaultReason, ""),
			want:    true,
		},
		{
			name:    "reason DISABLED is runtime default",
			details: makeEvalDetails("", of.DisabledReason, ""),
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
			name:    "empty variant with ERROR reason is runtime default (primary: variant absent)",
			details: makeEvalDetails("", of.ErrorReason, of.FlagNotFoundCode),
			want:    true,
		},
		{
			name:    "variant present with DEFAULT reason is runtime default (secondary belt-and-suspenders)",
			details: makeEvalDetails("default-variant", of.DefaultReason, ""),
			want:    true,
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
// success, error-reason, and provider-not-ready paths (CONT-09 / source_comment_id 3385309423).
//
// It must fail RED: the hook's Finally method panics with "not implemented".
func TestFlagEvaluationHookFinally(t *testing.T) {
	t.Run("success path records entry", func(t *testing.T) {
		w := setupTestWriter(t)
		hook := newFlagEvaluationHook(w)

		hookCtx := makeHookContext("test-flag", "user-123", map[string]any{"country": "US"})
		details := makeEvalDetails("on", of.TargetingMatchReason, "", of.FlagMetadata{
			metadataAllocationKey: "default-alloc",
		})

		hook.Finally(context.Background(), hookCtx, details, of.HookHints{})

		w.aggregator.mu.Lock()
		defer w.aggregator.mu.Unlock()

		total := len(w.aggregator.full) + len(w.aggregator.degraded) + len(w.aggregator.ultraDeg)
		if total == 0 {
			t.Error("expected Finally to record an entry on success path, got none")
		}
	})

	t.Run("error-reason path records entry (flag-not-found)", func(t *testing.T) {
		w := setupTestWriter(t)
		hook := newFlagEvaluationHook(w)

		hookCtx := makeHookContext("missing-flag", "user-123", nil)
		details := makeEvalDetails("", of.ErrorReason, of.FlagNotFoundCode)

		hook.Finally(context.Background(), hookCtx, details, of.HookHints{})

		w.aggregator.mu.Lock()
		defer w.aggregator.mu.Unlock()

		total := len(w.aggregator.full) + len(w.aggregator.degraded) + len(w.aggregator.ultraDeg)
		if total == 0 {
			t.Error("expected Finally to record an entry on error-reason path, got none")
		}
	})

	t.Run("provider-not-ready path records entry", func(t *testing.T) {
		w := setupTestWriter(t)
		hook := newFlagEvaluationHook(w)

		hookCtx := makeHookContext("any-flag", "", nil)
		details := makeEvalDetails("", of.ErrorReason, of.ProviderNotReadyCode)

		hook.Finally(context.Background(), hookCtx, details, of.HookHints{})

		w.aggregator.mu.Lock()
		defer w.aggregator.mu.Unlock()

		total := len(w.aggregator.full) + len(w.aggregator.degraded) + len(w.aggregator.ultraDeg)
		if total == 0 {
			t.Error("expected Finally to record an entry on provider-not-ready path, got none")
		}
	})

	t.Run("DEFAULT reason path records entry with runtime_default_used=true", func(t *testing.T) {
		w := setupTestWriter(t)
		hook := newFlagEvaluationHook(w)

		hookCtx := makeHookContext("absent-flag", "user-456", nil)
		details := makeEvalDetails("", of.DefaultReason, "")

		hook.Finally(context.Background(), hookCtx, details, of.HookHints{})

		w.aggregator.mu.Lock()
		defer w.aggregator.mu.Unlock()

		total := len(w.aggregator.full) + len(w.aggregator.degraded) + len(w.aggregator.ultraDeg)
		if total == 0 {
			t.Error("expected Finally to record an entry on DEFAULT-reason path, got none")
		}

		// The recorded entry should have runtimeDefault=true
		for _, e := range w.aggregator.full {
			if !e.runtimeDefault {
				t.Error("expected runtimeDefault=true for DEFAULT-reason evaluation")
			}
		}
	})
}

// TestFlagEvaluationHookContextCancelled verifies that a cancelled context causes
// Finally to return without recording (context safety / no zombie writes).
//
// It must fail RED: the hook's Finally method panics with "not implemented".
func TestFlagEvaluationHookContextCancelled(t *testing.T) {
	w := setupTestWriter(t)
	hook := newFlagEvaluationHook(w)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel BEFORE calling Finally

	hookCtx := makeHookContext("test-flag", "user-123", nil)
	details := makeEvalDetails("on", of.TargetingMatchReason, "")

	hook.Finally(ctx, hookCtx, details, of.HookHints{})

	w.aggregator.mu.Lock()
	defer w.aggregator.mu.Unlock()

	total := len(w.aggregator.full) + len(w.aggregator.degraded) + len(w.aggregator.ultraDeg)
	if total != 0 {
		t.Errorf("expected no entries when context is cancelled, got %d", total)
	}
}
