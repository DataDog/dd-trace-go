// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
)

func mustMarshalJSONMap(t *testing.T, payload any) map[string]any {
	t.Helper()

	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("failed to unmarshal payload for schema validation: %v", err)
	}
	return m
}

func TestFlagEvaluationEndpointUsesTrackName(t *testing.T) {
	const want = "/evp_proxy/v2/api/v2/flagevaluation"
	if flagEvalLoggingEndpoint != want {
		t.Fatalf("flagEvalLoggingEndpoint = %q, want %q", flagEvalLoggingEndpoint, want)
	}
}

func TestDefaultEvalCapSizing(t *testing.T) {
	if defaultEvalGlobalCap <= evalScaleFullBucketTarget {
		t.Fatalf("defaultEvalGlobalCap = %d, want > %d", defaultEvalGlobalCap, evalScaleFullBucketTarget)
	}

	if defaultEvalPerFlagCap != evalScalePerFlagBucketTarget {
		t.Fatalf("defaultEvalPerFlagCap = %d, want %d", defaultEvalPerFlagCap, evalScalePerFlagBucketTarget)
	}

	if defaultEvalDegradedCap <= evalScaleDegradedBucketTarget {
		t.Fatalf("defaultEvalDegradedCap = %d, want > %d", defaultEvalDegradedCap, evalScaleDegradedBucketTarget)
	}
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
			got, _ := flattenAndPruneContext(tc.input)

			if !reflect.DeepEqual(got, want) {
				t.Errorf("merged flatten+prune differs from flattenContext+pruneContext:\n got=%v\nwant=%v", got, want)
			}

			// 256-field limit preserved.
			if len(got) > maxContextFields {
				t.Errorf("merged result has %d fields, exceeds maxContextFields %d", len(got), maxContextFields)
			}

			// Determinism: repeated calls yield an identical canonical key.
			firstAttrs, _ := flattenAndPruneContext(tc.input)
			first := canonicalContextKey(firstAttrs)
			for range 25 {
				attrs, _ := flattenAndPruneContext(tc.input)
				if k := canonicalContextKey(attrs); k != first {
					t.Fatalf("merged flatten+prune nondeterministic: canonical keys differ across calls")
				}
			}
		})
	}
}

// setupTestAggregator creates a flagEvalLoggingAggregator with small caps for testing.
// Caps are deliberately small to trigger tier-cascade behavior in unit tests.
func setupTestAggregator(t *testing.T) *flagEvalLoggingAggregator {
	t.Helper()
	return &flagEvalLoggingAggregator{
		full:        make(map[evaluationAggregationKey]*evaluationEntry),
		degraded:    make(map[evaluationDegradedKey]*evaluationEntry),
		perFlagFull: make(map[string]int),
		globalCap:   10, // small cap to trigger overflow in tests
		perFlagCap:  3,
		degradedCap: 3,
	}
}

