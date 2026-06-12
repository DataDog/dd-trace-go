// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// setupTestAggregator creates a flagEvaluationAggregator with small caps for testing.
// Caps are deliberately small to trigger tier-cascade behavior in unit tests.
func setupTestAggregator(t *testing.T) *flagEvaluationAggregator {
	t.Helper()
	return &flagEvaluationAggregator{
		full:        make(map[evaluationAggregationKey]*evaluationEntry),
		degraded:    make(map[evaluationDegradedKey]*evaluationEntry),
		ultraDeg:    make(map[evaluationUltraDegradedKey]*evaluationEntry),
		perFlagFull: make(map[string]int),
		globalCap:   10, // small cap to trigger overflow in tests
		perFlagCap:  3,
		degradedCap: 3,
	}
}

// TestPruneContext verifies that pruneContext applies the 256-field / 256-char limits
// before evaluation context enters the aggregation buffer.
//
// It must fail RED: pruneContext panics with "not implemented".
func TestPruneContext(t *testing.T) {
	t.Run("300 fields truncated to exactly 256", func(t *testing.T) {
		raw := make(map[string]any, 300)
		for i := range 300 {
			raw[fmt.Sprintf("key%d", i)] = fmt.Sprintf("value%d", i)
		}

		out := pruneContext(raw)

		if len(out) != 256 {
			t.Errorf("expected exactly 256 fields after prune, got %d", len(out))
		}
	})

	t.Run("string value exceeding 256 chars is dropped", func(t *testing.T) {
		longVal := strings.Repeat("x", 300) // 300 chars > maxFieldLength(256)
		raw := map[string]any{
			"short": "ok",
			"long":  longVal,
		}

		out := pruneContext(raw)

		if _, ok := out["long"]; ok {
			t.Error("expected long string value to be dropped from pruned context")
		}
		if _, ok := out["short"]; !ok {
			t.Error("expected short string value to be retained in pruned context")
		}
	})

	t.Run("nil input returns nil", func(t *testing.T) {
		out := pruneContext(nil)
		if out != nil {
			t.Errorf("expected nil for nil input, got %v", out)
		}
	})

	t.Run("empty input returns nil or empty", func(t *testing.T) {
		out := pruneContext(map[string]any{})
		if out != nil && len(out) != 0 {
			t.Errorf("expected nil or empty for empty input, got %v", out)
		}
	})

	t.Run("non-string values are retained regardless of notional length", func(t *testing.T) {
		raw := map[string]any{
			"intVal":  42,
			"boolVal": true,
		}
		out := pruneContext(raw)
		if out["intVal"] == nil {
			t.Error("expected integer value to be retained")
		}
		if out["boolVal"] == nil {
			t.Error("expected boolean value to be retained")
		}
	})
}

