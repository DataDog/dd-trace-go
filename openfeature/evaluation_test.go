// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"strconv"
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

	full, _, _, _ := a.drain()
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

	full, _, _, _ := a.drain()
	if len(full) != 2 {
		t.Errorf("expected 2 entries, got %d", len(full))
	}
}

func TestEvaluationAggregator_DrainResets(t *testing.T) {
	a := newEvaluationAggregator(100, 1000)
	key := evaluationAggregationKey{flagKey: "flag-x", variant: "on"}

	a.add(key, nil, "", "", false, 1000)

	full, degraded, keys, degradedKeys := a.drain()
	if len(full) != 1 {
		t.Errorf("expected 1 entry before reset, got %d", len(full))
	}
	if degraded == nil {
		t.Error("expected degraded map to be non-nil")
	}
	if len(keys) != 1 {
		t.Errorf("expected keys map len=1, got %d", len(keys))
	}
	if degradedKeys == nil {
		t.Error("expected degradedKeys map to be non-nil")
	}

	// After drain, aggregator should be empty.
	full2, _, keys2, _ := a.drain()
	if len(full2) != 0 {
		t.Errorf("expected empty full after drain, got %d entries", len(full2))
	}
	if a.globalCount != 0 {
		t.Errorf("expected globalCount=0 after drain, got %d", a.globalCount)
	}
	if len(a.perFlagFull) != 0 {
		t.Errorf("expected perFlagFull empty after drain, got %d entries", len(a.perFlagFull))
	}
	if len(keys2) != 0 {
		t.Errorf("expected empty keys after drain, got %d entries", len(keys2))
	}
}

func TestEvaluationAggregator_PerFlagSoftCap(t *testing.T) {
	a := newEvaluationAggregator(3, 100)

	// Add 3 distinct tuples for "flag-a" (different targetingKeys) → all go to full.
	for i := 0; i < 3; i++ {
		key := evaluationAggregationKey{
			flagKey:          "flag-a",
			variant:          "on",
			reason:           "TARGETING_MATCH",
			targetingKey:     "user-" + strconv.Itoa(i),
			targetingRuleKey: "rule-" + strconv.Itoa(i),
		}
		a.add(key, nil, "", "", false, 1000)
	}

	if got := a.perFlagFull["flag-a"]; got != 3 {
		t.Errorf("expected perFlagFull[flag-a]=3, got %d", got)
	}

	// Add a 4th distinct tuple for "flag-a" → should go to degraded.
	key4 := evaluationAggregationKey{
		flagKey:          "flag-a",
		variant:          "on",
		reason:           "TARGETING_MATCH",
		targetingKey:     "user-99",
		targetingRuleKey: "rule-99",
	}
	a.add(key4, nil, "", "", false, 2000)

	// perFlagFull should still be 3 (not incremented for degraded).
	if got := a.perFlagFull["flag-a"]; got != 3 {
		t.Errorf("expected perFlagFull[flag-a]=3 after cap hit, got %d", got)
	}

	full, degraded, _, _ := a.drain()
	if len(full) != 3 {
		t.Errorf("expected 3 full entries, got %d", len(full))
	}
	if len(degraded) != 1 {
		t.Errorf("expected 1 degraded entry, got %d", len(degraded))
	}
}

func TestEvaluationAggregator_DegradedBucketIncrement(t *testing.T) {
	a := newEvaluationAggregator(2, 100)

	// Add 3 distinct tuples for "flag-b" with same variant/allocation/rule/reason.
	// Third one goes to degraded.
	for i := 0; i < 3; i++ {
		key := evaluationAggregationKey{
			flagKey:      "flag-b",
			variant:      "on",
			reason:       "TARGETING_MATCH",
			targetingKey: "user-" + strconv.Itoa(i),
		}
		a.add(key, nil, "", "", false, 1000)
	}

	// Add a 4th tuple with same variant/allocation/rule/reason → increments same degraded bucket.
	key4 := evaluationAggregationKey{
		flagKey:      "flag-b",
		variant:      "on",
		reason:       "TARGETING_MATCH",
		targetingKey: "user-99",
	}
	a.add(key4, nil, "", "", false, 2000)

	_, degraded, _, _ := a.drain()
	if len(degraded) != 1 {
		t.Fatalf("expected 1 degraded entry, got %d", len(degraded))
	}

	// Find the degraded entry and verify count == 2.
	dk := evaluationDegradedKey{flagKey: "flag-b", variant: "on", reason: "TARGETING_MATCH"}
	dh := hashDegradedKey(dk)
	entry := degraded[dh]
	if entry == nil {
		t.Fatal("expected degraded entry not found")
	}
	if entry.count != 2 {
		t.Errorf("expected degraded count=2, got %d", entry.count)
	}
}

func TestEvaluationAggregator_PerFlagCapDoesNotAffectOtherFlags(t *testing.T) {
	a := newEvaluationAggregator(2, 100)

	// Fill flag-a to cap (2 entries).
	for i := 0; i < 2; i++ {
		key := evaluationAggregationKey{
			flagKey:      "flag-a",
			variant:      "on",
			targetingKey: "user-" + strconv.Itoa(i),
		}
		a.add(key, nil, "", "", false, 1000)
	}

	// Add a tuple for flag-b → should go to full, not degraded.
	keyB := evaluationAggregationKey{flagKey: "flag-b", variant: "on", targetingKey: "user-0"}
	a.add(keyB, nil, "", "", false, 1000)

	full, degraded, _, _ := a.drain()
	if len(full) != 3 {
		t.Errorf("expected 3 full entries (2 flag-a + 1 flag-b), got %d", len(full))
	}
	if len(degraded) != 0 {
		t.Errorf("expected 0 degraded entries, got %d", len(degraded))
	}
}

func TestEvaluationAggregator_DrainResetsKeyMaps(t *testing.T) {
	a := newEvaluationAggregator(2, 100)

	// Add a full entry and a degraded entry.
	keyFull := evaluationAggregationKey{flagKey: "flag-x", variant: "on", targetingKey: "u1"}
	keyFull2 := evaluationAggregationKey{flagKey: "flag-x", variant: "on", targetingKey: "u2"}
	keyOver := evaluationAggregationKey{flagKey: "flag-x", variant: "on", targetingKey: "u3"}
	a.add(keyFull, nil, "", "", false, 1000)
	a.add(keyFull2, nil, "", "", false, 1000)
	a.add(keyOver, nil, "", "", false, 2000) // goes to degraded

	full, degraded, keys, degradedKeys := a.drain()

	if len(full) != 2 {
		t.Errorf("expected 2 full entries, got %d", len(full))
	}
	if len(degraded) != 1 {
		t.Errorf("expected 1 degraded entry, got %d", len(degraded))
	}
	if len(keys) != 2 {
		t.Errorf("expected keys map len=2, got %d", len(keys))
	}
	if len(degradedKeys) != 1 {
		t.Errorf("expected degradedKeys map len=1, got %d", len(degradedKeys))
	}

	// Verify keys map contains the correct keys.
	h1 := hashKey(keyFull)
	if _, ok := keys[h1]; !ok {
		t.Error("expected keyFull in keys map")
	}

	// After drain, internal maps should be reset.
	full2, _, keys2, degradedKeys2 := a.drain()
	if len(full2) != 0 {
		t.Errorf("expected empty full after drain, got %d", len(full2))
	}
	if len(keys2) != 0 {
		t.Errorf("expected empty keys after drain, got %d", len(keys2))
	}
	if len(degradedKeys2) != 0 {
		t.Errorf("expected empty degradedKeys after drain, got %d", len(degradedKeys2))
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
