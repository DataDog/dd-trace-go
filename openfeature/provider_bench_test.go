// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"sort"
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

			// Add additional fields with unique names so all numFields are distinct.
			for i := 1; i < size.numFields; i++ {
				flatCtx["field"+strconv.Itoa(i)] = i
			}

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_ = provider.BooleanEvaluation(ctx, "bool-flag", false, flatCtx)
			}
		})
	}
}

// BenchmarkEvaluationWithVaryingFlagCounts benchmarks evaluation across configs with
// different numbers of flags. The evaluated flag key rotates across all configured flags,
// so flag-cardinality is exercised rather than repeatedly evaluating a single key.
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

			// Add additional flags with unique monotonic keys so cardinality is exercised.
			for i := len(config.Flags); i < count.numFlags; i++ {
				flagKey := "flag-" + strconv.Itoa(i)
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

			// Rotate across all boolean flags so a larger config is actually exercised,
			// not just a single repeated key.
			flagKeys := boolBenchmarkFlagKeys(config)

			b.ReportAllocs()
			b.ResetTimer()

			i := 0
			for b.Loop() {
				_ = provider.BooleanEvaluation(ctx, flagKeys[i%len(flagKeys)], false, flatCtx)
				i++
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
// Flag keys are unique monotonic integers ("bench-flag-N") so the claimed cardinality
// is fully exercised without wrapping.
func makeBenchmarkConfig(numFlags int) *universalFlagsConfiguration {
	config := createTestConfig()
	for i := len(config.Flags); i < numFlags; i++ {
		flagKey := "bench-flag-" + strconv.Itoa(i)
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

// boolBenchmarkFlagKeys returns the boolean flag keys in cfg in deterministic order, so a
// benchmark can rotate the evaluated flag and exercise flag-cardinality (not just users).
func boolBenchmarkFlagKeys(cfg *universalFlagsConfiguration) []string {
	keys := make([]string, 0, len(cfg.Flags))
	for k, f := range cfg.Flags {
		if f.VariationType == valueTypeBoolean {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// makeBenchmarkAttrs builds an evaluation-context attribute map with numFields-1 distinct
// fields (the targeting key is supplied separately to NewEvaluationContext, so the flattened
// context has numFields entries total). Field names are "fieldN" so all are distinct.
func makeBenchmarkAttrs(numFields int) map[string]any {
	attrs := make(map[string]any, numFields)
	for i := 1; i < numFields; i++ {
		attrs["field"+strconv.Itoa(i)] = "value"
	}
	return attrs
}

// flagEvalBenchProfile is one load profile for the three-column overhead comparison.
type flagEvalBenchProfile struct {
	name      string
	numFlags  int
	numUsers  int
	numFields int
}

// flagEvalBenchProfiles are shared by the Noop / OTel-only / OTel+EVP benchmarks so the
// three columns are directly comparable.
var flagEvalBenchProfiles = []flagEvalBenchProfile{
	{"typical/100flags_50users_10fields", 100, 50, 10},
	{"stress/10flags_1000users_250fields", 10, 1000, 250},
}

// runFlagEvalBenchmark drives evaluations through a real OpenFeature client so the
// provider's registered hooks actually run. Calling provider.BooleanEvaluation directly
// would bypass Hooks(), so neither the OTel nor the EVP hook would execute and all three
// columns would measure the same bare evaluator. Both the evaluated flag key and the
// targeting key rotate, so flag- and user-cardinality are exercised.
//
// configureHooks nils whichever provider hooks the tier under test should exclude. It runs
// before the provider is registered; Init does not recreate hooks, so the choice sticks.
// The provider uses a 24h flush interval, so the EVP writer never flushes during the run —
// no HTTP round-trip enters the hot path, isolating hook + aggregator cost.
func runFlagEvalBenchmark(b *testing.B, configureHooks func(p *DatadogProvider)) {
	b.Helper()
	for _, p := range flagEvalBenchProfiles {
		b.Run(p.name, func(b *testing.B) {
			provider := newDatadogProvider(ProviderConfig{FlagEvaluationFlushInterval: 24 * time.Hour})
			configureHooks(provider)

			config := makeBenchmarkConfig(p.numFlags)
			provider.updateConfiguration(config)

			initCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := openfeature.SetProviderWithContextAndWait(initCtx, provider); err != nil {
				cancel()
				b.Fatalf("set provider: %v", err)
			}
			cancel()
			client := openfeature.NewClient("flageval-bench")

			flagKeys := boolBenchmarkFlagKeys(config)
			attrs := makeBenchmarkAttrs(p.numFields)
			ctx := context.Background()

			b.ReportAllocs()
			b.ResetTimer()

			i := 0
			for b.Loop() {
				evalCtx := openfeature.NewEvaluationContext("bench-user-"+strconv.Itoa(i%p.numUsers), attrs)
				_ = client.Boolean(ctx, flagKeys[i%len(flagKeys)], false, evalCtx)
				i++
			}
		})
	}
}

// BenchmarkFlagEvaluationNoop measures the client+provider evaluation cost with no hooks —
// the baseline column for the three-column overhead comparison.
//
// Run command:
//
//	GOFLAGS=-mod=readonly go test ./openfeature -run='^$' -bench='^BenchmarkFlagEvaluation' \
//	  -benchmem -count=3 -cpu=8
func BenchmarkFlagEvaluationNoop(b *testing.B) {
	runFlagEvalBenchmark(b, func(p *DatadogProvider) {
		p.flagEvalHook = nil
		p.flagEvalEVPHook = nil
		p.flagEvalWriter = nil
		p.exposureHook = nil
	})
}

// BenchmarkFlagEvaluationOTelOnly measures the cost with only the existing OTel
// feature_flag.evaluations hook (Path A — the preserved baseline). The EVP hook and writer
// are disabled so this column isolates the OTel hook's cost.
func BenchmarkFlagEvaluationOTelOnly(b *testing.B) {
	runFlagEvalBenchmark(b, func(p *DatadogProvider) {
		p.flagEvalEVPHook = nil
		p.flagEvalWriter = nil
		p.exposureHook = nil
	})
}

// BenchmarkFlagEvaluationOTelPlusEVP measures the marginal cost of adding the new EVP
// flagevaluation hook alongside the existing OTel hook (Path A + Path B). Only the exposure
// hook is disabled; the OTel hook, EVP hook, and aggregator all run.
func BenchmarkFlagEvaluationOTelPlusEVP(b *testing.B) {
	runFlagEvalBenchmark(b, func(p *DatadogProvider) {
		p.exposureHook = nil
	})
}

// BenchmarkFlagEvaluationEVPRecord isolates the EVP hook's synchronous hot-path cost — the
// scalar extraction + shallow context copy (EvaluationContext().Attributes()) + non-blocking
// enqueue that runs on the evaluation goroutine. A discard drainer empties the queue so it
// never fills, and the asynchronous aggregation (flatten/prune/hash/add, performed by the
// real worker off the hot path) is NOT attributed here. This is the latency a flag
// evaluation actually pays when the EVP path is enabled — distinct from the client
// benchmarks above, whose process-wide allocs/wall-clock also capture the worker's work.
func BenchmarkFlagEvaluationEVPRecord(b *testing.B) {
	for _, p := range flagEvalBenchProfiles {
		b.Run(p.name, func(b *testing.B) {
			w := newFlagEvaluationWriter(ProviderConfig{FlagEvaluationFlushInterval: 24 * time.Hour})

			// Discard drainer: keep the queue empty without aggregating, so only the
			// synchronous enqueue cost is measured here.
			done := make(chan struct{})
			go func() {
				for {
					select {
					case <-w.events:
					case <-done:
						return
					}
				}
			}()
			defer close(done)

			hookCtx := makeHookContext("bool-flag", "bench-user", makeBenchmarkAttrs(p.numFields))
			details := makeEvalDetails("on", openfeature.TargetingMatchReason, "")

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				w.record(hookCtx, details)
			}
		})
	}
}
