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

	of "github.com/open-feature/go-sdk/openfeature"
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
		ultraDegCap: defaultEvalUltraDegradedCap, // real default keeps the multi-tier cascade from collapsing into the sentinel
	}
}

// newTestAggregator builds a flagEvaluationAggregator with explicit, caller-supplied caps.
// Unlike setupTestAggregator (which fixes small caps), each cap is a parameter so a test can
// drive a specific tier-overflow scenario. The cap NUMBERS are load-bearing — callers pass
// the exact values their scenario requires.
func newTestAggregator(globalCap, perFlagCap, degradedCap, ultraDegCap int) *flagEvaluationAggregator {
	return &flagEvaluationAggregator{
		full:        make(map[evaluationAggregationKey]*evaluationEntry),
		degraded:    make(map[evaluationDegradedKey]*evaluationEntry),
		ultraDeg:    make(map[evaluationUltraDegradedKey]*evaluationEntry),
		perFlagFull: make(map[string]int),
		globalCap:   globalCap,
		perFlagCap:  perFlagCap,
		degradedCap: degradedCap,
		ultraDegCap: ultraDegCap,
	}
}

// TestPruneContext verifies that pruneContext applies the 256-field / 256-char limits
// before evaluation context enters the aggregation buffer.
//
// It must fail RED: pruneContext panics with "not implemented".
func TestPruneContext(t *testing.T) {
	tests := []struct {
		name   string
		input  map[string]any
		assert func(t *testing.T, out map[string]any)
	}{
		{
			name: "300 fields truncated to exactly 256",
			input: func() map[string]any {
				raw := make(map[string]any, 300)
				for i := range 300 {
					raw[fmt.Sprintf("key%d", i)] = fmt.Sprintf("value%d", i)
				}
				return raw
			}(),
			assert: func(t *testing.T, out map[string]any) {
				if len(out) != 256 {
					t.Errorf("expected exactly 256 fields after prune, got %d", len(out))
				}
			},
		},
		{
			name: "string value exceeding 256 chars is dropped",
			input: map[string]any{
				"short": "ok",
				"long":  strings.Repeat("x", 300), // 300 chars > maxFieldLength(256)
			},
			assert: func(t *testing.T, out map[string]any) {
				if _, ok := out["long"]; ok {
					t.Error("expected long string value to be dropped from pruned context")
				}
				if _, ok := out["short"]; !ok {
					t.Error("expected short string value to be retained in pruned context")
				}
			},
		},
		{
			name:  "nil input returns nil",
			input: nil,
			assert: func(t *testing.T, out map[string]any) {
				if out != nil {
					t.Errorf("expected nil for nil input, got %v", out)
				}
			},
		},
		{
			name:  "empty input returns nil or empty",
			input: map[string]any{},
			assert: func(t *testing.T, out map[string]any) {
				if out != nil && len(out) != 0 {
					t.Errorf("expected nil or empty for empty input, got %v", out)
				}
			},
		},
		{
			name: "non-string values are retained regardless of notional length",
			input: map[string]any{
				"intVal":  42,
				"boolVal": true,
			},
			assert: func(t *testing.T, out map[string]any) {
				if out["intVal"] == nil {
					t.Error("expected integer value to be retained")
				}
				if out["boolVal"] == nil {
					t.Error("expected boolean value to be retained")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t, pruneContext(tc.input))
		})
	}
}

