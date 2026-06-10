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

func TestEvaluationAggregator_GlobalCapRoutesToDegraded(t *testing.T) {
	a := newEvaluationAggregator(100, 3)

	// Add 3 distinct tuples for different flags → all go to full, globalCount==3
	key1 := evaluationAggregationKey{flagKey: "flag-a", variant: "on", targetingKey: "user-1"}
	key2 := evaluationAggregationKey{flagKey: "flag-b", variant: "off", targetingKey: "user-2"}
	key3 := evaluationAggregationKey{flagKey: "flag-c", variant: "on", targetingKey: "user-3"}

	a.add(key1, nil, "", "", false, 1000)
	a.add(key2, nil, "", "", false, 1000)
	a.add(key3, nil, "", "", false, 1000)

	if a.globalCount != 3 {
		t.Errorf("expected globalCount=3 after adding 3 entries, got %d", a.globalCount)
	}

	// Add a 4th tuple for a new flag → global cap is hit, should go to degraded
	key4 := evaluationAggregationKey{flagKey: "flag-d", variant: "on", targetingKey: "user-4"}
	a.add(key4, nil, "", "", false, 2000)

	// globalCount should still be 3 (degraded inserts don't increment it)
	if a.globalCount != 3 {
		t.Errorf("expected globalCount=3 after degraded insert, got %d", a.globalCount)
	}

	full, degraded, _, _ := a.drain()
	if len(full) != 3 {
		t.Errorf("expected 3 full entries, got %d", len(full))
	}
	if len(degraded) != 1 {
		t.Errorf("expected 1 degraded entry, got %d", len(degraded))
	}
}

func TestEvaluationAggregator_GlobalCapIncrementExistingDegraded(t *testing.T) {
	a := newEvaluationAggregator(100, 2)

	// Add 2 entries to fill global cap (flag-a/user1, flag-a/user2)
	key1 := evaluationAggregationKey{flagKey: "flag-a", variant: "on", reason: "TARGETING_MATCH", targetingKey: "user-1"}
	key2 := evaluationAggregationKey{flagKey: "flag-a", variant: "on", reason: "TARGETING_MATCH", targetingKey: "user-2"}
	a.add(key1, nil, "", "", false, 1000)
	a.add(key2, nil, "", "", false, 1000)

	if a.globalCount != 2 {
		t.Errorf("expected globalCount=2 after filling cap, got %d", a.globalCount)
	}

	// Add 2 more entries with same degraded key (flag-a, variant=on, reason=TARGETING_MATCH)
	// Both should go to the same degraded bucket
	key3 := evaluationAggregationKey{flagKey: "flag-a", variant: "on", reason: "TARGETING_MATCH", targetingKey: "user-3"}
	key4 := evaluationAggregationKey{flagKey: "flag-a", variant: "on", reason: "TARGETING_MATCH", targetingKey: "user-4"}
	a.add(key3, nil, "", "", false, 1000)
	a.add(key4, nil, "", "", false, 2000)

	_, degraded, _, _ := a.drain()
	if len(degraded) != 1 {
		t.Fatalf("expected 1 degraded bucket, got %d", len(degraded))
	}

	// Find the degraded entry and verify count == 2
	dk := evaluationDegradedKey{flagKey: "flag-a", variant: "on", reason: "TARGETING_MATCH"}
	dh := hashDegradedKey(dk)
	entry := degraded[dh]
	if entry == nil {
		t.Fatal("expected degraded entry not found")
	}
	if entry.count != 2 {
		t.Errorf("expected degraded count=2, got %d", entry.count)
	}
}

func TestEvaluationAggregator_FairnessEviction(t *testing.T) {
	a := newEvaluationAggregator(10, 2)

	// Add flag-a/user1, flag-a/user2 → fills global cap (globalCount=2)
	key1 := evaluationAggregationKey{flagKey: "flag-a", variant: "on", reason: "TARGETING_MATCH", targetingKey: "user-1"}
	key2 := evaluationAggregationKey{flagKey: "flag-a", variant: "on", reason: "TARGETING_MATCH", targetingKey: "user-2"}
	a.add(key1, nil, "", "", false, 1000)
	a.add(key2, nil, "", "", false, 1000)

	if a.globalCount != 2 {
		t.Fatalf("expected globalCount=2 after filling cap, got %d", a.globalCount)
	}

	// Add flag-b/user3 (cold flag, never seen) → fairness fires
	keyB := evaluationAggregationKey{flagKey: "flag-b", variant: "on", reason: "TARGETING_MATCH", targetingKey: "user-3"}
	a.add(keyB, nil, "", "", false, 2000)

	// len(full) == 2 (one flag-a entry + flag-b/user3)
	if len(a.full) != 2 {
		t.Errorf("expected len(full)==2, got %d", len(a.full))
	}
	// len(degraded) == 1 (the evicted flag-a entry rolled into degraded)
	if len(a.degraded) != 1 {
		t.Errorf("expected len(degraded)==1, got %d", len(a.degraded))
	}
	// perFlagFull["flag-b"] == 1
	if a.perFlagFull["flag-b"] != 1 {
		t.Errorf("expected perFlagFull[flag-b]==1, got %d", a.perFlagFull["flag-b"])
	}
	// perFlagFull["flag-a"] == 1 (was 2, one evicted)
	if a.perFlagFull["flag-a"] != 1 {
		t.Errorf("expected perFlagFull[flag-a]==1, got %d", a.perFlagFull["flag-a"])
	}
	// globalCount == 2
	if a.globalCount != 2 {
		t.Errorf("expected globalCount==2, got %d", a.globalCount)
	}
	// The degraded entry for flag-a has count == 1 (the evicted entry's count)
	dk := evaluationDegradedKey{flagKey: "flag-a", variant: "on", reason: "TARGETING_MATCH"}
	dh := hashDegradedKey(dk)
	de := a.degraded[dh]
	if de == nil {
		t.Fatal("expected degraded entry for flag-a not found")
	}
	if de.count != 1 {
		t.Errorf("expected degraded count==1, got %d", de.count)
	}
}

