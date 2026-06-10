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
