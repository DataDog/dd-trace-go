// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"testing"

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

	for i := 0; i < b.N; i++ {
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

	for i := 0; i < b.N; i++ {
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

	for i := 0; i < b.N; i++ {
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

	for i := 0; i < b.N; i++ {
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

	for i := 0; i < b.N; i++ {
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

			for i := 0; i < b.N; i++ {
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

			for i := 0; i < b.N; i++ {
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