// TestFlagEvaluationPayloadSchema verifies that full, degraded, and ultra-degraded events
// marshal to JSON that omits the expected optional fields per tier while always including
// the 5 required fields.
//
// It must fail RED: the aggregator.add method panics with "not implemented".
func TestFlagEvaluationPayloadSchema(t *testing.T) {
	nowMs := time.Now().UnixMilli()

	t.Run("full tier has all required fields", func(t *testing.T) {
		event := flagEvaluationEvent{
			Timestamp:       nowMs,
			Flag:            flagEvalFlag{Key: "test-flag"},
			FirstEvaluation: nowMs,
			LastEvaluation:  nowMs,
			EvaluationCount: 1,
			Variant:         &flagEvalVariant{Key: "on"},
			TargetingKey:    "user-123",
			Context: &flagEvalEventContext{
				Evaluation: map[string]any{"country": "US"},
			},
		}

		b, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("failed to marshal full event: %v", err)
		}

		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		// Required fields must be present
		for _, req := range []string{"timestamp", "flag", "first_evaluation", "last_evaluation", "evaluation_count"} {
			if _, ok := m[req]; !ok {
				t.Errorf("full tier: required field %q missing from marshaled JSON", req)
			}
		}

		// flag.key must be present
		if flagObj, ok := m["flag"].(map[string]any); !ok {
			t.Error("full tier: flag is not an object")
		} else if _, ok := flagObj["key"]; !ok {
			t.Error("full tier: flag.key missing")
		}
	})

	t.Run("degraded tier omits targeting_key and context.evaluation", func(t *testing.T) {
		// Degraded tier: no targeting_key, no context.evaluation; variant + allocation present
		event := flagEvaluationEvent{
			Timestamp:       nowMs,
			Flag:            flagEvalFlag{Key: "test-flag"},
			FirstEvaluation: nowMs,
			LastEvaluation:  nowMs,
			EvaluationCount: 5,
			Variant:         &flagEvalVariant{Key: "on"},
			Allocation:      &flagEvalAllocation{Key: "default"},
			// TargetingKey:   intentionally absent
			// Context:        intentionally absent
		}

		b, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("failed to marshal degraded event: %v", err)
		}

		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		// Required fields must be present
		for _, req := range []string{"timestamp", "flag", "first_evaluation", "last_evaluation", "evaluation_count"} {
			if _, ok := m[req]; !ok {
				t.Errorf("degraded tier: required field %q missing", req)
			}
		}

		// targeting_key must be absent
		if _, ok := m["targeting_key"]; ok {
			t.Error("degraded tier: targeting_key should be absent")
		}

		// context must be absent
		if _, ok := m["context"]; ok {
			t.Error("degraded tier: context should be absent")
		}
	})

	t.Run("ultra-degraded tier has only required fields", func(t *testing.T) {
		// Ultra-degraded: only flag key + counts; no variant, allocation, targeting, context
		event := flagEvaluationEvent{
			Timestamp:       nowMs,
			Flag:            flagEvalFlag{Key: "test-flag"},
			FirstEvaluation: nowMs,
			LastEvaluation:  nowMs,
			EvaluationCount: 1000,
			// All optional fields intentionally absent
		}

		b, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("failed to marshal ultra-degraded event: %v", err)
		}

		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		// Required fields must be present
		for _, req := range []string{"timestamp", "flag", "first_evaluation", "last_evaluation", "evaluation_count"} {
			if _, ok := m[req]; !ok {
				t.Errorf("ultra-degraded tier: required field %q missing", req)
			}
		}

		// Optional fields must be absent
		for _, opt := range []string{"targeting_key", "variant", "allocation", "targeting_rule", "error", "context", "runtime_default_used"} {
			if _, ok := m[opt]; ok {
				t.Errorf("ultra-degraded tier: optional field %q should be absent", opt)
			}
		}
	})

	t.Run("first_evaluation and last_evaluation meet minimum constraint", func(t *testing.T) {
		// Schema minimum: 1759276800000 (2025-08-01 Unix ms)
		// Using time.Now().UnixMilli() always satisfies this.
		const schemaMin int64 = 1759276800000
		if nowMs < schemaMin {
			t.Errorf("time.Now().UnixMilli() = %d is below schema minimum %d; use current timestamps only", nowMs, schemaMin)
		}
	})

	t.Run("batch payload wraps events in flagEvaluations array", func(t *testing.T) {
		payload := flagEvaluationPayload{
			Context: flagEvalDDContext{
				Service: "test-service",
				Env:     "test",
				Version: "1.0.0",
			},
			FlagEvaluations: []flagEvaluationEvent{
				{
					Timestamp:       nowMs,
					Flag:            flagEvalFlag{Key: "test-flag"},
					FirstEvaluation: nowMs,
					LastEvaluation:  nowMs,
					EvaluationCount: 1,
				},
			},
		}

		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("failed to marshal batch payload: %v", err)
		}

		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if _, ok := m["context"]; !ok {
			t.Error("batch payload: context missing")
		}
		if _, ok := m["flagEvaluations"]; !ok {
			t.Error("batch payload: flagEvaluations array missing")
		}
		if arr, ok := m["flagEvaluations"].([]any); !ok || len(arr) != 1 {
			t.Errorf("batch payload: expected 1 flagEvaluations entry, got %v", m["flagEvaluations"])
		}
	})
}

