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
	"github.com/stretchr/testify/require"
)

func newBenchmarkClient(b *testing.B) *openfeature.Client {
	b.Helper()

	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	require.NoError(b, openfeature.SetProviderAndWait(provider))
	b.Cleanup(provider.Shutdown)

	return openfeature.NewClient("flageval-bench")
}

// BenchmarkOpenFeatureClientEvaluation benchmarks boolean flag evaluation through the OpenFeature client.
func BenchmarkOpenFeatureClientEvaluation(b *testing.B) {
	client := newBenchmarkClient(b)
	ctx := context.Background()
	evalCtx := openfeature.NewEvaluationContext("user-123", map[string]any{
		"country": "US",
	})

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_, _ = client.BooleanValue(ctx, "bool-flag", false, evalCtx)
	}
}

// BenchmarkOpenFeatureClientConcurrentEvaluations benchmarks concurrent flag evaluations through the OpenFeature client.
func BenchmarkOpenFeatureClientConcurrentEvaluations(b *testing.B) {
	client := newBenchmarkClient(b)
	ctx := context.Background()
	evalCtx := openfeature.NewEvaluationContext("user-123", map[string]any{
		"country": "US",
	})

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = client.BooleanValue(ctx, "bool-flag", false, evalCtx)
		}
	})
}

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

// BenchmarkEvaluation benchmarks object flag evaluation
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
	// scale profile targets the team's >=2,500-flag goal. Flag count is the dimension under
	// test, so it dominates; users/fields are kept modest (500/20) so the flag-cardinality
	// signal is not swamped by per-evaluation context cost and the suite stays tractable.
	{"scale/2500flags_500users_20fields", 2500, 500, 20},
}

// runFlagEvalBenchmark drives evaluations through a real OpenFeature client so the provider's
// registered hooks actually run - calling provider.BooleanEvaluation directly would bypass
// Hooks() and every column would measure the same bare evaluator. Flag and targeting keys
// rotate to exercise flag- and user-cardinality.
//
// configureHooks nils whichever provider hooks the tier under test should exclude (it runs
// before registration; Init does not recreate hooks, so the choice sticks). The 24h flush
// interval keeps the EVP writer from flushing during the run, so no HTTP round-trip enters
// the hot path - isolating hook + aggregator cost.
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

// BenchmarkFlagEvaluationNoop measures the client+provider evaluation cost with no hooks -
// the baseline column for the three-column overhead comparison.
//
// Run command:
//
//	GOFLAGS=-mod=readonly go test ./openfeature -run='^$' -bench='^BenchmarkFlagEvaluation' \
//	  -benchmem -count=3 -cpu=8
func BenchmarkFlagEvaluationNoop(b *testing.B) {
	runFlagEvalBenchmark(b, func(p *DatadogProvider) {
		p.flagEvalMetricsHook = nil
		p.flagEvalLoggingHook = nil
		p.flagEvalLoggingWriter = nil
		p.exposureHook = nil
	})
}

// BenchmarkFlagEvaluationOTelOnly measures the cost with only the existing OTel
// feature_flag.evaluations hook (Path A - the preserved baseline). The EVP hook and writer
// are disabled so this column isolates the OTel hook's cost.
func BenchmarkFlagEvaluationOTelOnly(b *testing.B) {
	runFlagEvalBenchmark(b, func(p *DatadogProvider) {
		p.flagEvalLoggingHook = nil
		p.flagEvalLoggingWriter = nil
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

// BenchmarkFlagEvaluationEVPRecord isolates the EVP hook's synchronous hot-path cost: scalar
// extraction + shallow context copy + non-blocking enqueue, all on the evaluation goroutine.
// A discard drainer keeps the queue empty so the asynchronous aggregation (flatten/prune/hash/
// add, done by the real worker off the hot path) is NOT attributed here - this is the latency a
// flag evaluation actually pays when the EVP path is enabled.
func BenchmarkFlagEvaluationEVPRecord(b *testing.B) {
	for _, p := range flagEvalBenchProfiles {
		b.Run(p.name, func(b *testing.B) {
			w := newFlagEvalLoggingWriter(ProviderConfig{FlagEvaluationFlushInterval: 24 * time.Hour})

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

// scaleBenchProfile is the >=2,500-flag profile (the team's scale target) selected from
// flagEvalBenchProfiles by name. It panics if the profile is removed so the scale benches
// fail loudly rather than silently degrade to a smaller config.
func scaleBenchProfile() flagEvalBenchProfile {
	const name = "scale/2500flags_500users_20fields"
	for _, p := range flagEvalBenchProfiles {
		if p.name == name {
			return p
		}
	}
	panic("scale profile " + name + " missing from flagEvalBenchProfiles")
}

// BenchmarkFlagEvaluationOTelPlusEVPParallel measures concurrent server-side evaluation
// through a real OpenFeature client with BOTH the OTel hook and the new EVP flagevaluation
// hook enabled, at the >=2,500-flag scale profile. Because every
// concurrent evaluation funnels through the EVP writer's single aggregator mutex
// (flagEvalLoggingAggregator.add), this is the bench that surfaces lock contention under
// realistic multi-goroutine server load.
//
// Each goroutine rotates its own flag key and targeting key across the full cardinality so the
// aggregator sees a realistic spread of buckets, not a single hot key. The 24h flush interval
// keeps the EVP writer from flushing mid-run so no HTTP round-trip enters the hot path.
//
// Run command:
//
//	GOFLAGS=-mod=readonly go test ./openfeature -run='^$' \
//	  -bench='^BenchmarkFlagEvaluationOTelPlusEVPParallel$' -benchmem -count=2 -cpu=8
func BenchmarkFlagEvaluationOTelPlusEVPParallel(b *testing.B) {
	p := scaleBenchProfile()

	provider := newDatadogProvider(ProviderConfig{FlagEvaluationFlushInterval: 24 * time.Hour})
	// Disable only the exposure hook; OTel hook + EVP hook + aggregator all run, so this
	// measures the combined Path A + Path B cost under contention.
	provider.exposureHook = nil

	config := makeBenchmarkConfig(p.numFlags)
	provider.updateConfiguration(config)

	initCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := openfeature.SetProviderWithContextAndWait(initCtx, provider); err != nil {
		cancel()
		b.Fatalf("set provider: %v", err)
	}
	cancel()
	client := openfeature.NewClient("flageval-bench-parallel")

	flagKeys := boolBenchmarkFlagKeys(config)
	attrs := makeBenchmarkAttrs(p.numFields)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			evalCtx := openfeature.NewEvaluationContext("bench-user-"+strconv.Itoa(i%p.numUsers), attrs)
			_ = client.Boolean(ctx, flagKeys[i%len(flagKeys)], false, evalCtx)
			i++
		}
	})
}
