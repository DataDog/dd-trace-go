// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"strconv"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"
)

// =============================================================================
// Aggregator path benchmarks — one benchmark per distinct code path in add().
//
// Path taxonomy:
//   A. Full map HIT             — existing entry, just increment + timestamp
//   B. Full map MISS, under cap — new tuple, insert into full map
//   C. Full map MISS, per-flag cap hit, degraded HIT  — increment degraded entry
//   D. Full map MISS, per-flag cap hit, degraded MISS — insert new degraded entry
//   E. Full map MISS, global cap hit, known flag, degraded HIT
//   F. Full map MISS, global cap hit, known flag, degraded MISS
//   G. Full map MISS, global cap hit, cold flag — fairness eviction fires
//   H. Hook → aggregator (full path including context flattening + hashing)
//      H1. Hook, map hit (warm, same key every call)
//      H2. Hook, map miss (unique key each call, under cap)
//      H3. Hook, rich context (10 attributes)
// =============================================================================

// BenchmarkEvaluationAggregator_AddIncrement tests the dominant path:
// incrementing an existing entry (no insert, no cap check).
func BenchmarkEvaluationAggregator_AddIncrement(b *testing.B) {
	a := newEvaluationAggregator(10000, 65536)
	// Pre-populate with one entry for "flag-a"/"user-0"
	a.add(evaluationAggregationKey{
		flagKey:      "flag-a",
		variant:      "on",
		targetingKey: "user-0",
	}, nil, "", "", false, 1000)

	key := evaluationAggregationKey{
		flagKey:      "flag-a",
		variant:      "on",
		targetingKey: "user-0",
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		a.add(key, nil, "", "", false, 1000)
	}
}

// BenchmarkEvaluationAggregator_AddNewTuple tests the new-tuple insert path
// (allocates entry, updates maps). Each iteration uses a unique targetingKey.
func BenchmarkEvaluationAggregator_AddNewTuple(b *testing.B) {
	a := newEvaluationAggregator(10000, 65536)

	b.ReportAllocs()
	b.ResetTimer()

	i := 0
	for b.Loop() {
		a.add(evaluationAggregationKey{
			flagKey:      "flag-a",
			variant:      "on",
			targetingKey: strconv.Itoa(i),
		}, nil, "", "", false, int64(i))
		i++
	}
}

// BenchmarkEvaluationAggregator_AddDegraded tests the degraded bucket path
// (per-flag cap already hit). cap=1 forces all but first to degrade.
func BenchmarkEvaluationAggregator_AddDegraded(b *testing.B) {
	a := newEvaluationAggregator(1, 65536)
	// Pre-add one entry for "flag-a"/"user-0" to fill the per-flag cap
	a.add(evaluationAggregationKey{
		flagKey:      "flag-a",
		variant:      "on",
		targetingKey: "user-0",
	}, nil, "", "", false, 1000)

	b.ReportAllocs()
	b.ResetTimer()

	i := 1
	for b.Loop() {
		a.add(evaluationAggregationKey{
			flagKey:      "flag-a",
			variant:      "on",
			targetingKey: strconv.Itoa(i),
		}, nil, "", "", false, int64(i))
		i++
	}
}

// BenchmarkEvaluationHook_After tests the full hook path (hook → aggregator.add).
func BenchmarkEvaluationHook_After(b *testing.B) {
	writer := &evaluationWriter{
		aggregator: newEvaluationAggregator(10000, 65536),
	}
	h := newEvaluationHook(writer)

	evalCtx := of.NewEvaluationContext("user-123", map[string]any{
		"country": "US",
	})
	hookCtx := of.NewHookContext(
		"bench-flag",
		of.Boolean,
		false,
		of.ClientMetadata{},
		of.Metadata{},
		evalCtx,
	)
	details := of.InterfaceEvaluationDetails{
		Value: true,
		EvaluationDetails: of.EvaluationDetails{
			FlagKey:  "bench-flag",
			FlagType: of.Boolean,
			ResolutionDetail: of.ResolutionDetail{
				Variant: "on",
				Reason:  of.TargetingMatchReason,
			},
		},
	}

	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = h.After(ctx, hookCtx, details, of.HookHints{})
	}
}

// BenchmarkEvaluationAggregator_AddConcurrent tests concurrent throughput
// with distinct targeting keys.
func BenchmarkEvaluationAggregator_AddConcurrent(b *testing.B) {
	a := newEvaluationAggregator(10000, 65536)

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			a.add(evaluationAggregationKey{
				flagKey:      "f",
				variant:      "v",
				targetingKey: strconv.Itoa(i % 100),
			}, nil, "", "", false, int64(i))
			i++
		}
	})
}