// TestAggregatorCollisionSafety verifies that two distinct inputs that would collide
// under FNV-1a-only map keying land in SEPARATE buckets under the struct-keyed map.
//
// It must fail RED: aggregator.add panics with "not implemented".
func TestAggregatorCollisionSafety(t *testing.T) {
	agg := setupTestAggregator(t)
	nowMs := time.Now().UnixMilli()

	// Two evaluations that differ only in allocationKey — they must be in separate full buckets.
	// Under FNV-1a alone (on a concatenated string), carefully crafted keys can collide;
	// under a struct-keyed map, these are structurally distinct and cannot collide.
	d1 := evalDetails{
		flagKey:       "my-flag",
		variant:       "on",
		allocationKey: "alloc-a",
		reason:        "targeting_match",
	}
	d2 := evalDetails{
		flagKey:       "my-flag",
		variant:       "on",
		allocationKey: "alloc-b",
		reason:        "targeting_match",
	}

	agg.add(d1, nil, nowMs)
	agg.add(d2, nil, nowMs)

	agg.mu.Lock()
	defer agg.mu.Unlock()

	if len(agg.full) != 2 {
		t.Errorf("expected 2 separate full-tier buckets for distinct allocationKeys, got %d", len(agg.full))
	}

	// A second add with d1 must increment the existing bucket, not create a third
	agg.mu.Unlock()
	agg.add(d1, nil, nowMs)
	agg.mu.Lock()

	if len(agg.full) != 2 {
		t.Errorf("re-adding d1 must increment existing bucket, not create new one; got %d buckets", len(agg.full))
	}
}

// TestAggregatorConcurrentMinMax verifies that 1000 goroutines recording the same key
// produce count==1000 and firstEvaluation<=lastEvaluation.
// Must be run with -race to satisfy the race-free requirement.
func TestAggregatorConcurrentMinMax(t *testing.T) {
	agg := &flagEvaluationAggregator{
		full:        make(map[evaluationAggregationKey]*evaluationEntry),
		degraded:    make(map[evaluationDegradedKey]*evaluationEntry),
		ultraDeg:    make(map[evaluationUltraDegradedKey]*evaluationEntry),
		perFlagFull: make(map[string]int),
		globalCap:   100_000, // large enough not to overflow during this test
		perFlagCap:  100_000,
		degradedCap: 100_000,
	}

	d := evalDetails{
		flagKey: "concurrent-flag",
		variant: "on",
		reason:  "targeting_match",
	}

	const goroutines = 1000
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			nowMs := time.Now().UnixMilli()
			agg.add(d, nil, nowMs)
		}()
	}
	wg.Wait()

	agg.mu.Lock()
	defer agg.mu.Unlock()

	if len(agg.full) != 1 {
		t.Fatalf("expected exactly 1 full-tier bucket, got %d", len(agg.full))
	}

	for _, entry := range agg.full {
		if entry.count != goroutines {
			t.Errorf("expected count=%d, got %d", goroutines, entry.count)
		}
		if entry.firstEvaluation > entry.lastEvaluation {
			t.Errorf("firstEvaluation=%d > lastEvaluation=%d — min/max invariant violated",
				entry.firstEvaluation, entry.lastEvaluation)
		}
	}
}

