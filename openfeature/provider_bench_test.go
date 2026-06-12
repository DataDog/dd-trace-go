// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/open-feature/go-sdk/openfeature"
)

// BenchmarkBooleanEvaluation benchmarks boolean flag evaluation
func BenchmarkBooleanEvaluation(b *testing.B) {
	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	ctx := context.Background()
	flatCtx := openfeature.FlattenedContext{
		"targetingKey": "user-123",
		"country":      "US",
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = provider.BooleanEvaluation(ctx, "bool-flag", false, flatCtx)
	}
}

// BenchmarkStringEvaluation benchmarks string flag evaluation
func BenchmarkStringEvaluation(b *testing.B) {
	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	ctx := context.Background()
	flatCtx := openfeature.FlattenedContext{
		"targetingKey": "user-123",
		"age":          25,
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = provider.StringEvaluation(ctx, "string-flag", "default", flatCtx)
	}
}

// BenchmarkIntEvaluation benchmarks integer flag evaluation
func BenchmarkIntEvaluation(b *testing.B) {
	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	ctx := context.Background()
	flatCtx := openfeature.FlattenedContext{
		"targetingKey": "user-123",
		"premium":      "yes",
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = provider.IntEvaluation(ctx, "int-flag", 5, flatCtx)
	}
}

// BenchmarkFloatEvaluation benchmarks float flag evaluation
func BenchmarkFloatEvaluation(b *testing.B) {
	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	ctx := context.Background()
	flatCtx := openfeature.FlattenedContext{
		"targetingKey": "user-123",
		"tier":         "premium",
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = provider.FloatEvaluation(ctx, "float-flag", 0.0, flatCtx)
	}
}

// BenchmarkObjectEvaluation benchmarks object flag evaluation
func BenchmarkEvaluation(b *testing.B) {
	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	ctx := context.Background()
	flatCtx := openfeature.FlattenedContext{
		"targetingKey": "user-123",
		"requests":     1500,
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = provider.ObjectEvaluation(ctx, "json-flag", nil, flatCtx)
	}
}

// BenchmarkEvaluationWithVaryingContextSize benchmarks evaluation with different context sizes
func BenchmarkEvaluationWithVaryingContextSize(b *testing.B) {
	contextSizes := []struct {
		name      string
		numFields int
	}{
		{"1field", 1},
		{"5fields", 5},
		{"10fields", 10},
		{"20fields", 20},
	}

	for _, size := range contextSizes {
		b.Run(size.name, func(b *testing.B) {
			provider := newDatadogProvider(ProviderConfig{})
			config := createTestConfig()
			provider.updateConfiguration(config)

			ctx := context.Background()
			flatCtx := openfeature.FlattenedContext{
				"targetingKey": "user-123",
				"country":      "US",
			}

			// Add additional fields to the context
			for i := 1; i < size.numFields; i++ {
				flatCtx[string(rune('a'+i))] = i
			}

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_ = provider.BooleanEvaluation(ctx, "bool-flag", false, flatCtx)
			}
		})
	}
}

// BenchmarkEvaluationWithVaryingFlagCounts benchmarks evaluation with different numbers of flags in config
func BenchmarkEvaluationWithVaryingFlagCounts(b *testing.B) {
	flagCounts := []struct {
		name     string
		numFlags int
	}{
		{"5flags", 5},
		{"10flags", 10},
		{"50flags", 50},
		{"100flags", 100},
	}

	for _, count := range flagCounts {
		b.Run(count.name, func(b *testing.B) {
			provider := newDatadogProvider(ProviderConfig{})
			config := createTestConfig()

			// Add additional flags
			for i := len(config.Flags); i < count.numFlags; i++ {
				flagKey := string(rune('a' + i))
				config.Flags[flagKey] = &flag{
					Key:           flagKey,
					Enabled:       true,
					VariationType: valueTypeBoolean,
					Variations: map[string]*variant{
						"on": {Key: "on", Value: true},
					},
					Allocations: []*allocation{
						{
							Key:   "allocation1",
							Rules: []*rule{},
							Splits: []*split{
								{
									Shards:       []*shard{},
									VariationKey: "on",
								},
							},
						},
					},
				}
			}

			provider.updateConfiguration(config)

			ctx := context.Background()
			flatCtx := openfeature.FlattenedContext{
				"targetingKey": "user-123",
				"country":      "US",
			}

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_ = provider.BooleanEvaluation(ctx, "bool-flag", false, flatCtx)
			}
		})
	}
}

// BenchmarkConcurrentEvaluations benchmarks concurrent flag evaluations
func BenchmarkConcurrentEvaluations(b *testing.B) {
	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	ctx := context.Background()
	flatCtx := openfeature.FlattenedContext{
		"targetingKey": "user-123",
		"country":      "US",
	}

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = provider.BooleanEvaluation(ctx, "bool-flag", false, flatCtx)
		}
	})
}

// makeBenchmarkConfig creates a test config with the specified number of flags.
// Extends createTestConfig() for benchmark load profiles.
func makeBenchmarkConfig(numFlags int) *universalFlagsConfiguration {
	config := createTestConfig()
	for i := len(config.Flags); i < numFlags; i++ {
		flagKey := "bench-flag-" + string(rune('a'+i%26)) + string(rune('0'+i/26%10))
		config.Flags[flagKey] = &flag{
			Key:           flagKey,
			Enabled:       true,
			VariationType: valueTypeBoolean,
			Variations: map[string]*variant{
				"on": {Key: "on", Value: true},
			},
			Allocations: []*allocation{
				{
					Key:   "alloc-bench",
					Rules: []*rule{},
					Splits: []*split{
						{Shards: []*shard{}, VariationKey: "on"},
					},
				},
			},
		}
	}
	return config
}