// -----------------------------------------------------------------------------
// Path C: per-flag cap hit, degraded map HIT (increment existing degraded entry)
// Setup: fill flag-a to perFlagCap=1, pre-seed one degraded entry with the same
// degraded key so every benchmark iteration hits the increment branch.
// -----------------------------------------------------------------------------
func BenchmarkEvaluationAggregator_AddDegradedHit(b *testing.B) {
	a := newEvaluationAggregator(1, 65536)
	// Fill the per-flag cap for flag-a.
	a.add(evaluationAggregationKey{flagKey: "flag-a", variant: "on", targetingKey: "user-0"}, nil, "", "", false, 1000)
	// Pre-seed the degraded entry so the first benchmark iteration hits it too.
	a.add(evaluationAggregationKey{flagKey: "flag-a", variant: "on", targetingKey: "user-1"}, nil, "", "", false, 1000)

	key := evaluationAggregationKey{flagKey: "flag-a", variant: "on", targetingKey: "user-2"}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		// Same degraded key every iteration → always increments existing degraded entry.
		a.add(key, nil, "", "", false, 1000)
	}
}

// -----------------------------------------------------------------------------
// Path D: per-flag cap hit, degraded map MISS (insert new degraded entry)
// Each iteration uses a unique variant so a new degraded key is created each time.
// -----------------------------------------------------------------------------
func BenchmarkEvaluationAggregator_AddDegradedMiss(b *testing.B) {
	a := newEvaluationAggregator(1, 65536)
	// Fill the per-flag cap.
	a.add(evaluationAggregationKey{flagKey: "flag-a", variant: "seed", targetingKey: "user-0"}, nil, "", "", false, 1000)

	b.ReportAllocs()
	b.ResetTimer()

	i := 0
	for b.Loop() {
		// Unique variant per iteration → new degraded key each time.
		a.add(evaluationAggregationKey{
			flagKey:  "flag-a",
			variant:  strconv.Itoa(i),
			targetingKey: "user-1",
		}, nil, "", "", false, int64(i))
		i++
	}
}

// -----------------------------------------------------------------------------
// Path E: global cap hit, known flag (already has entries), degraded HIT
// -----------------------------------------------------------------------------
func BenchmarkEvaluationAggregator_AddGlobalCapKnownFlagDegradedHit(b *testing.B) {
	a := newEvaluationAggregator(100, 2)
	// Fill global cap with two distinct flags.
	a.add(evaluationAggregationKey{flagKey: "flag-a", variant: "on", targetingKey: "u0"}, nil, "", "", false, 1000)
	a.add(evaluationAggregationKey{flagKey: "flag-b", variant: "on", targetingKey: "u0"}, nil, "", "", false, 1000)
	// Seed a degraded entry for flag-a (same variant) by triggering a global-cap miss.
	a.add(evaluationAggregationKey{flagKey: "flag-a", variant: "on", targetingKey: "u1"}, nil, "", "", false, 1000)

	// This key will always hit the existing degraded bucket for flag-a.
	key := evaluationAggregationKey{flagKey: "flag-a", variant: "on", targetingKey: "u2"}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		a.add(key, nil, "", "", false, 1000)
	}
}

// -----------------------------------------------------------------------------
// Path F: global cap hit, known flag, degraded MISS (new degraded entry each time)
// -----------------------------------------------------------------------------
func BenchmarkEvaluationAggregator_AddGlobalCapKnownFlagDegradedMiss(b *testing.B) {
	a := newEvaluationAggregator(100, 2)
	a.add(evaluationAggregationKey{flagKey: "flag-a", variant: "on", targetingKey: "u0"}, nil, "", "", false, 1000)
	a.add(evaluationAggregationKey{flagKey: "flag-b", variant: "on", targetingKey: "u0"}, nil, "", "", false, 1000)

	b.ReportAllocs()
	b.ResetTimer()

	i := 0
	for b.Loop() {
		// Unique variant each time → new degraded key, flag-a is known.
		a.add(evaluationAggregationKey{
			flagKey:      "flag-a",
			variant:      strconv.Itoa(i),
			targetingKey: "u1",
		}, nil, "", "", false, int64(i))
		i++
	}
}