// TestSaturationCountPreservation is the regression guard against a silent drop at
// globalCap overflow. It asserts that the sum of all evaluation counts across ALL tiers
// (full + degraded + ultra-degraded) equals the total number of add() calls, even after
// BOTH globalCap AND perFlagCap have been exhausted.
//
// This test MUST FAIL on the pre-fix code (negative control proving the silent drop), and
// MUST PASS after rerouting the globalCap-overflow return into the ultra-degraded tier.
func TestSaturationCountPreservation(t *testing.T) {
	// Use small caps so we can saturate them quickly.
	// globalCap=5 means only 5 full-tier buckets ever created.
	// perFlagCap=2 means after 2 distinct full-tier buckets per flag, it overflows to degraded.
	// degradedCap=3 means only 3 degraded buckets; further overflow goes to ultra-degraded.
	agg := &flagEvaluationAggregator{
		full:        make(map[evaluationAggregationKey]*evaluationEntry),
		degraded:    make(map[evaluationDegradedKey]*evaluationEntry),
		ultraDeg:    make(map[evaluationUltraDegradedKey]*evaluationEntry),
		perFlagFull: make(map[string]int),
		globalCap:   5,
		perFlagCap:  2,
		degradedCap: 3,
	}
	nowMs := time.Now().UnixMilli()

	// We drive 100 distinct evaluations. Each call to add() must contribute exactly 1
	// count unit to one of the three tiers. After all calls, the Σ must equal 100.
	//
	// Strategy: use 20 different flag keys × 5 distinct allocationKey combos so that:
	//  - The first 2 per flag go into the full tier (perFlagCap=2).
	//  - The next ones overflow to degraded (bounded by degradedCap=3).
	//  - Once degraded is also full, overflow to ultra-degraded.
	//  - Once globalCap(5) is hit, any flag's attempt-count not yet at perFlagCap routes
	//    through the globalCap branch — the defect being guarded against is that this branch
	//    returns silently instead of routing to ultra-degraded.
	const totalCalls = 100
	for i := range totalCalls {
		flagIdx := i % 20
		allocIdx := i % 5
		d := evalDetails{
			flagKey:       fmt.Sprintf("sat-flag-%d", flagIdx),
			variant:       "on",
			allocationKey: fmt.Sprintf("alloc-%d", allocIdx),
			reason:        "targeting_match",
			targetingKey:  fmt.Sprintf("user-%d", i%10),
		}
		agg.add(d, nil, nowMs)
	}

	// Sum counts across all three tiers.
	agg.mu.Lock()
	defer agg.mu.Unlock()

	var totalCounted int64
	for _, e := range agg.full {
		totalCounted += e.count
	}
	for _, e := range agg.degraded {
		totalCounted += e.count
	}
	for _, e := range agg.ultraDeg {
		totalCounted += e.count
	}

	if totalCounted != totalCalls {
		t.Errorf(
			"count preservation violated: Σ(full+degraded+ultraDeg)=%d, expected=%d (add() calls); "+
				"silent drops detected (full buckets=%d, degraded buckets=%d, ultraDeg buckets=%d)",
			totalCounted, totalCalls,
			len(agg.full), len(agg.degraded), len(agg.ultraDeg),
		)
	}
}

