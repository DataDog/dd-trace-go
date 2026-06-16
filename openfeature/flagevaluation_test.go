// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"

	of "github.com/open-feature/go-sdk/openfeature"
)

func validateBatchedFlagEvaluationsSchema(t *testing.T, payload any) error {
	t.Helper()

	schemaBytes, err := os.ReadFile("testdata/flageval-worker/batchedflagevaluations.json")
	if err != nil {
		t.Fatalf("failed to read flageval-worker schema fixture: %v", err)
	}

	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("batchedflagevaluations.json", strings.NewReader(string(schemaBytes))); err != nil {
		t.Fatalf("failed to add flageval-worker schema fixture: %v", err)
	}
	schema, err := compiler.Compile("batchedflagevaluations.json")
	if err != nil {
		t.Fatalf("failed to compile flageval-worker schema fixture: %v", err)
	}

	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload for schema validation: %v", err)
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("failed to unmarshal payload for schema validation: %v", err)
	}
	return schema.Validate(doc)
}

// TestFlattenAndPruneContextEquivalence verifies the merged single-pass
// flattenAndPruneContext must produce a pruned result byte-for-byte identical to the prior
// two-step flattenContext + pruneContext pipeline across nested, oversized, and >256-field
// inputs (and the determinism + 256/256 limits are preserved).
func TestFlattenAndPruneContextEquivalence(t *testing.T) {
	bigFields := func() map[string]any {
		m := make(map[string]any, 400)
		for i := range 400 {
			m[fmt.Sprintf("key%04d", i)] = fmt.Sprintf("value%04d", i)
		}
		return m
	}

	cases := []struct {
		name  string
		input map[string]any
	}{
		{
			name:  "nested objects flatten to dot notation",
			input: map[string]any{"user": map[string]any{"id": "123", "email": "a@b.com"}, "country": "US"},
		},
		{
			name:  "deeply nested + arrays",
			input: map[string]any{"a": map[string]any{"b": map[string]any{"c": 1}}, "tags": []string{"x", "y", "z"}},
		},
		{
			name:  "oversized string value is skipped",
			input: map[string]any{"short": "ok", "long": strings.Repeat("x", maxFieldLength+10)},
		},
		{
			name:  "more than 256 fields truncated to 256",
			input: bigFields(),
		},
		{
			name:  "nested oversized string among many fields",
			input: map[string]any{"u": map[string]any{"bio": strings.Repeat("y", maxFieldLength+1), "id": 7}},
		},
		{
			name:  "mixed scalar types retained",
			input: map[string]any{"i": 42, "b": true, "f": 3.14, "s": "hi"},
		},
		{
			name:  "empty input",
			input: map[string]any{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Reference pipeline (the two former steps) vs the merged single-pass procedure.
			want := pruneContext(flattenContext(tc.input))
			got := flattenAndPruneContext(tc.input)

			if !reflect.DeepEqual(got, want) {
				t.Errorf("merged flatten+prune differs from flattenContext+pruneContext:\n got=%v\nwant=%v", got, want)
			}

			// 256-field limit preserved.
			if len(got) > maxContextFields {
				t.Errorf("merged result has %d fields, exceeds maxContextFields %d", len(got), maxContextFields)
			}

			// Determinism: repeated calls yield an identical canonical key.
			first := canonicalContextKey(flattenAndPruneContext(tc.input))
			for range 25 {
				if k := canonicalContextKey(flattenAndPruneContext(tc.input)); k != first {
					t.Fatalf("merged flatten+prune nondeterministic: canonical keys differ across calls")
				}
			}
		})
	}
}