// makeBenchmarkContext creates a FlattenedContext with numFields attributes.
func makeBenchmarkContext(numFields int) openfeature.FlattenedContext {
	ctx := openfeature.FlattenedContext{
		"targetingKey": "bench-user-001",
	}
	for i := 1; i < numFields; i++ {
		ctx["field"+string(rune('a'+i%26))] = "value"
	}
	return ctx
}

// BenchmarkFlagEvaluationNoop measures the raw evaluation cost with zero hooks —
// the pure evaluator baseline for the three-column overhead comparison (CONT-08 / D-11).
//
// Profile: typical (100 flags, 50-user simulation, 10-field context).
// Profile: stress  (10 flags, 1000-user simulation, 250-field context — near degraded trigger).
//
// Run command:
//
//	GOFLAGS=-mod=readonly go test ./openfeature -run='^$' -bench='^BenchmarkFlagEvaluation' \
//	  -benchmem -count=3 -cpu=8
func BenchmarkFlagEvaluationNoop(b *testing.B) {
	profiles := []struct {
		name      string
		numFlags  int
		numUsers  int
		numFields int
	}{
		{"typical/100flags_50users_10fields", 100, 50, 10},
		{"stress/10flags_1000users_250fields", 10, 1000, 250},
	}

	for _, p := range profiles {
		b.Run(p.name, func(b *testing.B) {
			// Noop: provider with no hooks (nil out hooks after construction)
			provider := newDatadogProvider(ProviderConfig{})
			provider.flagEvalHook = nil // no OTel hook
			provider.exposureHook = nil // no exposure hook
			config := makeBenchmarkConfig(p.numFlags)
			provider.updateConfiguration(config)

			ctx := context.Background()
			flatCtx := makeBenchmarkContext(p.numFields)

			b.ReportAllocs()
			b.ResetTimer()

			i := 0
			for b.Loop() {
				// Rotate user targeting key to simulate p.numUsers distinct users.
				// Use a per-iteration counter (not b.N, which is a single fixed total
				// under b.Loop()) so the cardinality profile is actually exercised (WR-04).
				flatCtx["targetingKey"] = "bench-user-" + strconv.Itoa(i%p.numUsers)
				i++
				_ = provider.BooleanEvaluation(ctx, "bool-flag", false, flatCtx)
			}
		})
	}
}

// BenchmarkFlagEvaluationOTelOnly measures the evaluation cost with only the existing
// OTel feature_flag.evaluations hook (Path A — preserved baseline) (CONT-08 / D-11).
func BenchmarkFlagEvaluationOTelOnly(b *testing.B) {
	profiles := []struct {
		name      string
		numFlags  int
		numUsers  int
		numFields int
	}{
		{"typical/100flags_50users_10fields", 100, 50, 10},
		{"stress/10flags_1000users_250fields", 10, 1000, 250},
	}

	for _, p := range profiles {
		b.Run(p.name, func(b *testing.B) {
			// OTel-only: provider with flagEvalHook (OTel) but no EVP hook
			provider := newDatadogProvider(ProviderConfig{})
			provider.exposureHook = nil // no exposure hook — isolate OTel cost only
			// provider.flagEvalHook is set by newDatadogProvider — keep it
			config := makeBenchmarkConfig(p.numFlags)
			provider.updateConfiguration(config)

			ctx := context.Background()
			flatCtx := makeBenchmarkContext(p.numFields)

			b.ReportAllocs()
			b.ResetTimer()

			i := 0
			for b.Loop() {
				flatCtx["targetingKey"] = "bench-user-" + strconv.Itoa(i%p.numUsers)
				i++
				_ = provider.BooleanEvaluation(ctx, "bool-flag", false, flatCtx)
			}
		})
	}
}

// BenchmarkFlagEvaluationOTelPlusEVP measures the marginal cost of adding the new EVP
// flagevaluation hook alongside the existing OTel hook (Path A + Path B) (CONT-08 / D-11).
//
// The EVP writer uses a 24-hour flush interval (effectively infinite) so no HTTP round-trip
// is in the hot path — this benchmarks only the hook + aggregator cost (RESEARCH Pitfall 4).
func BenchmarkFlagEvaluationOTelPlusEVP(b *testing.B) {
	profiles := []struct {
		name      string
		numFlags  int
		numUsers  int
		numFields int
	}{
		{"typical/100flags_50users_10fields", 100, 50, 10},
		{"stress/10flags_1000users_250fields", 10, 1000, 250},
	}

	for _, p := range profiles {
		b.Run(p.name, func(b *testing.B) {
			// OTel+EVP: provider with both OTel hook and EVP hook wired.
			// The EVP writer uses a 24-hour flush interval so no HTTP round-trip
			// is in the hot path — benchmarks hook + aggregator cost only.
			provider := newDatadogProvider(ProviderConfig{
				FlagEvaluationFlushInterval: 24 * time.Hour, // never flush during bench
			})
			provider.exposureHook = nil // isolate hook overhead; no exposure cost

			config := makeBenchmarkConfig(p.numFlags)
			provider.updateConfiguration(config)

			ctx := context.Background()
			flatCtx := makeBenchmarkContext(p.numFields)

			b.ReportAllocs()
			b.ResetTimer()

			i := 0
			for b.Loop() {
				flatCtx["targetingKey"] = "bench-user-" + strconv.Itoa(i%p.numUsers)
				i++
				_ = provider.BooleanEvaluation(ctx, "bool-flag", false, flatCtx)
			}
		})
	}
}