// newTestAggregator builds a flagEvalLoggingAggregator with explicit, caller-supplied caps.
// Unlike setupTestAggregator (which fixes small caps), each cap is a parameter so a test can
// drive a specific tier-overflow scenario. The cap NUMBERS are load-bearing — callers pass
// the exact values their scenario requires.
func newTestAggregator(globalCap, perFlagCap, degradedCap int) *flagEvalLoggingAggregator {
	return &flagEvalLoggingAggregator{
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
func TestFlagEvaluationPayloadSchema(t *testing.T) {
	nowMs := time.Now().UnixMilli()

	requiredFields := []string{"timestamp", "flag", "first_evaluation", "last_evaluation", "evaluation_count"}

	tierTests := []struct {
		name           string
		event          flagEvalLoggingEvent
		requiredFlgKey bool     // full tier additionally asserts flag.key is present
		optionalAbsent []string // optional fields that must NOT appear for this tier
	}{
		{
			name: "full tier has all required fields",
			event: flagEvalLoggingEvent{
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
			event: flagEvalLoggingEvent{
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
			event: flagEvalLoggingEvent{
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
		payload := flagEvalLoggingPayload{
			Context: flagEvalDDContext{
				Service: "test-service",
				Env:     "test",
				Version: "1.0.0",
			},
			FlagEvaluations: []flagEvalLoggingEvent{
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

	t.Run("batch payload uses only stable EVP flagevaluation fields", func(t *testing.T) {
		payload := flagEvalLoggingPayload{
			Context: flagEvalDDContext{
				Service: "test-service",
				Env:     "test",
				Version: "1.0.0",
			},
			FlagEvaluations: []flagEvalLoggingEvent{
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

		m := mustMarshalJSONMap(t, payload)
		for k := range m {
			switch k {
			case "context", "flagEvaluations":
			default:
				t.Fatalf("unexpected batch payload field %q", k)
			}
		}

		arr, ok := m["flagEvaluations"].([]any)
		if !ok {
			t.Fatalf("flagEvaluations is not an array: %T", m["flagEvaluations"])
		}
		if len(arr) != 3 {
			t.Fatalf("expected 3 flagEvaluations entries, got %d", len(arr))
		}

		allowedEventFields := map[string]struct{}{
			"timestamp":            {},
			"flag":                 {},
			"first_evaluation":     {},
			"last_evaluation":      {},
			"evaluation_count":     {},
			"runtime_default_used": {},
			"targeting_key":        {},
			"variant":              {},
			"allocation":           {},
			"targeting_rule":       {},
			"error":                {},
			"context":              {},
		}
		for i, raw := range arr {
			event, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("flagEvaluations[%d] is not an object: %T", i, raw)
			}
			for k := range event {
				if _, ok := allowedEventFields[k]; !ok {
					t.Fatalf("flagEvaluations[%d] emitted unexpected field %q", i, k)
				}
			}
			for _, required := range requiredFields {
				if _, ok := event[required]; !ok {
					t.Fatalf("flagEvaluations[%d] missing required field %q", i, required)
				}
			}
			flag, ok := event["flag"].(map[string]any)
			if !ok {
				t.Fatalf("flagEvaluations[%d].flag is not an object: %T", i, event["flag"])
			}
			if _, ok := flag["key"]; !ok {
				t.Fatalf("flagEvaluations[%d].flag.key missing", i)
			}
		}

		full := arr[0].(map[string]any)
		if _, ok := full["reason"]; ok {
			t.Fatal("full EVP event must not emit OpenFeature reason")
		}
		if _, ok := full["targeting_key"]; !ok {
			t.Fatal("full EVP event should retain targeting_key")
		}
		if _, ok := full["context"]; !ok {
			t.Fatal("full EVP event should retain context")
		}

		degraded := arr[1].(map[string]any)
		if _, ok := degraded["reason"]; ok {
			t.Fatal("degraded EVP event must not emit OpenFeature reason")
		}
		if _, ok := degraded["targeting_key"]; ok {
			t.Fatal("degraded EVP event should omit targeting_key")
		}
		if _, ok := degraded["context"]; ok {
			t.Fatal("degraded EVP event should omit context")
		}
		if _, ok := degraded["variant"]; !ok {
			t.Fatal("degraded EVP event should retain schema-visible variant")
		}
		if _, ok := degraded["allocation"]; !ok {
			t.Fatal("degraded EVP event should retain schema-visible allocation")
		}

		requiredOnly := arr[2].(map[string]any)
		for _, optional := range []string{"reason", "targeting_key", "variant", "allocation", "targeting_rule", "error", "context", "runtime_default_used"} {
			if _, ok := requiredOnly[optional]; ok {
				t.Fatalf("required-only EVP event should omit %q", optional)
			}
		}
	})
}

// TestAggregatorDistinctAllocationBuckets verifies that allocation is part of the aggregation key.
func TestAggregatorDistinctAllocationBuckets(t *testing.T) {
	agg := setupTestAggregator(t)
	nowMs := time.Now().UnixMilli()

	// Two evaluations that differ only in allocationKey — they must be in separate full buckets.
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
	w := newFlagEvalLoggingWriter(ProviderConfig{})
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

func TestEvaluationEntryObserveOutOfOrderTimestamps(t *testing.T) {
	entry := newEvaluationEntry(200)

	entry.observe(150)
	entry.observe(250)

	if entry.count != 3 {
		t.Fatalf("count = %d, want 3", entry.count)
	}
	if entry.firstEvaluation != 150 {
		t.Fatalf("firstEvaluation = %d, want 150", entry.firstEvaluation)
	}
	if entry.lastEvaluation != 250 {
		t.Fatalf("lastEvaluation = %d, want 250", entry.lastEvaluation)
	}
}

// TestSaturationCountPreservation is the regression guard against a SILENT drop at saturation.
// The invariant is: Σ(full+degraded counts) + droppedDegradedOverflow == add() calls.
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

		// Continue adding until degradedCap is also exhausted. At that point, new degraded
		// buckets must be dropped and COUNTED (droppedDegradedOverflow).
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

	t.Run("supported numeric scalar types produce distinct keys", func(t *testing.T) {
		values := []struct {
			name  string
			value any
		}{
			{name: "int", value: int(1)},
			{name: "int8", value: int8(1)},
			{name: "int16", value: int16(1)},
			{name: "int32", value: int32(1)},
			{name: "int64", value: int64(1)},
			{name: "uint", value: uint(1)},
			{name: "uint8", value: uint8(1)},
			{name: "uint16", value: uint16(1)},
			{name: "uint32", value: uint32(1)},
			{name: "uint64", value: uint64(1)},
			{name: "float32", value: float32(1)},
			{name: "float64", value: float64(1)},
		}

		keys := make(map[string]string, len(values))
		for _, item := range values {
			key := canonicalContextKey(map[string]any{"x": item.value})
			if other, ok := keys[key]; ok {
				t.Fatalf("%s and %s produced the same canonical context key", item.name, other)
			}
			keys[key] = item.name
		}
	})

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
			// Consent on: the context is a bucket dimension only when it survives
			// serialization. See TestConsentOffDropsContextFromBucketKey for the other half.
			d := evalDetails{flagKey: "f", variant: "on", observeFullEvaluationData: true}
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
		d := evalDetails{flagKey: "f", variant: "on", observeFullEvaluationData: true}
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

// TestDegradedCapBounded verifies that unbounded dynamic/abusive flag keys stay bounded.
// An unbounded number of distinct flag keys must NOT grow the degraded map without bound:
// len(degraded) <= degradedCap, and the over-cap counts must be DROPPED-AND-COUNTED
// (droppedDegradedOverflow), never silently lost.
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
	w := newFlagEvalLoggingWriter(ProviderConfig{})

	// Do NOT start the worker. stop() must still be safe (ticker==nil path) and mark stopped.
	w.stop()

	before := w.closedDrop.Load()

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
	if got := w.closedDrop.Load(); got != before+1 {
		t.Errorf("record() after stop() must count the event as a closed drop: closedDrop=%d, expected=%d", got, before+1)
	}
}

// TestBuildFlagEvalPayloads covers the size-bounded payload splitter (the Go mirror of
// Java's FlagEvaluationPayloads.buildPayloads): small flushes produce one payload, a flush
// over payloadSizeLimitBytes is split into multiple payloads, a single oversized event is
// degraded (targeting_key + context dropped) to fit, and an event too large even when degraded
// is dropped and counted.
func TestBuildFlagEvalPayloads(t *testing.T) {
	w := newFlagEvalLoggingWriter(ProviderConfig{})

	t.Run("small flush yields one payload", func(t *testing.T) {
		events := []flagEvalLoggingEvent{
			{Flag: flagEvalFlag{Key: "f1"}, EvaluationCount: 1},
			{Flag: flagEvalFlag{Key: "f2"}, EvaluationCount: 1},
		}
		payloads, dropped, degraded, err := w.buildFlagEvalPayloads(events)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(payloads) != 1 {
			t.Fatalf("got %d payloads, want 1", len(payloads))
		}
		if dropped != 0 || degraded != 0 {
			t.Errorf("dropped=%d degraded=%d, want 0/0", dropped, degraded)
		}
		// The single payload must be a valid envelope with both events.
		var p flagEvalLoggingPayload
		if err := json.Unmarshal(payloads[0], &p); err != nil {
			t.Fatalf("payload is not valid JSON: %v", err)
		}
		if len(p.FlagEvaluations) != 2 {
			t.Errorf("payload has %d events, want 2", len(p.FlagEvaluations))
		}
	})

	t.Run("oversized flush splits into multiple payloads", func(t *testing.T) {
		// Build enough events that their combined encoding exceeds payloadSizeLimitBytes.
		big := strings.Repeat("x", 64*1024) // 64 KiB context value each
		var events []flagEvalLoggingEvent
		// ~256 events × 64 KiB ≈ 16 MiB > 5 MiB limit → must split.
		for i := range 256 {
			events = append(events, flagEvalLoggingEvent{
				Flag:            flagEvalFlag{Key: fmt.Sprintf("f%d", i)},
				EvaluationCount: 1,
				TargetingKey:    fmt.Sprintf("user-%d", i),
				Context:         &flagEvalEventContext{Evaluation: map[string]any{"blob": big}},
			})
		}
		payloads, dropped, degraded, err := w.buildFlagEvalPayloads(events)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(payloads) < 2 {
			t.Fatalf("got %d payloads, want >=2 (flush must split)", len(payloads))
		}
		if dropped != 0 || degraded != 0 {
			t.Errorf("dropped=%d degraded=%d, want 0/0 for split-only", dropped, degraded)
		}
		// Every payload must be under the limit and be a valid envelope.
		totalEvents := 0
		for i, body := range payloads {
			if len(body) > payloadSizeLimitBytes {
				t.Errorf("payload %d is %d bytes, exceeds limit %d", i, len(body), payloadSizeLimitBytes)
			}
			var p flagEvalLoggingPayload
			if err := json.Unmarshal(body, &p); err != nil {
				t.Errorf("payload %d is not valid JSON: %v", i, err)
			}
			totalEvents += len(p.FlagEvaluations)
		}
		if totalEvents != len(events) {
			t.Errorf("payloads hold %d events, want %d (all retained across splits)", totalEvents, len(events))
		}
	})

	t.Run("single oversized event is degraded to fit", func(t *testing.T) {
		// One event whose full form (with context) exceeds the limit but whose degraded form
		// (no targeting_key + context) fits. Must be retained as degraded, not dropped.
		big := strings.Repeat("y", payloadSizeLimitBytes) // ~5 MiB context value
		events := []flagEvalLoggingEvent{{
			Flag:            flagEvalFlag{Key: "big-flag"},
			EvaluationCount: 7,
			TargetingKey:    "user-oversized",
			Context:         &flagEvalEventContext{Evaluation: map[string]any{"blob": big}},
		}}
		payloads, dropped, degraded, err := w.buildFlagEvalPayloads(events)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(payloads) != 1 {
			t.Fatalf("got %d payloads, want 1 (degraded event fits)", len(payloads))
		}
		if degraded != 7 {
			t.Errorf("degraded=%d, want 7 (the event's evaluation_count)", degraded)
		}
		if dropped != 0 {
			t.Errorf("dropped=%d, want 0 (degraded event fits)", dropped)
		}
		var p flagEvalLoggingPayload
		if err := json.Unmarshal(payloads[0], &p); err != nil {
			t.Fatalf("payload is not valid JSON: %v", err)
		}
		if len(p.FlagEvaluations) != 1 || p.FlagEvaluations[0].TargetingKey != "" || p.FlagEvaluations[0].Context != nil {
			t.Errorf("degraded event must have no targeting_key/context, got %+v", p.FlagEvaluations)
		}
	})

	t.Run("event too large even degraded is dropped", func(t *testing.T) {
		// Even the degraded form (flag key alone) can't fit if the flag key itself exceeds the
		// limit. Use a flag key larger than payloadSizeLimitBytes so no form fits.
		hugeKey := strings.Repeat("k", payloadSizeLimitBytes+1)
		events := []flagEvalLoggingEvent{{
			Flag:            flagEvalFlag{Key: hugeKey},
			EvaluationCount: 3,
		}}
		payloads, dropped, degraded, err := w.buildFlagEvalPayloads(events)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(payloads) != 0 {
			t.Fatalf("got %d payloads, want 0 (event can't fit even degraded)", len(payloads))
		}
		if dropped != 3 {
			t.Errorf("dropped=%d, want 3 (the event's evaluation_count)", dropped)
		}
		if degraded != 0 {
			t.Errorf("degraded=%d, want 0 (degraded form also too large)", degraded)
		}
	})
}

func TestStopDrainsAndFlushesQueuedFlagEvaluations(t *testing.T) {
	payloads := make(chan flagEvalLoggingPayload, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		if r.URL.Path != flagEvalLoggingEndpoint {
			t.Errorf("unexpected EVP path: got %q, want %q", r.URL.Path, flagEvalLoggingEndpoint)
		}

		var payload flagEvalLoggingPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("failed to decode flagevaluation payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		payloads <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	agentURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("failed to parse test server URL: %v", err)
	}

	evp := newEVPClient()
	evp.agentURL = agentURL
	evp.httpClient = server.Client()

	w := newFlagEvalLoggingWriterWithEVP(ProviderConfig{FlagEvaluationFlushInterval: time.Hour}, evp)
	w.start()

	hookCtx := of.NewHookContext(
		"test-flag",
		of.Boolean,
		false,
		of.NewClientMetadata(""),
		of.Metadata{Name: "test-provider"},
		of.NewEvaluationContext("user-1", map[string]any{"country": "US"}),
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

	w.stop()

	select {
	case payload := <-payloads:
		if len(payload.FlagEvaluations) != 1 {
			t.Fatalf("expected 1 flushed flagevaluation event, got %d", len(payload.FlagEvaluations))
		}
		event := payload.FlagEvaluations[0]
		if event.Flag.Key != "test-flag" {
			t.Errorf("unexpected flag key: got %q, want test-flag", event.Flag.Key)
		}
		if event.EvaluationCount != 1 {
			t.Errorf("unexpected evaluation count: got %d, want 1", event.EvaluationCount)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stop() returned without flushing queued flagevaluation event")
	}
}

// TestFlattenAndPruneContextReportsTruncationReasons covers the reasons slice returned by
// flattenAndPruneContext. Each reason must be reported at most once per call, and only when
// the matching cap actually fired.
func TestFlattenAndPruneContextReportsTruncationReasons(t *testing.T) {
	overlongValue := strings.Repeat("v", maxFieldLength+1)
	overlongKey := strings.Repeat("k", maxKeyLength+1)

	// Field-cap alone: many small fields, no oversized keys or values.
	t.Run("fields cap alone", func(t *testing.T) {
		attrs := map[string]any{}
		for i := range maxContextFields + 5 {
			attrs[fmt.Sprintf("field-%04d", i)] = "small"
		}
		_, reasons := flattenAndPruneContext(attrs)
		if !slices.Contains(reasons, truncReasonMaxContextFields) || slices.Contains(reasons, truncReasonMaxValueLength) || slices.Contains(reasons, truncReasonMaxKeyLength) {
			t.Errorf("expected only max_context_fields, got %v", reasons)
		}
	})

	// Value-cap alone: one oversized string value, everything else small and in-cap.
	t.Run("value cap alone", func(t *testing.T) {
		attrs := map[string]any{"role": "admin", "big": overlongValue}
		_, reasons := flattenAndPruneContext(attrs)
		if !slices.Contains(reasons, truncReasonMaxValueLength) || slices.Contains(reasons, truncReasonMaxKeyLength) || slices.Contains(reasons, truncReasonMaxContextFields) {
			t.Errorf("expected only max_value_length, got %v", reasons)
		}
	})

	// Key-cap alone: one over-long key, everything else small.
	t.Run("key cap alone", func(t *testing.T) {
		attrs := map[string]any{"role": "admin", overlongKey: "small"}
		_, reasons := flattenAndPruneContext(attrs)
		if !slices.Contains(reasons, truncReasonMaxKeyLength) || slices.Contains(reasons, truncReasonMaxContextFields) || slices.Contains(reasons, truncReasonMaxValueLength) {
			t.Errorf("expected only max_key_length, got %v", reasons)
		}
	})

	// Value and key caps together, no field-cap. Sorting: "aaaa..."(overlong key) < "big" < "role"
	// so the overlong-key entry is visited before the fields cap could fire.
	t.Run("value and key caps together", func(t *testing.T) {
		attrs := map[string]any{
			"aaaa" + overlongKey: "small",       // sorts first, hits key cap
			"big":                overlongValue, // sorts middle, hits value cap
			"role":               "admin",       // sorts last, kept
		}
		_, reasons := flattenAndPruneContext(attrs)
		if !slices.Contains(reasons, truncReasonMaxKeyLength) || !slices.Contains(reasons, truncReasonMaxValueLength) {
			t.Errorf("expected max_key_length AND max_value_length, got %v", reasons)
		}
	})

	// No caps fire → nil reasons.
	t.Run("nothing to truncate", func(t *testing.T) {
		attrs := map[string]any{"role": "admin"}
		_, reasons := flattenAndPruneContext(attrs)
		if reasons != nil {
			t.Errorf("expected nil reasons, got %v", reasons)
		}
	})

	// List-element cap: a single list with more than maxListElements scalar elements. Only the
	// list cap fires; depth/structure/field caps do not (the kept elements stay under the field cap).
	t.Run("list elements cap", func(t *testing.T) {
		elems := make([]any, maxListElements+5)
		for i := range elems {
			elems[i] = fmt.Sprintf("e%d", i)
		}
		attrs := map[string]any{"tags": elems}
		got, reasons := flattenAndPruneContext(attrs)
		if !slices.Contains(reasons, truncReasonMaxListElements) {
			t.Errorf("expected max_list_elements, got %v", reasons)
		}
		// Exactly maxListElements list entries are retained.
		count := 0
		for k := range got {
			if strings.HasPrefix(k, "tags.") {
				count++
			}
		}
		if count != maxListElements {
			t.Errorf("retained %d list elements, want %d", count, maxListElements)
		}
	})

	// Structure-property cap: a single structure with more than maxStructureProperties properties.
	t.Run("structure properties cap", func(t *testing.T) {
		nested := make(map[string]any, maxStructureProperties+5)
		for i := range maxStructureProperties + 5 {
			nested[fmt.Sprintf("p%04d", i)] = i
		}
		attrs := map[string]any{"obj": nested}
		got, reasons := flattenAndPruneContext(attrs)
		if !slices.Contains(reasons, truncReasonMaxStructureProperties) {
			t.Errorf("expected max_structure_properties, got %v", reasons)
		}
		count := 0
		for k := range got {
			if strings.HasPrefix(k, "obj.") {
				count++
			}
		}
		if count != maxStructureProperties {
			t.Errorf("retained %d structure properties, want %d", count, maxStructureProperties)
		}
	})

	// Snapshot-depth cap: a chain of nested maps deeper than maxSnapshotDepth, each with a
	// scalar sibling so shallower scalars are retained while the depth-4 container is truncated.
	t.Run("snapshot depth cap", func(t *testing.T) {
		var node any = map[string]any{"val": "bottom"}
		for i := range maxSnapshotDepth + 2 {
			node = map[string]any{"val": fmt.Sprintf("L%d", i), "n": node}
		}
		attrs := map[string]any{"root": node}
		got, reasons := flattenAndPruneContext(attrs)
		if !slices.Contains(reasons, truncReasonMaxSnapshotDepth) {
			t.Errorf("expected max_snapshot_depth, got %v", reasons)
		}
		// Deepest retained scalar is root.n.n.n.val (4 dots); the container at depth 4 is truncated.
		maxDepth := 0
		for k := range got {
			if d := strings.Count(k, "."); d > maxDepth {
				maxDepth = d
			}
		}
		if maxDepth != maxSnapshotDepth {
			t.Errorf("deepest retained key has %d dots, want %d (maxSnapshotDepth)", maxDepth, maxSnapshotDepth)
		}
	})

	// Cycle cap: a map that contains itself as a descendant. The walker must terminate and
	// report the cycle reason rather than infinite-looping.
	t.Run("cycle", func(t *testing.T) {
		cyclic := map[string]any{"name": "x"}
		cyclic["self"] = cyclic // self-reference
		attrs := map[string]any{"ctx": cyclic}
		got, reasons := flattenAndPruneContext(attrs)
		if !slices.Contains(reasons, truncReasonCycle) {
			t.Errorf("expected cycle reason, got %v", reasons)
		}
		// The non-cyclic sibling is retained; the cycle is truncated.
		if got["ctx.name"] != "x" {
			t.Errorf("expected ctx.name retained, got %v", got)
		}
	})
}

// TestFlattenAndPruneContextBoundedTraversal verifies the inline-bounding contract that mirrors
// Java's DDEvaluator.copyPrunedContext: the walker's retained output (and therefore its hot-path
// cost) is bounded by the caps regardless of how large or pathological the caller's input is.
// Each case constructs an adversarial input and asserts the retained set is capped, the matching
// reason fires, and the call terminates (no infinite recursion / stack overflow).
func TestFlattenAndPruneContextBoundedTraversal(t *testing.T) {
	t.Run("deep chain is truncated at maxSnapshotDepth", func(t *testing.T) {
		var node any = map[string]any{"val": "bottom"}
		for i := range 100 { // far deeper than maxSnapshotDepth
			node = map[string]any{"val": fmt.Sprintf("L%d", i), "n": node}
		}
		attrs := map[string]any{"root": node}
		got, reasons := flattenAndPruneContext(attrs)
		if !slices.Contains(reasons, truncReasonMaxSnapshotDepth) {
			t.Fatalf("expected max_snapshot_depth, got %v", reasons)
		}
		maxDepth := 0
		for k := range got {
			if d := strings.Count(k, "."); d > maxDepth {
				maxDepth = d
			}
		}
		if maxDepth != maxSnapshotDepth {
			t.Errorf("deepest retained key has %d dots, want %d", maxDepth, maxSnapshotDepth)
		}
	})

	t.Run("huge list retains at most maxListElements", func(t *testing.T) {
		elems := make([]any, 10_000)
		for i := range elems {
			elems[i] = i
		}
		attrs := map[string]any{"tags": elems}
		got, reasons := flattenAndPruneContext(attrs)
		if !slices.Contains(reasons, truncReasonMaxListElements) {
			t.Fatalf("expected max_list_elements, got %v", reasons)
		}
		count := 0
		for k := range got {
			if strings.HasPrefix(k, "tags.") {
				count++
			}
		}
		if count != maxListElements {
			t.Errorf("retained %d list elements, want %d", count, maxListElements)
		}
	})

	t.Run("huge structure retains at most maxStructureProperties", func(t *testing.T) {
		nested := make(map[string]any, 10_000)
		for i := range 10_000 {
			nested[fmt.Sprintf("p%05d", i)] = i
		}
		attrs := map[string]any{"obj": nested}
		got, reasons := flattenAndPruneContext(attrs)
		if !slices.Contains(reasons, truncReasonMaxStructureProperties) {
			t.Fatalf("expected max_structure_properties, got %v", reasons)
		}
		count := 0
		for k := range got {
			if strings.HasPrefix(k, "obj.") {
				count++
			}
		}
		if count != maxStructureProperties {
			t.Errorf("retained %d structure properties, want %d", count, maxStructureProperties)
		}
	})

	t.Run("retained field count never exceeds maxContextFields", func(t *testing.T) {
		attrs := make(map[string]any, 5_000)
		for i := range 5_000 {
			attrs[fmt.Sprintf("k%05d", i)] = fmt.Sprintf("v%d", i)
		}
		got, reasons := flattenAndPruneContext(attrs)
		if !slices.Contains(reasons, truncReasonMaxContextFields) {
			t.Fatalf("expected max_context_fields, got %v", reasons)
		}
		if len(got) > maxContextFields {
			t.Errorf("retained %d fields, exceeds maxContextFields %d", len(got), maxContextFields)
		}
	})

	t.Run("cyclic structure terminates", func(t *testing.T) {
		cyclic := map[string]any{"name": "x"}
		cyclic["self"] = cyclic
		attrs := map[string]any{"ctx": cyclic}
		got, reasons := flattenAndPruneContext(attrs)
		if !slices.Contains(reasons, truncReasonCycle) {
			t.Fatalf("expected cycle reason, got %v", reasons)
		}
		if got["ctx.name"] != "x" {
			t.Errorf("expected ctx.name retained, got %v", got)
		}
	})

	t.Run("mutual cycle terminates", func(t *testing.T) {
		a := map[string]any{"id": 1}
		b := map[string]any{"id": 2}
		a["b"] = b
		b["a"] = a // a -> b -> a cycle
		attrs := map[string]any{"root": a}
		_, reasons := flattenAndPruneContext(attrs)
		if !slices.Contains(reasons, truncReasonCycle) {
			t.Errorf("expected cycle reason, got %v", reasons)
		}
	})
}

// TestRecordBumpsContextTruncatedTelemetry covers the record → contextTruncatedByReason path:
// a caller passing a context that trips a cap must bump the matching counter, and consent-off
// must skip the copy entirely (so the counter stays at zero even for the same caller shape).
func TestRecordBumpsContextTruncatedTelemetry(t *testing.T) {
	attrs := map[string]any{}
	for i := range maxContextFields + 3 {
		attrs[fmt.Sprintf("field-%04d", i)] = "small"
	}
	hookCtx := of.NewHookContext(
		"tel-flag",
		of.Boolean,
		false,
		of.NewClientMetadata(""),
		of.Metadata{Name: "test-provider"},
		of.NewEvaluationContext("user-1", attrs),
	)
	details := func(consent bool) of.InterfaceEvaluationDetails {
		return of.InterfaceEvaluationDetails{
			Value: true,
			EvaluationDetails: of.EvaluationDetails{
				FlagKey:  "tel-flag",
				FlagType: of.Boolean,
				ResolutionDetail: of.ResolutionDetail{
					Variant: "on",
					Reason:  of.TargetingMatchReason,
					FlagMetadata: of.FlagMetadata{
						metadataObserveFullEvaluationDataKey: consent,
					},
				},
			},
		}
	}

	loadCounter := func(w *flagEvalLoggingWriter, reason string) int64 {
		v, ok := w.contextTruncatedByReason.Load(reason)
		if !ok {
			return 0
		}
		return v.(*atomic.Int64).Load()
	}

	t.Run("consent-on bumps max_context_fields once", func(t *testing.T) {
		w := newFlagEvalLoggingWriter(ProviderConfig{})
		w.record(hookCtx, details(true))
		if got := loadCounter(w, truncReasonMaxContextFields); got != 1 {
			t.Errorf("consent-on: expected max_context_fields=1, got %d", got)
		}
	})

	t.Run("consent-off skips copy entirely — no truncation reasons bump", func(t *testing.T) {
		w := newFlagEvalLoggingWriter(ProviderConfig{})
		w.record(hookCtx, details(false))
		if got := loadCounter(w, truncReasonMaxContextFields); got != 0 {
			t.Errorf("consent-off: max_context_fields should be 0 (no copy work), got %d", got)
		}
	})
}

// TestPreEnqueueBoundingReleasesCallerContext proves the context is snapshotted onto the queued
// evalEvent by the time record() returns — mutating the caller's map after record() must not
// affect the aggregated bucket. Under a worker-side copy this would fail: the queue would hold
// a live reference and observe the mutation on the next flush.
func TestPreEnqueueBoundingReleasesCallerContext(t *testing.T) {
	w := newFlagEvalLoggingWriter(ProviderConfig{})
	attrs := map[string]any{"role": "admin"}
	hookCtx := of.NewHookContext(
		"pre-flag",
		of.Boolean,
		false,
		of.NewClientMetadata(""),
		of.Metadata{Name: "test-provider"},
		of.NewEvaluationContext("user-1", attrs),
	)
	details := of.InterfaceEvaluationDetails{
		Value: true,
		EvaluationDetails: of.EvaluationDetails{
			FlagKey:  "pre-flag",
			FlagType: of.Boolean,
			ResolutionDetail: of.ResolutionDetail{
				Variant:      "on",
				Reason:       of.TargetingMatchReason,
				FlagMetadata: of.FlagMetadata{metadataObserveFullEvaluationDataKey: true},
			},
		},
	}
	w.record(hookCtx, details)

	// Mutation happens AFTER record() returned. If the queue held a live reference the worker
	// would observe "role=poisoned"; because the copy was taken pre-enqueue, the snapshot is
	// stable.
	attrs["role"] = "poisoned"

	w.aggregate(<-w.events)
	if len(w.aggregator.full) != 1 {
		t.Fatalf("expected one aggregated entry, got %d", len(w.aggregator.full))
	}
	for _, entry := range w.aggregator.full {
		if got := entry.contextAttrs["role"]; got != "admin" {
			t.Errorf("expected snapshotted role=admin, got %v — the caller's mutation leaked into the aggregated bucket", got)
		}
	}
}

func TestRecordAggregatesPrunedContextSnapshot(t *testing.T) {
	w := newFlagEvalLoggingWriter(ProviderConfig{})
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
				// Consent on: pruning is only reachable when the context is captured at all.
				FlagMetadata: of.FlagMetadata{metadataObserveFullEvaluationDataKey: true},
			},
		},
	}

	w.record(hookCtx, details)

	if len(w.events) != 1 {
		t.Fatalf("expected one queued event, got %d", len(w.events))
	}
	w.aggregate(<-w.events)

	if len(w.aggregator.full) != 1 {
		t.Fatalf("expected one aggregated entry, got %d", len(w.aggregator.full))
	}
	for _, entry := range w.aggregator.full {
		if got := len(entry.contextAttrs); got != maxContextFields {
			t.Fatalf("aggregated context should be pruned to %d fields, got %d", maxContextFields, got)
		}
		if _, ok := entry.contextAttrs["zzz-oversized"]; ok {
			t.Fatal("aggregated context should not contain oversized string values")
		}
	}
}

// TestExtractEvalDetailsPrefersErrorMessage verifies ErrorMessage is preferred when present
// under consent-on; under consent-off ErrorMessage is dropped and ErrorCode is substituted so
// the wire never carries raw evaluation-context values echoed back through error strings.
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

	consentOn := func(d of.InterfaceEvaluationDetails) of.InterfaceEvaluationDetails {
		if d.FlagMetadata == nil {
			d.FlagMetadata = of.FlagMetadata{}
		}
		d.FlagMetadata[metadataObserveFullEvaluationDataKey] = true
		return d
	}

	tests := []struct {
		name             string
		details          of.InterfaceEvaluationDetails
		wantErrorMessage string
	}{
		{
			name: "consent-on: ErrorMessage preferred over ErrorCode",
			details: consentOn(of.InterfaceEvaluationDetails{
				EvaluationDetails: of.EvaluationDetails{
					ResolutionDetail: of.ResolutionDetail{
						Reason:       of.ErrorReason,
						ErrorCode:    of.GeneralCode,
						ErrorMessage: "boom",
					},
				},
			}),
			wantErrorMessage: "boom",
		},
		{
			name: "consent-on: empty ErrorMessage falls back to ErrorCode",
			details: consentOn(of.InterfaceEvaluationDetails{
				EvaluationDetails: of.EvaluationDetails{
					ResolutionDetail: of.ResolutionDetail{
						Reason:    of.ErrorReason,
						ErrorCode: of.TypeMismatchCode,
					},
				},
			}),
			wantErrorMessage: string(of.TypeMismatchCode),
		},
		{
			name: "consent-on: both empty yields empty errorMessage",
			details: consentOn(of.InterfaceEvaluationDetails{
				EvaluationDetails: of.EvaluationDetails{
					ResolutionDetail: of.ResolutionDetail{
						Reason: of.TargetingMatchReason,
					},
				},
			}),
			wantErrorMessage: "",
		},
		{
			name: "consent-off: raw ErrorMessage is dropped, ErrorCode substituted",
			details: of.InterfaceEvaluationDetails{
				EvaluationDetails: of.EvaluationDetails{
					ResolutionDetail: of.ResolutionDetail{
						Reason:       of.ErrorReason,
						ErrorCode:    of.TypeMismatchCode,
						ErrorMessage: `variant type mismatch: "jane.doe@datadoghq.com"`,
					},
				},
			},
			wantErrorMessage: string(of.TypeMismatchCode),
		},
		{
			name: "consent-off: raw ErrorMessage with no ErrorCode is dropped entirely",
			details: of.InterfaceEvaluationDetails{
				EvaluationDetails: of.EvaluationDetails{
					ResolutionDetail: of.ResolutionDetail{
						Reason:       of.ErrorReason,
						ErrorMessage: `For input string: "jane.doe@datadoghq.com"`,
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

// TestErrorMessageNeverCarriesRawContextUnderConsentOff is the wire-level negative test the
// cross-SDK contract requires: given an evaluation whose ErrorMessage embeds a PII-shaped raw
// value (as our own evaluator and third-party providers both can produce), the serialized
// flagevaluation payload must not contain that raw value anywhere under consent-off, even
// though the same raw string would ride through untouched on the consent-on path.
func TestErrorMessageNeverCarriesRawContextUnderConsentOff(t *testing.T) {
	const rawPII = "jane.doe@datadoghq.com"
	const errMsgWithPII = `For input string: "` + rawPII + `"`

	mkHookCtx := func() of.HookContext {
		return of.NewHookContext(
			"payment_flag",
			of.String,
			"default",
			of.NewClientMetadata(""),
			of.Metadata{Name: "test-provider"},
			of.NewEvaluationContext(rawPII, nil),
		)
	}

	newWriterUnderTest := func() *flagEvalLoggingWriter {
		w := newFlagEvalLoggingWriterWithEVP(ProviderConfig{}, nil)
		w.aggregator.globalCap = 100
		w.aggregator.perFlagCap = 10
		w.aggregator.degradedCap = 10
		return w
	}

	for _, tc := range []struct {
		name    string
		consent bool
	}{
		{"consent-off drops raw ErrorMessage", false},
		{"consent-on preserves raw ErrorMessage (control)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			details := of.InterfaceEvaluationDetails{
				EvaluationDetails: of.EvaluationDetails{
					ResolutionDetail: of.ResolutionDetail{
						Reason:       of.ErrorReason,
						ErrorCode:    of.TypeMismatchCode,
						ErrorMessage: errMsgWithPII,
						FlagMetadata: of.FlagMetadata{
							metadataObserveFullEvaluationDataKey: tc.consent,
						},
					},
				},
			}
			w := newWriterUnderTest()
			w.record(mkHookCtx(), details)
			if len(w.events) != 1 {
				t.Fatalf("expected one queued event, got %d", len(w.events))
			}
			w.aggregate(<-w.events)

			events := w.buildFlushEvents(time.Now().UnixMilli())
			if len(events) != 1 {
				t.Fatalf("expected one flush event, got %d", len(events))
			}
			payload, err := json.Marshal(events[0])
			if err != nil {
				t.Fatalf("marshal payload: %v", err)
			}
			gotContainsPII := strings.Contains(string(payload), rawPII)
			if tc.consent && !gotContainsPII {
				t.Errorf("consent-on control failed: payload should still contain raw ErrorMessage. payload=%s", payload)
			}
			if !tc.consent && gotContainsPII {
				t.Errorf("consent-off leaked raw PII in payload: %s", payload)
			}
		})
	}
}
