// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"testing"
)

func TestEvaluationAggregator_AddIncrement(t *testing.T) {
	a := newEvaluationAggregator(100, 1000)
	key := evaluationAggregationKey{
		flagKey:  "my-flag",
		variant:  "on",
		reason:   "TARGETING_MATCH",
		targetingKey: "user-1",
		contextHash: 42,
	}
	ctx := map[string]any{"env": "prod"}

	a.add(key, ctx, "", "", false, 1000)
	a.add(key, ctx, "", "", false, 2000)

	full, _ := a.drain()
	if len(full) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(full))
	}
	h := hashKey(key)
	entry := full[h]
	if entry == nil {
		t.Fatal("expected entry not found")
	}
	if entry.count != 2 {
		t.Errorf("expected count=2, got %d", entry.count)
	}
	if entry.firstEvaluation != 1000 {
		t.Errorf("expected firstEvaluation=1000, got %d", entry.firstEvaluation)
	}
	if entry.lastEvaluation != 2000 {
		t.Errorf("expected lastEvaluation=2000, got %d", entry.lastEvaluation)
	}
}

func TestEvaluationAggregator_AddDistinctKeys(t *testing.T) {
	a := newEvaluationAggregator(100, 1000)
	key1 := evaluationAggregationKey{flagKey: "flag-a", variant: "on"}
	key2 := evaluationAggregationKey{flagKey: "flag-b", variant: "off"}

	a.add(key1, nil, "", "", false, 1000)
	a.add(key2, nil, "", "", false, 1000)

	full, _ := a.drain()
	if len(full) != 2 {
		t.Errorf("expected 2 entries, got %d", len(full))
	}
}

func TestEvaluationAggregator_DrainResets(t *testing.T) {
	a := newEvaluationAggregator(100, 1000)
	key := evaluationAggregationKey{flagKey: "flag-x", variant: "on"}

	a.add(key, nil, "", "", false, 1000)

	full, degraded := a.drain()
	if len(full) != 1 {
		t.Errorf("expected 1 entry before reset, got %d", len(full))
	}
	if degraded == nil {
		t.Error("expected degraded map to be non-nil")
	}

	// After drain, aggregator should be empty.
	full2, _ := a.drain()
	if len(full2) != 0 {
		t.Errorf("expected empty full after drain, got %d entries", len(full2))
	}
	if a.globalCount != 0 {
		t.Errorf("expected globalCount=0 after drain, got %d", a.globalCount)
	}
	if len(a.perFlagFull) != 0 {
		t.Errorf("expected perFlagFull empty after drain, got %d entries", len(a.perFlagFull))
	}
}

func TestFlattenAndExtractPrimitive_Basic(t *testing.T) {
	attrs := map[string]any{
		"name":        "alice",
		"age":         int(30),
		"score":       float64(9.5),
		"active":      true,
		"targetingKey": "user-1", // should be excluded
		"nested":      map[string]any{"x": 1}, // should be excluded (not primitive)
	}
	result := flattenAndExtractPrimitive(attrs)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if _, ok := result["targetingKey"]; ok {
		t.Error("targetingKey should be excluded")
	}
	if _, ok := result["nested"]; ok {
		t.Error("nested map should be excluded")
	}
	for _, key := range []string{"name", "age", "score", "active"} {
		if _, ok := result[key]; !ok {
			t.Errorf("expected key %q in result", key)
		}
	}
}

func TestHashContext_Deterministic(t *testing.T) {
	// Build the same logical map via two different insertion orders.
	// Go map iteration is randomized so we insert in one order and verify
	// two separate calls produce the same hash.
	attrs1 := map[string]any{
		"a": "1",
		"b": "2",
		"c": "3",
	}
	attrs2 := map[string]any{
		"c": "3",
		"a": "1",
		"b": "2",
	}
	h1 := hashContext(attrs1)
	h2 := hashContext(attrs2)
	if h1 != h2 {
		t.Errorf("hashContext not deterministic: %d != %d", h1, h2)
	}

	// Different values should produce different hashes.
	attrs3 := map[string]any{
		"a": "X",
		"b": "2",
		"c": "3",
	}
	h3 := hashContext(attrs3)
	if h1 == h3 {
		t.Error("expected different hashes for different attrs")
	}
}
