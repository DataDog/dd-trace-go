// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"
)

func newTestEvaluationWriter() *evaluationWriter {
	return &evaluationWriter{
		aggregator: newEvaluationAggregator(100, 1000),
	}
}

func TestEvaluationHook_After_Basic(t *testing.T) {
	writer := newTestEvaluationWriter()
	hook := newEvaluationHook(writer)

	evalCtx := of.NewEvaluationContext("", map[string]any{})
	hookCtx := of.NewHookContext(
		"test-flag",
		of.Boolean,
		false,
		of.ClientMetadata{},
		of.Metadata{},
		evalCtx,
	)
	details := of.InterfaceEvaluationDetails{
		Value: true,
		EvaluationDetails: of.EvaluationDetails{
			FlagKey:  "test-flag",
			FlagType: of.Boolean,
			ResolutionDetail: of.ResolutionDetail{
				Variant: "on",
				Reason:  of.TargetingMatchReason,
			},
		},
	}

	err := hook.After(context.Background(), hookCtx, details, of.HookHints{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	full, _, _, keys, _, _ := writer.aggregator.drain()
	if len(full) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(full))
	}

	var entry *evaluationEntry
	var key evaluationAggregationKey
	for h, e := range full {
		entry = e
		key = keys[h]
	}

	if entry.count != 1 {
		t.Errorf("expected count=1, got %d", entry.count)
	}
	if key.flagKey != "test-flag" {
		t.Errorf("expected flagKey=test-flag, got %s", key.flagKey)
	}
	if key.variant != "on" {
		t.Errorf("expected variant=on, got %s", key.variant)
	}
	if key.reason != "targeting_match" {
		t.Errorf("expected reason=targeting_match, got %s", key.reason)
	}
}

func TestEvaluationHook_After_NilWriter(t *testing.T) {
	hook := newEvaluationHook(nil)

	evalCtx := of.NewEvaluationContext("", map[string]any{})
	hookCtx := of.NewHookContext(
		"test-flag",
		of.Boolean,
		false,
		of.ClientMetadata{},
		of.Metadata{},
		evalCtx,
	)
	details := of.InterfaceEvaluationDetails{
		Value: true,
		EvaluationDetails: of.EvaluationDetails{
			FlagKey: "test-flag",
			ResolutionDetail: of.ResolutionDetail{
				Variant: "on",
				Reason:  of.DefaultReason,
			},
		},
	}

	err := hook.After(context.Background(), hookCtx, details, of.HookHints{})
	if err != nil {
		t.Fatalf("expected nil error with nil writer, got: %v", err)
	}
}

func TestEvaluationHook_After_ContextCancelled(t *testing.T) {
	writer := newTestEvaluationWriter()
	hook := newEvaluationHook(writer)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	evalCtx := of.NewEvaluationContext("", map[string]any{})
	hookCtx := of.NewHookContext(
		"test-flag",
		of.Boolean,
		false,
		of.ClientMetadata{},
		of.Metadata{},
		evalCtx,
	)
	details := of.InterfaceEvaluationDetails{
		Value: true,
		EvaluationDetails: of.EvaluationDetails{
			FlagKey: "test-flag",
			ResolutionDetail: of.ResolutionDetail{
				Variant: "on",
				Reason:  of.TargetingMatchReason,
			},
		},
	}

	err := hook.After(ctx, hookCtx, details, of.HookHints{})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}

	full, _, _, _, _, _ := writer.aggregator.drain()
	if len(full) != 0 {
		t.Errorf("expected 0 entries for cancelled context, got %d", len(full))
	}
}

func TestEvaluationHook_After_WithTargetingKey(t *testing.T) {
	writer := newTestEvaluationWriter()
	hook := newEvaluationHook(writer)

	evalCtx := of.NewEvaluationContext("user-42", map[string]any{})
	hookCtx := of.NewHookContext(
		"my-flag",
		of.Boolean,
		false,
		of.ClientMetadata{},
		of.Metadata{},
		evalCtx,
	)
	details := of.InterfaceEvaluationDetails{
		Value: true,
		EvaluationDetails: of.EvaluationDetails{
			FlagKey: "my-flag",
			ResolutionDetail: of.ResolutionDetail{
				Variant: "on",
				Reason:  of.TargetingMatchReason,
			},
		},
	}

	err := hook.After(context.Background(), hookCtx, details, of.HookHints{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	full, _, _, _, _, _ := writer.aggregator.drain()
	if len(full) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(full))
	}
	for _, e := range full {
		if e.targetingKey != "user-42" {
			t.Errorf("expected targetingKey=user-42, got %s", e.targetingKey)
		}
	}
}

func TestInternReason(t *testing.T) {
	cases := []struct {
		reason   of.Reason
		expected string
	}{
		{of.TargetingMatchReason, "targeting_match"},
		{of.DefaultReason, "default"},
		{of.ErrorReason, "error"},
		{of.DisabledReason, "disabled"},
		{of.StaticReason, "static"},
		{of.CachedReason, "cached"},
		{of.SplitReason, "split"},
		{of.Reason("SPLIT"), "split"},
		{of.Reason("UNKNOWN_REASON"), "unknown_reason"},
		{of.Reason("CustomReason"), "customreason"},
	}

	for _, tc := range cases {
		got := internReason(tc.reason)
		if got != tc.expected {
			t.Errorf("internReason(%q) = %q, want %q", tc.reason, got, tc.expected)
		}
	}
}