// setupTestAggregator creates a flagEvaluationAggregator with small caps for testing.
// Caps are deliberately small to trigger tier-cascade behavior in unit tests.
func setupTestAggregator(t *testing.T) *flagEvaluationAggregator {
	t.Helper()
	return &flagEvaluationAggregator{
		full:        make(map[evaluationAggregationKey]*evaluationEntry),
		degraded:    make(map[evaluationDegradedKey]*evaluationEntry),
		perFlagFull: make(map[string]int),
		globalCap:   10, // small cap to trigger overflow in tests
		perFlagCap:  3,
		degradedCap: 3,
	}
}

// newTestAggregator builds a flagEvaluationAggregator with explicit, caller-supplied caps.
// Unlike setupTestAggregator (which fixes small caps), each cap is a parameter so a test can
// drive a specific tier-overflow scenario. The cap NUMBERS are load-bearing — callers pass
// the exact values their scenario requires.
func newTestAggregator(globalCap, perFlagCap, degradedCap int) *flagEvaluationAggregator {
	return &flagEvaluationAggregator{
		full:        make(map[evaluationAggregationKey]*evaluationEntry),
		degraded:    make(map[evaluationDegradedKey]*evaluationEntry),
		perFlagFull: make(map[string]int),
		globalCap:   globalCap,
		perFlagCap:  perFlagCap,
		degradedCap: degradedCap,
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

// TestFlagEvaluationPayloadSchema verifies that full, degraded, and required-only events
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
			name: "required-only event omits all optional fields",
			// A bare event carrying only flag key + counts; no variant, allocation, targeting,
			// context. (This shape is not emitted by a dedicated tier, but the
			// schema must still accept a required-fields-only event.)
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

	t.Run("batch payload validates against real flageval-worker schema", func(t *testing.T) {
		payload := flagEvaluationPayload{
			Context: flagEvalDDContext{
				Service: "test-service",
				Env:     "test",
				Version: "1.0.0",
			},
			FlagEvaluations: []flagEvaluationEvent{
				{
					Timestamp:       nowMs,
					Flag:            flagEvalFlag{Key: "full-flag"},
					FirstEvaluation: nowMs,
					LastEvaluation:  nowMs,
					EvaluationCount: 2,
					RuntimeDefault:  true,
					TargetingKey:    "user-123",
					Variant:         &flagEvalVariant{Key: "on"},
					Allocation:      &flagEvalAllocation{Key: "alloc-a"},
					Error:           &flagEvalError{Message: string(of.TypeMismatchCode)},
					Context: &flagEvalEventContext{
						Evaluation: map[string]any{"country": "US", "plan": "pro"},
					},
				},
				{
					Timestamp:       nowMs,
					Flag:            flagEvalFlag{Key: "degraded-flag"},
					FirstEvaluation: nowMs,
					LastEvaluation:  nowMs,
					EvaluationCount: 5,
					Variant:         &flagEvalVariant{Key: "off"},
					Allocation:      &flagEvalAllocation{Key: "alloc-b"},
					Error:           &flagEvalError{Message: string(of.FlagNotFoundCode)},
				},
				{
					Timestamp:       nowMs,
					Flag:            flagEvalFlag{Key: "required-only-flag"},
					FirstEvaluation: nowMs,
					LastEvaluation:  nowMs,
					EvaluationCount: 1,
				},
			},
		}

		if err := validateBatchedFlagEvaluationsSchema(t, payload); err != nil {
			t.Fatalf("payload should validate against flageval-worker schema: %v", err)
		}
	})

	t.Run("worker schema rejects top-level reason", func(t *testing.T) {
		payload := map[string]any{
			"context": map[string]any{"service": "test-service"},
			"flagEvaluations": []map[string]any{{
				"timestamp":        nowMs,
				"flag":             map[string]any{"key": "test-flag"},
				"first_evaluation": nowMs,
				"last_evaluation":  nowMs,
				"evaluation_count": 1,
				"reason":           "targeting_match",
			}},
		}

		if err := validateBatchedFlagEvaluationsSchema(t, payload); err == nil {
			t.Fatal("payload with top-level reason should be rejected by flageval-worker schema")
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
	}
	d2 := evalDetails{
		flagKey:       "my-flag",
		variant:       "on",
		allocationKey: "alloc-b",
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

func TestOpenFeatureReasonIsNotEVPCardinality(t *testing.T) {
	w := newFlagEvaluationWriter(ProviderConfig{})
	hookCtx := of.NewHookContext(
		"reasonless-flag",
		of.Boolean,
		false,
		of.NewClientMetadata(""),
		of.Metadata{Name: "test-provider"},
		of.NewEvaluationContext("user-1", map[string]any{"country": "US"}),
	)
	metadata := of.FlagMetadata{metadataAllocationKey: "alloc-a"}
	detailsA := makeEvalDetails("on", of.TargetingMatchReason, "", metadata)
	detailsB := makeEvalDetails("on", of.SplitReason, "", metadata)

	w.record(hookCtx, detailsA)
	w.record(hookCtx, detailsB)
	for len(w.events) > 0 {
		w.aggregate(<-w.events)
	}

	w.aggregator.mu.Lock()
	defer w.aggregator.mu.Unlock()
	if len(w.aggregator.full) != 1 {
		t.Fatalf("reason-only differences must not split EVP buckets; got %d full buckets", len(w.aggregator.full))
	}
	for _, e := range w.aggregator.full {
		if e.count != 2 {
			t.Fatalf("reason-only differences should aggregate into count=2, got %d", e.count)
		}
	}
}

// TestAggregatorConcurrentMinMax verifies that 1000 goroutines recording the same key
// produce count==1000 and firstEvaluation<=lastEvaluation.
// Must be run with -race to satisfy the race-free requirement.
func TestAggregatorConcurrentMinMax(t *testing.T) {
	// Caps large enough not to overflow during this test.
	agg := newTestAggregator(100_000, 100_000, 100_000)

	d := evalDetails{
		flagKey: "concurrent-flag",
		variant: "on",
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

// TestSaturationCountPreservation is the regression guard against a SILENT drop at saturation
// in the 2-tier design. Because the degraded tier is now terminal (overflow is dropped, not
// cascaded), the invariant is: Σ(full+degraded counts) + droppedDegradedOverflow == add() calls.
// No evaluation may vanish without being COUNTED — silent loss is the defect this guards against.
func TestSaturationCountPreservation(t *testing.T) {
	// Use small caps so we can saturate them quickly.
	// globalCap=5 means only 5 full-tier buckets ever created.
	// perFlagCap=2 means after 2 distinct full-tier buckets per flag, it overflows to degraded.
	// degradedCap=3 means only 3 degraded buckets; further overflow is dropped(counted).
	agg := newTestAggregator(5, 2, 3)
	nowMs := time.Now().UnixMilli()

	// Drive 100 distinct evaluations. Each add() must contribute exactly 1 count unit to either
	// the full tier, the degraded tier, or the droppedDegradedOverflow counter. After all calls,
	// Σ(full+degraded) + dropped must equal 100 — nothing silently lost.
	const totalCalls = 100
	for i := range totalCalls {
		flagIdx := i % 20
		allocIdx := i % 5
		d := evalDetails{
			flagKey:       fmt.Sprintf("sat-flag-%d", flagIdx),
			variant:       "on",
			allocationKey: fmt.Sprintf("alloc-%d", allocIdx),
			targetingKey:  fmt.Sprintf("user-%d", i%10),
		}
		agg.add(d, nil, nowMs)
	}

	// Sum counts across both tiers plus the observable drop counter.
	agg.mu.Lock()
	defer agg.mu.Unlock()

	var totalCounted int64
	for _, e := range agg.full {
		totalCounted += e.count
	}
	for _, e := range agg.degraded {
		totalCounted += e.count
	}
	totalCounted += agg.droppedDegradedOverflow

	if totalCounted != totalCalls {
		t.Errorf(
			"count preservation violated: Σ(full+degraded)+dropped=%d, expected=%d (add() calls); "+
				"silent drops detected (full buckets=%d, degraded buckets=%d, droppedDegradedOverflow=%d)",
			totalCounted, totalCalls,
			len(agg.full), len(agg.degraded), agg.droppedDegradedOverflow,
		)
	}
}

// TestAggregatorCapOverflow verifies that:
//   - Exceeding perFlagCap routes new entries to the degraded map.
//   - Exceeding degradedCap drops new entries and counts the drop.
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
			targetingKey:  "user-overflow",
		}
		agg.add(d4, map[string]any{"extra": "data"}, nowMs)

		agg.mu.Lock()
		defer agg.mu.Unlock()

		if len(agg.degraded) == 0 {
			t.Error("expected at least one degraded bucket after perFlagCap overflow")
		}
	})

	t.Run("degradedCap overflow is dropped and counted", func(t *testing.T) {
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
					targetingKey:  fmt.Sprintf("user-%d", j),
				}
				agg.add(d, nil, nowMs)
			}
		}

		// Continue adding until degradedCap is also exhausted. At that point — with no ultra
		// tier — new degraded buckets must be dropped and COUNTED (droppedDegradedOverflow).
		for i := range 10 {
			d := evalDetails{
				flagKey: fmt.Sprintf("overflow-flag-%d", i),
				variant: "on",
			}
			// Force each into degraded by also filling its full tier
			for j := range 4 {
				d2 := evalDetails{
					flagKey:       d.flagKey,
					variant:       d.variant,
					allocationKey: fmt.Sprintf("a%d", j),
				}
				agg.add(d2, nil, nowMs)
			}
		}

		agg.mu.Lock()
		defer agg.mu.Unlock()

		if len(agg.degraded) > agg.degradedCap {
			t.Errorf("degraded tier %d exceeds degradedCap %d — terminal tier not bounded", len(agg.degraded), agg.degradedCap)
		}
		if agg.droppedDegradedOverflow == 0 {
			t.Error("expected droppedDegradedOverflow > 0 after degradedCap exhaustion (drops must be counted, not silent)")
		}
	})

	t.Run("globalCap bounds full-tier bucket growth only", func(t *testing.T) {
		agg := setupTestAggregator(t) // globalCap=10, perFlagCap=3, degradedCap=3
		nowMs := time.Now().UnixMilli()

		// Add 50 distinct evaluations (each a unique flag key).
		// globalCap=10 caps the full tier; overflow cascades to degraded (then drops if degraded
		// is also full). The full tier must stay at or below globalCap, and the total count
		// across both tiers plus the drop counter must equal the number of add() calls.
		const calls = 50
		for i := range calls {
			d := evalDetails{
				flagKey: fmt.Sprintf("flag-%d", i),
				variant: "on",
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

		// Every add() call must have produced a count unit somewhere observable (no silent drops).
		var totalCounted int64
		for _, e := range agg.full {
			totalCounted += e.count
		}
		for _, e := range agg.degraded {
			totalCounted += e.count
		}
		totalCounted += agg.droppedDegradedOverflow
		if totalCounted != calls {
			t.Errorf("count preservation violated: Σ(full+degraded)+dropped=%d, expected=%d", totalCounted, calls)
		}
	})
}

// TestPruneContextDeterministic verifies deterministic context pruning.
// A >256-field context must prune to an IDENTICAL kept subset (and therefore an identical
// canonical key) on every call, and two independently-built maps with the same logical entries
// must produce an equal key. On the pre-fix code (map-range cap BEFORE sort) the kept subset is
// random, so the key varies across iterations and identical logical contexts fragment into
// separate buckets.
func TestPruneContextDeterministic(t *testing.T) {
	const fields = 400 // > maxContextFields (256)
	build := func() map[string]any {
		m := make(map[string]any, fields)
		for i := range fields {
			m[fmt.Sprintf("key%04d", i)] = fmt.Sprintf("value%04d", i)
		}
		return m
	}

	first := canonicalContextKey(pruneContext(build()))
	for range 50 {
		got := canonicalContextKey(pruneContext(build()))
		if got != first {
			t.Fatalf("pruneContext+canonicalContextKey nondeterministic over >256 fields: keys differ across iterations")
		}
	}

	// Two independently-built maps with the SAME 400 logical entries must produce an equal key.
	if a, b := canonicalContextKey(pruneContext(build())), canonicalContextKey(pruneContext(build())); a != b {
		t.Errorf("identical logical contexts produced different canonical keys")
	}
}

// TestPruneContextOversizedStringDeterministic verifies an oversized-string
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

	first := canonicalContextKey(pruneContext(build()))
	for range 50 {
		got := canonicalContextKey(pruneContext(build()))
		if got != first {
			t.Fatalf("oversized-string prune nondeterministic: canonical keys differ across iterations")
		}
	}

	// The oversized value must never appear in the pruned subset.
	pruned := pruneContext(build())
	if _, ok := pruned["zzz-oversized"]; ok {
		t.Error("oversized string value should be skipped from pruned context")
	}
}

// TestCanonicalContextKeyEncoding verifies that the comparable canonical-context key replaces
// the lossy FNV-1a discriminator). Distinct contexts must produce DISTINCT keys — int 1 vs
// string "1" must differ, and '='/'\n'-bearing values/keys must not fake a multi-field context.
// Because the full canonical encoding IS the map key (no hash), distinct contexts ALWAYS land
// in separate full-tier buckets via add() with ZERO misattribution.
func TestCanonicalContextKeyEncoding(t *testing.T) {
	// Distinct contexts must produce distinct keys — no aliasing across type or delimiter tricks.
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
			if canonicalContextKey(tc.mapA) == canonicalContextKey(tc.mapB) {
				t.Errorf("canonical key must distinguish %v from %v", tc.mapA, tc.mapB)
			}
		})
	}

	// Logically-identical contexts must produce the SAME key (so they aggregate into one bucket).
	t.Run("identical contexts produce identical keys", func(t *testing.T) {
		a := canonicalContextKey(map[string]any{"x": 1, "y": "two"})
		b := canonicalContextKey(map[string]any{"y": "two", "x": 1})
		if a != b {
			t.Errorf("logically-identical contexts produced different canonical keys")
		}
	})

	// Each distinct-context case must land in its OWN full-tier bucket via add() — the count is
	// never misattributed to the other context (the defect the lossy FNV discriminator risked).
	for _, tc := range inequalityTests {
		t.Run("distinct buckets via add(): "+tc.name, func(t *testing.T) {
			agg := setupTestAggregator(t)
			nowMs := time.Now().UnixMilli()
			d := evalDetails{flagKey: "f", variant: "on"}
			agg.add(d, tc.mapA, nowMs)
			agg.add(d, tc.mapB, nowMs)

			agg.mu.Lock()
			defer agg.mu.Unlock()
			if len(agg.full) != 2 {
				t.Errorf("expected 2 full-tier buckets for distinct contexts %v vs %v, got %d", tc.mapA, tc.mapB, len(agg.full))
			}
			// Zero misattribution: every bucket holds exactly the one count it received.
			for k, e := range agg.full {
				if e.count != 1 {
					t.Errorf("bucket %+v has count %d; distinct contexts must not merge (misattribution)", k, e.count)
				}
			}
		})
	}

	// A multi-field context with a key/value containing '\n' and '=' must still aggregate
	// identically with itself (re-adding increments the SAME bucket, not a third).
	t.Run("re-adding identical multi-field context increments same bucket", func(t *testing.T) {
		agg := setupTestAggregator(t)
		nowMs := time.Now().UnixMilli()
		d := evalDetails{flagKey: "f", variant: "on"}
		ctx := map[string]any{"a": "x\ny", "b": 7, "c=d": true}
		agg.add(d, ctx, nowMs)
		agg.add(d, map[string]any{"b": 7, "a": "x\ny", "c=d": true}, nowMs) // same logical context, different insertion order

		agg.mu.Lock()
		defer agg.mu.Unlock()
		if len(agg.full) != 1 {
			t.Errorf("expected 1 full-tier bucket for identical context, got %d", len(agg.full))
		}
		for _, e := range agg.full {
			if e.count != 2 {
				t.Errorf("expected count 2 for re-added identical context, got %d", e.count)
			}
		}
	})
}