// TestAggregatorCapOverflow verifies that:
//   - Exceeding perFlagCap routes new entries to the degraded map.
//   - Exceeding degradedCap routes new entries to the ultra-degraded map.
//   - Global cap bounds total bucket growth.
func TestAggregatorCapOverflow(t *testing.T) {
	t.Run("perFlagCap overflow routes to degraded", func(t *testing.T) {
		agg := setupTestAggregator(t) // perFlagCap=3
		nowMs := time.Now().UnixMilli()

		// Fill perFlagCap (3) full-tier buckets for "flag-a"
		for i := range 3 {
			d := evalDetails{
				flagKey:       "flag-a",
				variant:       "on",
				allocationKey: fmt.Sprintf("alloc-%d", i),
				reason:        "targeting_match",
				targetingKey:  fmt.Sprintf("user-%d", i),
			}
			agg.add(d, map[string]any{"key": fmt.Sprintf("v%d", i)}, nowMs)
		}

		agg.mu.Lock()
		if agg.perFlagFull["flag-a"] != 3 {
			t.Errorf("expected perFlagFull[flag-a]=3, got %d", agg.perFlagFull["flag-a"])
		}
		agg.mu.Unlock()

		// The 4th distinct entry for "flag-a" must overflow to degraded
		d4 := evalDetails{
			flagKey:       "flag-a",
			variant:       "on",
			allocationKey: "alloc-overflow",
			reason:        "targeting_match",
			targetingKey:  "user-overflow",
		}
		agg.add(d4, map[string]any{"extra": "data"}, nowMs)

		agg.mu.Lock()
		defer agg.mu.Unlock()

		if len(agg.degraded) == 0 {
			t.Error("expected at least one degraded bucket after perFlagCap overflow")
		}
	})

	t.Run("degradedCap overflow routes to ultra-degraded", func(t *testing.T) {
		agg := setupTestAggregator(t) // degradedCap=3
		nowMs := time.Now().UnixMilli()

		// Pre-fill the degraded map to capacity by forcing overflow from full tier.
		// Use different variants to get 3 distinct degraded buckets.
		for i := range 4 { // 4 fills full to cap=3 then overflows once
			for j := range 3 { // perFlagCap=3; 4 distinct allocs per flag => overflow on 4th
				d := evalDetails{
					flagKey:       fmt.Sprintf("flag-%d", i),
					variant:       fmt.Sprintf("v%d", j),
					allocationKey: fmt.Sprintf("alloc-%d", j),
					reason:        "targeting_match",
					targetingKey:  fmt.Sprintf("user-%d", j),
				}
				agg.add(d, nil, nowMs)
			}
		}

		// Continue adding until degradedCap is also exhausted.
		// At that point, ultra-degraded must receive new entries.
		for i := range 10 {
			d := evalDetails{
				flagKey: fmt.Sprintf("overflow-flag-%d", i),
				variant: "on",
				reason:  "targeting_match",
			}
			// Force each into degraded by also filling its full tier
			for j := range 4 {
				d2 := evalDetails{
					flagKey:       d.flagKey,
					variant:       d.variant,
					allocationKey: fmt.Sprintf("a%d", j),
					reason:        d.reason,
				}
				agg.add(d2, nil, nowMs)
			}
		}

		agg.mu.Lock()
		defer agg.mu.Unlock()

		if len(agg.ultraDeg) == 0 {
			t.Error("expected ultra-degraded buckets after degradedCap overflow")
		}
	})

	t.Run("globalCap bounds full-tier bucket growth only", func(t *testing.T) {
		agg := setupTestAggregator(t) // globalCap=10, perFlagCap=3, degradedCap=3
		nowMs := time.Now().UnixMilli()

		// Add 50 distinct evaluations (each a unique flag key).
		// globalCap=10 caps the full tier; overflow goes to ultra-degraded.
		// The full tier must stay at or below globalCap.
		// The total count across all tiers must equal the number of add() calls
		// (no silent drops).
		const calls = 50
		for i := range calls {
			d := evalDetails{
				flagKey: fmt.Sprintf("flag-%d", i),
				variant: "on",
				reason:  "targeting_match",
			}
			agg.add(d, nil, nowMs)
		}

		agg.mu.Lock()
		defer agg.mu.Unlock()

		// Full tier must be bounded by globalCap.
		if agg.globalCount > agg.globalCap {
			t.Errorf("full-tier globalCount %d exceeds globalCap %d", agg.globalCount, agg.globalCap)
		}
		if len(agg.full) > agg.globalCap {
			t.Errorf("full-tier buckets %d exceeds globalCap %d", len(agg.full), agg.globalCap)
		}

		// Every add() call must have produced a count unit in some tier (no silent drops).
		var totalCounted int64
		for _, e := range agg.full {
			totalCounted += e.count
		}
		for _, e := range agg.degraded {
			totalCounted += e.count
		}
		for _, e := range agg.ultraDeg {
			totalCounted += e.count
		}
		if totalCounted != calls {
			t.Errorf("count preservation violated: Σ(all tiers)=%d, expected=%d", totalCounted, calls)
		}
	})
}