// TestFlagEvaluationPayloadSchema verifies that full, degraded, and ultra-degraded events
// marshal to JSON that omits the expected optional fields per tier while always including
// the 5 required fields.
//
// It must fail RED: the aggregator.add method panics with "not implemented".
func TestFlagEvaluationPayloadSchema(t *testing.T) {
	nowMs := time.Now().UnixMilli()

	requiredFields := []string{"timestamp", "flag", "first_evaluation", "last_evaluation", "evaluation_count"}

	tierTests := []struct {
		name           string
		event          flagEvaluationEvent
		requiredFlgKey bool     // full tier additionally asserts flag.key is present
		optionalAbsent []string // optional fields that must NOT appear for this tier
	}{
		{
			name: "full tier has all required fields",
			event: flagEvaluationEvent{
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
			},
			requiredFlgKey: true,
		},
		{
			name: "degraded tier omits targeting_key and context.evaluation",
			// Degraded tier: no targeting_key, no context.evaluation; variant + allocation present.
			event: flagEvaluationEvent{
				Timestamp:       nowMs,
				Flag:            flagEvalFlag{Key: "test-flag"},
				FirstEvaluation: nowMs,
				LastEvaluation:  nowMs,
				EvaluationCount: 5,
				Variant:         &flagEvalVariant{Key: "on"},
				Allocation:      &flagEvalAllocation{Key: "default"},
				// TargetingKey / Context intentionally absent.
			},
			optionalAbsent: []string{"targeting_key", "context"},
		},
		{
			name: "ultra-degraded tier has only required fields",
			// Ultra-degraded: only flag key + counts; no variant, allocation, targeting, context.
			event: flagEvaluationEvent{
				Timestamp:       nowMs,
				Flag:            flagEvalFlag{Key: "test-flag"},
				FirstEvaluation: nowMs,
				LastEvaluation:  nowMs,
				EvaluationCount: 1000,
				// All optional fields intentionally absent.
			},
			optionalAbsent: []string{"targeting_key", "variant", "allocation", "targeting_rule", "error", "context", "runtime_default_used"},
		},
	}

	for _, tc := range tierTests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.event)
			if err != nil {
				t.Fatalf("failed to marshal event: %v", err)
			}
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			for _, req := range requiredFields {
				if _, ok := m[req]; !ok {
					t.Errorf("required field %q missing from marshaled JSON", req)
				}
			}
			if tc.requiredFlgKey {
				if flagObj, ok := m["flag"].(map[string]any); !ok {
					t.Error("flag is not an object")
				} else if _, ok := flagObj["key"]; !ok {
					t.Error("flag.key missing")
				}
			}
			for _, opt := range tc.optionalAbsent {
				if _, ok := m[opt]; ok {
					t.Errorf("optional field %q should be absent", opt)
				}
			}
		})
	}

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
	// Caps large enough not to overflow during this test.
	agg := newTestAggregator(100_000, 100_000, 100_000, defaultEvalUltraDegradedCap)

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
	agg := newTestAggregator(5, 2, 3, defaultEvalUltraDegradedCap)
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

// TestPruneContextDeterministic guards Finding #1 (nondeterministic context pruning).
// A >256-field context must prune to an IDENTICAL kept subset (and therefore an identical
// hash) on every call, and two independently-built maps with the same logical entries must
// hash equal. On the pre-fix code (map-range cap BEFORE sort) the kept subset is random, so
// the hash varies across iterations and identical logical contexts fragment into separate
// buckets.
func TestPruneContextDeterministic(t *testing.T) {
	const fields = 400 // > maxContextFields (256)
	build := func() map[string]any {
		m := make(map[string]any, fields)
		for i := range fields {
			m[fmt.Sprintf("key%04d", i)] = fmt.Sprintf("value%04d", i)
		}
		return m
	}

	first := hashContext(pruneContext(build()))
	for range 50 {
		got := hashContext(pruneContext(build()))
		if got != first {
			t.Fatalf("pruneContext+hashContext nondeterministic over >256 fields: got %d, want %d", got, first)
		}
	}

	// Two independently-built maps with the SAME 400 logical entries must hash equal.
	if a, b := hashContext(pruneContext(build())), hashContext(pruneContext(build())); a != b {
		t.Errorf("identical logical contexts hashed differently: %d != %d", a, b)
	}
}