// TestDegradedCapBounded verifies that unbounded dynamic/abusive flag keys stay bounded under
// the 2-tier design. With the degraded tier as the terminal tier, an unbounded number of distinct
// flag keys must NOT grow the degraded map without bound: len(degraded) <= degradedCap, and the
// over-cap counts must be DROPPED-AND-COUNTED (droppedDegradedOverflow), never silently lost.
// Σ(full+degraded counts) + droppedDegradedOverflow must equal the add() call count.
func TestDegradedCapBounded(t *testing.T) {
	const cap = 3
	// globalCap=0 forces every distinct full key straight past the full tier into degraded.
	agg := newTestAggregator(0, 100_000, cap)
	nowMs := time.Now().UnixMilli()

	const calls = 100
	for i := range calls {
		d := evalDetails{
			flagKey: fmt.Sprintf("dynamic-flag-%d", i), // every key distinct
			variant: "on",
		}
		agg.add(d, nil, nowMs)
	}

	agg.mu.Lock()
	defer agg.mu.Unlock()

	if len(agg.degraded) > cap {
		t.Errorf("degraded cardinality %d exceeds degradedCap (%d) — terminal tier not bounded", len(agg.degraded), cap)
	}

	var total int64
	for _, e := range agg.full {
		total += e.count
	}
	for _, e := range agg.degraded {
		total += e.count
	}
	total += agg.droppedDegradedOverflow
	if total != calls {
		t.Errorf("count preservation violated under degradedCap: Σ(full+degraded)+dropped=%d, expected=%d", total, calls)
	}

	// The over-cap counts must be observable in the drop counter.
	if agg.droppedDegradedOverflow == 0 {
		t.Errorf("expected droppedDegradedOverflow > 0 when distinct keys exceed degradedCap")
	}
}