// -----------------------------------------------------------------------------
// Path G: global cap hit, cold flag — fairness eviction fires every iteration.
// To ensure the cap is always full when the cold flag arrives, we rebuild the
// aggregator state before each iteration using b.ResetTimer tricks — but since
// that's impractical in a tight loop, we instead use a large enough cap that
// one eviction per iteration is sustainable: cap=N, N distinct known-flag
// entries fill it, then a fresh cold flag triggers eviction each time.
// We use cap=10 so setup is fast and eviction is always triggered.
// -----------------------------------------------------------------------------
func BenchmarkEvaluationAggregator_AddFairnessEviction(b *testing.B) {
	b.ReportAllocs()

	i := 0
	for b.Loop() {
		// Fresh aggregator each iteration so the cold flag is always cold
		// and the global cap is always exactly full. cap=10.
		a := newEvaluationAggregator(100, 10)
		for j := 0; j < 10; j++ {
			a.add(evaluationAggregationKey{
				flagKey:      "flag-known",
				variant:      "on",
				targetingKey: "u" + strconv.Itoa(j),
			}, nil, "", "", false, 1000)
		}
		// Cold flag — triggers fairness eviction.
		a.add(evaluationAggregationKey{
			flagKey:      "cold-flag-" + strconv.Itoa(i),
			variant:      "on",
			targetingKey: "u0",
		}, nil, "", "", false, 1000)
		i++
	}
}

// -----------------------------------------------------------------------------
// Hook-level benchmarks (full path: context extraction → hash → aggregator.add)
// -----------------------------------------------------------------------------

// BenchmarkEvaluationHook_After_MapHit: same flag+user every call → increment path.
// Already covered by BenchmarkEvaluationHook_After above.

// BenchmarkEvaluationHook_After_MapMiss: unique targeting key each call → new-tuple insert.
func BenchmarkEvaluationHook_After_MapMiss(b *testing.B) {
	writer := &evaluationWriter{
		aggregator: newEvaluationAggregator(10000, 65536),
	}
	h := newEvaluationHook(writer)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	i := 0
	for b.Loop() {
		evalCtx := of.NewEvaluationContext("user-"+strconv.Itoa(i), map[string]any{"country": "US"})
		hookCtx := of.NewHookContext("bench-flag", of.Boolean, false, of.ClientMetadata{}, of.Metadata{}, evalCtx)
		details := of.InterfaceEvaluationDetails{
			Value: true,
			EvaluationDetails: of.EvaluationDetails{
				FlagKey:  "bench-flag",
				FlagType: of.Boolean,
				ResolutionDetail: of.ResolutionDetail{
					Variant: "on",
					Reason:  of.TargetingMatchReason,
				},
			},
		}
		_ = h.After(ctx, hookCtx, details, of.HookHints{})
		i++
	}
}

// BenchmarkEvaluationHook_After_RichContext: 10 context attributes, map hit.
func BenchmarkEvaluationHook_After_RichContext(b *testing.B) {
	writer := &evaluationWriter{
		aggregator: newEvaluationAggregator(10000, 65536),
	}
	h := newEvaluationHook(writer)
	ctx := context.Background()

	evalCtx := of.NewEvaluationContext("user-123", map[string]any{
		"country":    "US",
		"tier":       "premium",
		"age":        30,
		"platform":   "ios",
		"region":     "us-east-1",
		"ab_cohort":  "treatment",
		"account_id": int64(99999),
		"beta_user":  true,
		"locale":     "en-US",
		"version":    "3.2.1",
	})
	hookCtx := of.NewHookContext("bench-flag", of.Boolean, false, of.ClientMetadata{}, of.Metadata{}, evalCtx)
	details := of.InterfaceEvaluationDetails{
		Value: true,
		EvaluationDetails: of.EvaluationDetails{
			FlagKey:  "bench-flag",
			FlagType: of.Boolean,
			ResolutionDetail: of.ResolutionDetail{
				Variant: "on",
				Reason:  of.TargetingMatchReason,
			},
		},
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = h.After(ctx, hookCtx, details, of.HookHints{})
	}
}

// BenchmarkEvaluationHook_After_ErrorEval: evaluation with an error result.
func BenchmarkEvaluationHook_After_ErrorEval(b *testing.B) {
	writer := &evaluationWriter{
		aggregator: newEvaluationAggregator(10000, 65536),
	}
	h := newEvaluationHook(writer)
	ctx := context.Background()

	evalCtx := of.NewEvaluationContext("user-123", map[string]any{"country": "US"})
	hookCtx := of.NewHookContext("bench-flag", of.Boolean, false, of.ClientMetadata{}, of.Metadata{}, evalCtx)
	details := of.InterfaceEvaluationDetails{
		Value: false,
		EvaluationDetails: of.EvaluationDetails{
			FlagKey:  "bench-flag",
			FlagType: of.Boolean,
			ResolutionDetail: of.ResolutionDetail{
				Variant:      "",
				Reason:       of.ErrorReason,
				ErrorCode:    of.FlagNotFoundCode,
				ErrorMessage: "bench-flag not found",
			},
		},
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = h.After(ctx, hookCtx, details, of.HookHints{})
	}
}