// TestPruneContextOversizedStringDeterministic guards Finding #1 with an oversized-string
// skip among >256 fields: the oversized-string skip must be applied against a deterministic
// key ordering, so the kept subset (and hash) is stable across iterations.
func TestPruneContextOversizedStringDeterministic(t *testing.T) {
	const fields = 400
	longVal := strings.Repeat("x", maxFieldLength+44) // > maxFieldLength → skipped
	build := func() map[string]any {
		m := make(map[string]any, fields)
		for i := range fields {
			m[fmt.Sprintf("key%04d", i)] = fmt.Sprintf("value%04d", i)
		}
		m["zzz-oversized"] = longVal // sorts last; deterministically skipped
		return m
	}

	first := hashContext(pruneContext(build()))
	for range 50 {
		got := hashContext(pruneContext(build()))
		if got != first {
			t.Fatalf("oversized-string prune nondeterministic: got %d, want %d", got, first)
		}
	}

	// The oversized value must never appear in the pruned subset.
	pruned := pruneContext(build())
	if _, ok := pruned["zzz-oversized"]; ok {
		t.Error("oversized string value should be skipped from pruned context")
	}
}

// TestHashContextCanonicalEncoding guards Finding #2 (non-canonical context hash encoding).
// Distinct contexts must not alias: int 1 vs string "1" must differ, and '='/'\n'-bearing
// values/keys must not be able to fake a multi-field context. Drives a subset through
// aggregator.add and asserts SEPARATE full-tier buckets.
func TestHashContextCanonicalEncoding(t *testing.T) {
	// Distinct contexts must hash differently — no aliasing across type or delimiter tricks.
	inequalityTests := []struct {
		name       string
		mapA, mapB map[string]any
	}{
		{
			// Type-tagged encoding must distinguish int 1 from string "1".
			name: "int 1 != string 1",
			mapA: map[string]any{"x": 1},
			mapB: map[string]any{"x": "1"},
		},
		{
			// {"a=b":"c"} vs {"a":"b=c"} render identically under key+"="+value with no
			// length delimiter; canonical encoding must keep them distinct.
			name: "'=' in key/value cannot alias a multi-field context",
			mapA: map[string]any{"a=b": "c"},
			mapB: map[string]any{"a": "b=c"},
		},
		{
			// Under key+"="+value+"\n", a newline in a value would collide with a two-field map.
			name: "'\\n' in value cannot alias a multi-field context",
			mapA: map[string]any{"a": "x\ny", "b": "z"},
			mapB: map[string]any{"a": "x", "y=z": ""},
		},
	}

	for _, tc := range inequalityTests {
		t.Run(tc.name, func(t *testing.T) {
			if hashContext(tc.mapA) == hashContext(tc.mapB) {
				t.Errorf("canonical encoding must distinguish %v from %v", tc.mapA, tc.mapB)
			}
		})
	}

	t.Run("distinct contexts land in separate full-tier buckets via add()", func(t *testing.T) {
		agg := setupTestAggregator(t)
		nowMs := time.Now().UnixMilli()
		d := evalDetails{flagKey: "f", variant: "on", reason: "targeting_match"}
		agg.add(d, map[string]any{"x": 1}, nowMs)
		agg.add(d, map[string]any{"x": "1"}, nowMs)

		agg.mu.Lock()
		defer agg.mu.Unlock()
		if len(agg.full) != 2 {
			t.Errorf("expected 2 full-tier buckets for int 1 vs string \"1\", got %d", len(agg.full))
		}
	})
}

// TestUltraDegradedCapBounded guards Finding #3 (ultra-degraded tier unbounded). With a
// small positive ultraDegCap, many distinct flag keys must NOT grow ultraDeg without bound:
// len(ultraDeg) <= ultraDegCap+1 (the +1 is the sentinel overflow bucket) and counts are
// preserved (Σ across tiers == add() calls).
func TestUltraDegradedCapBounded(t *testing.T) {
	const cap = 3
	// globalCap=0 and degradedCap=0 force everything straight into the ultra-degraded tier.
	agg := newTestAggregator(0, 100_000, 0, cap)
	nowMs := time.Now().UnixMilli()

	const calls = 100
	for i := range calls {
		d := evalDetails{
			flagKey: fmt.Sprintf("dynamic-flag-%d", i), // every key distinct
			variant: "on",
			reason:  "targeting_match",
		}
		agg.add(d, nil, nowMs)
	}

	agg.mu.Lock()
	defer agg.mu.Unlock()

	if len(agg.ultraDeg) > cap+1 {
		t.Errorf("ultra-degraded cardinality %d exceeds ultraDegCap+1 (%d) — tier not bounded", len(agg.ultraDeg), cap+1)
	}

	var total int64
	for _, e := range agg.full {
		total += e.count
	}
	for _, e := range agg.degraded {
		total += e.count
	}
	for _, e := range agg.ultraDeg {
		total += e.count
	}
	if total != calls {
		t.Errorf("count preservation violated under ultraDegCap: Σ(all tiers)=%d, expected=%d", total, calls)
	}

	// The sentinel overflow bucket must be present and carry the over-cap counts.
	if _, ok := agg.ultraDeg[evaluationUltraDegradedKey{flagKey: ultraDegradedOverflowFlagKey}]; !ok {
		t.Errorf("expected sentinel overflow bucket %q in ultra-degraded tier", ultraDegradedOverflowFlagKey)
	}
}