// TestRecordAfterStopIsNoop verifies record() cannot enqueue after shutdown. After
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

func TestRecordQueuesPrunedContextSnapshot(t *testing.T) {
	w := newFlagEvaluationWriter(ProviderConfig{})
	attrs := make(map[string]any, maxContextFields+50)
	for i := range maxContextFields + 50 {
		attrs[fmt.Sprintf("field-%03d", i)] = fmt.Sprintf("value-%03d", i)
	}
	attrs["zzz-oversized"] = strings.Repeat("x", maxFieldLength+1)
	hookCtx := of.NewHookContext(
		"test-flag",
		of.Boolean,
		false,
		of.NewClientMetadata(""),
		of.Metadata{Name: "test-provider"},
		of.NewEvaluationContext("user-1", attrs),
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

	if len(w.events) != 1 {
		t.Fatalf("expected one queued event, got %d", len(w.events))
	}
	ev := <-w.events
	if got := len(ev.contextAttrs); got != maxContextFields {
		t.Fatalf("queued context should be pruned to %d fields, got %d", maxContextFields, got)
	}
	if _, ok := ev.contextAttrs["zzz-oversized"]; ok {
		t.Fatal("queued context should not contain oversized string values")
	}
}

// TestExtractEvalDetailsPrefersErrorMessage verifies ErrorMessage is preferred when present;
// ErrorCode is the fallback only when ErrorMessage is empty.
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