func TestEvaluationAggregator_FairnessOnlyForColdFlags(t *testing.T) {
	a := newEvaluationAggregator(10, 2)

	// Add flag-a/user1, flag-a/user2 → fills global cap
	key1 := evaluationAggregationKey{flagKey: "flag-a", variant: "on", reason: "TARGETING_MATCH", targetingKey: "user-1"}
	key2 := evaluationAggregationKey{flagKey: "flag-a", variant: "on", reason: "TARGETING_MATCH", targetingKey: "user-2"}
	a.add(key1, nil, "", "", false, 1000)
	a.add(key2, nil, "", "", false, 1000)

	if a.globalCount != 2 {
		t.Fatalf("expected globalCount=2 after filling cap, got %d", a.globalCount)
	}

	// Add flag-a/user3 (NOT cold — flag-a already has entries) → should go to degraded normally, no eviction
	key3 := evaluationAggregationKey{flagKey: "flag-a", variant: "on", reason: "TARGETING_MATCH", targetingKey: "user-3"}
	a.add(key3, nil, "", "", false, 2000)

	// len(full) == 2 (no change)
	if len(a.full) != 2 {
		t.Errorf("expected len(full)==2, got %d", len(a.full))
	}
	// len(degraded) == 1 (flag-a/user3 degraded)
	if len(a.degraded) != 1 {
		t.Errorf("expected len(degraded)==1, got %d", len(a.degraded))
	}
	// perFlagFull["flag-a"] == 2 (unchanged by the degraded insert)
	if a.perFlagFull["flag-a"] != 2 {
		t.Errorf("expected perFlagFull[flag-a]==2, got %d", a.perFlagFull["flag-a"])
	}
}

func TestEvaluationAggregator_FairnessCountFolded(t *testing.T) {
	a := newEvaluationAggregator(10, 2)

	// Add flag-a/user1 with count incremented 5 times (add same key 5 times)
	key1 := evaluationAggregationKey{flagKey: "flag-a", variant: "on", reason: "TARGETING_MATCH", targetingKey: "user-1"}
	for i := 0; i < 5; i++ {
		a.add(key1, nil, "", "", false, int64(1000+i))
	}

	// Add flag-a/user2 → fills global cap (globalCount=2)
	key2 := evaluationAggregationKey{flagKey: "flag-a", variant: "on", reason: "TARGETING_MATCH", targetingKey: "user-2"}
	a.add(key2, nil, "", "", false, 2000)

	if a.globalCount != 2 {
		t.Fatalf("expected globalCount=2 after filling cap, got %d", a.globalCount)
	}

	// Add flag-b/user1 (cold) → fairness evicts one flag-a entry
	keyB := evaluationAggregationKey{flagKey: "flag-b", variant: "on", reason: "TARGETING_MATCH", targetingKey: "user-1"}
	a.add(keyB, nil, "", "", false, 3000)

	// Determine which flag-a entry was evicted (victim is the one with most count = user-1 with count 5,
	// but scan order is map iteration; either could be evicted. We check total preservation.)
	// Assert degraded entry for flag-a exists
	dk := evaluationDegradedKey{flagKey: "flag-a", variant: "on", reason: "TARGETING_MATCH"}
	dh := hashDegradedKey(dk)
	de := a.degraded[dh]
	if de == nil {
		t.Fatal("expected degraded entry for flag-a not found")
	}

	// Sum full counts for flag-a + degraded count for flag-a == 6 (total evaluations preserved)
	var fullFlagACount int64
	for h, entry := range a.full {
		if k, ok := a.keys[h]; ok && k.flagKey == "flag-a" {
			fullFlagACount += entry.count
		}
	}
	total := fullFlagACount + de.count
	if total != 6 {
		t.Errorf("expected total flag-a evaluations==6 (preserved), got %d (full=%d, degraded=%d)", total, fullFlagACount, de.count)
	}
	// degraded count should equal the evicted entry's original count
	if de.count != 5 && de.count != 1 {
		t.Errorf("expected degraded count to be 5 or 1 (the evicted entry's count), got %d", de.count)
	}
}

func TestEvaluationAggregator_DrainResetsGlobalCount(t *testing.T) {
	a := newEvaluationAggregator(2, 2)

	// Fill to global cap
	key1 := evaluationAggregationKey{flagKey: "flag-a", variant: "on", targetingKey: "user-1"}
	key2 := evaluationAggregationKey{flagKey: "flag-b", variant: "on", targetingKey: "user-2"}
	a.add(key1, nil, "", "", false, 1000)
	a.add(key2, nil, "", "", false, 1000)

	if a.globalCount != 2 {
		t.Errorf("expected globalCount=2 before drain, got %d", a.globalCount)
	}

	// Drain
	a.drain()

	// After drain, globalCount should be 0
	if a.globalCount != 0 {
		t.Errorf("expected globalCount=0 after drain, got %d", a.globalCount)
	}

	// Verify that new entries can be added after drain
	key3 := evaluationAggregationKey{flagKey: "flag-c", variant: "on", targetingKey: "user-3"}
	a.add(key3, nil, "", "", false, 2000)

	if a.globalCount != 1 {
		t.Errorf("expected globalCount=1 after adding entry post-drain, got %d", a.globalCount)
	}

	full, _, _, _ := a.drain()
	if len(full) != 1 {
		t.Errorf("expected 1 full entry after drain, got %d", len(full))
	}
}