// TestRecordAfterStopIsNoop guards Finding #4 (record() can enqueue after shutdown). After
// stop(), record() must NOT enqueue into the never-drained events channel; the event must be
// counted as dropped instead. On the pre-fix code (no stopped check in record()) the event is
// enqueued and silently lost.
func TestRecordAfterStopIsNoop(t *testing.T) {
	w := newFlagEvaluationWriter(ProviderConfig{})

	// Do NOT start the worker. stop() must still be safe (ticker==nil path) and mark stopped.
	w.stop()

	before := w.dropped.Load()

	// Build a minimal hook context + details to drive record().
	hookCtx := of.NewHookContext(
		"test-flag",
		of.Boolean,
		false,
		of.NewClientMetadata(""),
		of.Metadata{Name: "test-provider"},
		of.NewEvaluationContext("user-1", nil),
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

	w.record(hookCtx, details)

	if got := len(w.events); got != 0 {
		t.Errorf("record() after stop() enqueued %d event(s); expected 0 (no-op into never-drained channel)", got)
	}
	if got := w.dropped.Load(); got != before+1 {
		t.Errorf("record() after stop() must count the event as dropped: dropped=%d, expected=%d", got, before+1)
	}
}

// TestExtractEvalDetailsPrefersErrorMessage guards Finding #5 (error payload uses ErrorCode
// instead of OpenFeature's ErrorMessage). ErrorMessage is preferred when present; ErrorCode is
// the fallback only when ErrorMessage is empty.
func TestExtractEvalDetailsPrefersErrorMessage(t *testing.T) {
	mkHookCtx := func() of.HookContext {
		return of.NewHookContext(
			"test-flag",
			of.Boolean,
			false,
			of.NewClientMetadata(""),
			of.Metadata{Name: "test-provider"},
			of.NewEvaluationContext("user-1", nil),
		)
	}

	tests := []struct {
		name             string
		details          of.InterfaceEvaluationDetails
		wantErrorMessage string
	}{
		{
			name: "ErrorMessage present is preferred over ErrorCode",
			details: of.InterfaceEvaluationDetails{
				EvaluationDetails: of.EvaluationDetails{
					ResolutionDetail: of.ResolutionDetail{
						Reason:       of.ErrorReason,
						ErrorCode:    of.GeneralCode,
						ErrorMessage: "boom",
					},
				},
			},
			wantErrorMessage: "boom",
		},
		{
			name: "empty ErrorMessage falls back to ErrorCode",
			details: of.InterfaceEvaluationDetails{
				EvaluationDetails: of.EvaluationDetails{
					ResolutionDetail: of.ResolutionDetail{
						Reason:    of.ErrorReason,
						ErrorCode: of.TypeMismatchCode,
					},
				},
			},
			wantErrorMessage: string(of.TypeMismatchCode),
		},
		{
			name: "both empty yields empty errorMessage",
			details: of.InterfaceEvaluationDetails{
				EvaluationDetails: of.EvaluationDetails{
					ResolutionDetail: of.ResolutionDetail{
						Reason: of.TargetingMatchReason,
					},
				},
			},
			wantErrorMessage: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractEvalDetails(mkHookCtx(), tc.details).errorMessage; got != tc.wantErrorMessage {
				t.Errorf("errorMessage = %q, want %q", got, tc.wantErrorMessage)
			}
		})
	}
}
