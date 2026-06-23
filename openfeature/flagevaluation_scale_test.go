// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"fmt"
	"testing"
	"time"
)

// These tests drive flagEvaluationAggregator.add directly (no client, no hooks, no async worker)
// to assert the 2,500-flag scale target against production aggregation caps.

// scaleFlagShape describes the realistic per-flag structure used to size the degraded tier.
// Degraded key includes schema-visible retained fields only. OpenFeature reason is not accepted
// by the worker schema and is not part of degraded cardinality.
type scaleFlagShape struct {
	variants    int // distinct variant keys this flag can return
	allocations int // distinct allocation keys this flag can return
}

// makeScaleFlags builds n flags with a realistic spread of variants/allocations. The spread is
// deterministic (driven by index modulo) so the degraded cardinality is exactly reproducible:
//   - ~1/3 of flags: 2 variants, 1 allocation  (simple on/off)
//   - ~1/3 of flags: 3 variants, 1 allocation  (multivariate)
//   - ~1/3 of flags: 4 variants, 2 allocations (multivariate + multiple allocations)
func makeScaleFlags(n int) []scaleFlagShape {
	flags := make([]scaleFlagShape, n)
	for i := range n {
		switch i % 3 {
		case 0:
			flags[i] = scaleFlagShape{variants: 2, allocations: 1}
		case 1:
			flags[i] = scaleFlagShape{variants: 3, allocations: 1}
		default:
			flags[i] = scaleFlagShape{variants: 4, allocations: 2}
		}
	}
	return flags
}

// legitimateDegradedCardinality returns the number of DISTINCT degraded buckets the given flag
// shapes would produce if every (variant × allocation) combination were observed.
// This is the count degradedCap must hold without dropping legitimate buckets.
func legitimateDegradedCardinality(flags []scaleFlagShape) int {
	total := 0
	for _, f := range flags {
		total += f.variants * f.allocations
	}
	return total
}

// driveScale records evaluations into agg for the given flag shapes, distributing each flag's
// evaluations across its variant/allocation combinations and across numContexts distinct
// evaluation contexts (subjects). evalsPerCombo controls how many subjects hit each combination
// (so counts accumulate). It returns the total number of add() calls made.
//
// The context cardinality knob (numContexts) is what splits the FULL tier: the full key includes
// targetingKey + contextKey, so more distinct subjects => more full buckets => earlier full-tier
// saturation => earlier cascade into degraded.
func driveScale(agg *flagEvaluationAggregator, flags []scaleFlagShape, numContexts, evalsPerCombo int) int64 {
	nowMs := time.Now().UnixMilli()
	var calls int64
	ctxCounter := 0
	for fi, f := range flags {
		flagKey := fmt.Sprintf("flag-%05d", fi)
		for v := range f.variants {
			variant := fmt.Sprintf("v%d", v)
			for a := range f.allocations {
				alloc := fmt.Sprintf("alloc-%d", a)
				for range evalsPerCombo {
					// Spread subjects across numContexts distinct targeting keys + contexts.
					subj := ctxCounter % numContexts
					ctxCounter++
					d := evalDetails{
						flagKey:       flagKey,
						variant:       variant,
						allocationKey: alloc,
						targetingKey:  fmt.Sprintf("user-%d", subj),
					}
					attrs := map[string]any{
						"country": fmt.Sprintf("c%d", subj%50),
						"plan":    fmt.Sprintf("p%d", subj%5),
					}
					agg.add(d, attrs, nowMs)
					calls++
				}
			}
		}
	}
	return calls
}

// tierCounts returns observable aggregator state after a run. Over-cap degraded counts land in
// droppedDegradedOverflow (observable, not silent).
// sumCounts includes the drop counter so the count-preservation invariant is Σ == add() calls.
type tierCounts struct {
	full, degraded int
	dropped        int64
	globalCount    int
	sumCounts      int64
}

func snapshot(agg *flagEvaluationAggregator) tierCounts {
	agg.mu.Lock()
	defer agg.mu.Unlock()
	tc := tierCounts{
		full:        len(agg.full),
		degraded:    len(agg.degraded),
		dropped:     agg.droppedDegradedOverflow,
		globalCount: agg.globalCount,
	}
	for _, e := range agg.full {
		tc.sumCounts += e.count
	}
	for _, e := range agg.degraded {
		tc.sumCounts += e.count
	}
	tc.sumCounts += agg.droppedDegradedOverflow
	return tc
}

// TestScaleDegradedCardinality2500Flags verifies the production degradedCap has at least 2x
// headroom over the legitimate degraded cardinality of the 2,500-flag target shape.
func TestScaleDegradedCardinality2500Flags(t *testing.T) {
	const n = 2500
	flags := makeScaleFlags(n)
	deg := legitimateDegradedCardinality(flags)

	// Per-shape breakdown for the report.
	var s0, s1, s2 int
	for i := range n {
		switch i % 3 {
		case 0:
			s0++
		case 1:
			s1++
		default:
			s2++
		}
	}
	t.Logf("2,500-flag realistic shape:")
	t.Logf("  %d flags @ 2v×1a = %d degraded buckets", s0, s0*2*1)
	t.Logf("  %d flags @ 3v×1a = %d degraded buckets", s1, s1*3*1)
	t.Logf("  %d flags @ 4v×2a = %d degraded buckets", s2, s2*4*2)
	t.Logf("LEGITIMATE degraded cardinality (Σ variants×allocations) = %d", deg)
	t.Logf("production degradedCap = %d", defaultEvalDegradedCap)
	t.Logf("production globalCap   = %d", defaultEvalGlobalCap)

	recDegraded := roundUpTo(deg*2, 1000)
	if defaultEvalDegradedCap < recDegraded {
		t.Fatalf("degradedCap=%d does not provide 2x headroom over legitimate cardinality=%d; want >= %d",
			defaultEvalDegradedCap, deg, recDegraded)
	}
	t.Logf("degradedCap headroom OK: cap=%d legitimate=%d requiredWith2xHeadroom=%d",
		defaultEvalDegradedCap, deg, recDegraded)
}

func roundUpTo(v, mult int) int {
	if v%mult == 0 {
		return v
	}
	return ((v / mult) + 1) * mult
}

// TestScaleDropTriggerSweep verifies that the 2,500-flag target shape does not drop legitimate
// counts across representative context-cardinality points with production caps.
func TestScaleDropTriggerSweep(t *testing.T) {
	const n = 2500
	flags := makeScaleFlags(n)
	deg := legitimateDegradedCardinality(flags)
	t.Logf("2,500 flags; legitimate degraded cardinality = %d; production caps "+
		"full=%d perFlag=%d degraded=%d (full -> degraded -> drop)",
		deg, defaultEvalGlobalCap, defaultEvalPerFlagCap, defaultEvalDegradedCap)

	// Sweep distinct-context cardinality. Low = few subjects (full tier stays small);
	// high = many subjects (full tier saturates, cascading to degraded then drop).
	sweep := []struct {
		name        string
		numContexts int
		evalsPer    int
	}{
		{"few-contexts (10 subjects)", 10, 2},
		{"moderate-contexts (1k subjects)", 1000, 1},
		{"many-contexts (100k subjects)", 100_000, 1},
		{"extreme-contexts (1M subjects)", 1_000_000, 1},
	}

	for _, sp := range sweep {
		t.Run(sp.name, func(t *testing.T) {
			agg := newTestAggregator(
				defaultEvalGlobalCap,
				defaultEvalPerFlagCap,
				defaultEvalDegradedCap,
			)
			calls := driveScale(agg, flags, sp.numContexts, sp.evalsPer)
			tc := snapshot(agg)

			t.Logf("contexts=%d evalsPerCombo=%d => add() calls=%d", sp.numContexts, sp.evalsPer, calls)
			t.Logf("  full=%d (globalCount=%d, cap=%d)  degraded=%d (cap=%d)  droppedDegradedOverflow=%d",
				tc.full, tc.globalCount, defaultEvalGlobalCap,
				tc.degraded, defaultEvalDegradedCap, tc.dropped)
			t.Logf("  Σ counts (full+degraded+dropped)=%d (must == add() calls=%d => %v)",
				tc.sumCounts, calls, tc.sumCounts == calls)

			// Count preservation must hold: nothing silently lost.
			if tc.sumCounts != calls {
				t.Errorf("count preservation violated: Σ=%d != calls=%d", tc.sumCounts, calls)
			}

			if tc.dropped != 0 {
				t.Errorf("unexpected degraded overflow drops at %s: got %d", sp.name, tc.dropped)
			}
		})
	}
}

// TestScaleDropRequiresDegradedSaturation isolates the precondition for a terminal-tier drop:
// the degraded tier must be full. With production degradedCap and realistic 2,500-flag structure,
// it forces all buckets through the degraded tier and asserts that no legitimate counts drop.
func TestScaleDropRequiresDegradedSaturation(t *testing.T) {
	const n = 2500
	flags := makeScaleFlags(n)
	deg := legitimateDegradedCardinality(flags)

	// Force the full tier to be useless (globalCap=0) so EVERY new bucket cascades immediately
	// to the degraded path — the worst case for the degraded tier at 2,500 flags.
	agg := newTestAggregator(0, defaultEvalPerFlagCap, defaultEvalDegradedCap)
	calls := driveScale(agg, flags, 100_000, 1)
	tc := snapshot(agg)

	t.Logf("WORST CASE for degraded tier (globalCap=0 => everything cascades to degraded):")
	t.Logf("  legitimate degraded cardinality = %d, degradedCap = %d", deg, defaultEvalDegradedCap)
	t.Logf("  result: full=%d degraded=%d droppedDegradedOverflow=%d  Σ=%d/calls=%d",
		tc.full, tc.degraded, tc.dropped, tc.sumCounts, calls)

	if deg > defaultEvalDegradedCap {
		t.Errorf("legitimate degraded cardinality %d EXCEEDS degradedCap %d — aggregation would DROP "+
			"legitimate counts at 2,500 flags; raise degradedCap", deg, defaultEvalDegradedCap)
	} else {
		t.Logf("  => all %d legitimate degraded buckets FIT under degradedCap %d (headroom %d); "+
			"no legitimate count dropped at the scale target.",
			deg, defaultEvalDegradedCap, defaultEvalDegradedCap-deg)
		if tc.dropped != 0 {
			t.Errorf("unexpected drops (%d) when legitimate cardinality fits under degradedCap", tc.dropped)
		}
	}

	if tc.sumCounts != calls {
		t.Errorf("count preservation violated: Σ=%d != calls=%d", tc.sumCounts, calls)
	}
}

// TestScaleFullSaturationCascade saturates the full tier naturally with production caps and
// verifies that overflow cascades to degraded without dropping legitimate counts.
func TestScaleFullSaturationCascade(t *testing.T) {
	const n = 2500
	flags := makeScaleFlags(n)
	deg := legitimateDegradedCardinality(flags)

	agg := newTestAggregator(
		defaultEvalGlobalCap,
		defaultEvalPerFlagCap,
		defaultEvalDegradedCap,
	)
	// With 16 distinct subjects per combo the full tier sees ~173k distinct full keys, over
	// globalCap, forcing overflow into degraded.
	calls := driveScale(agg, flags, 1_000_000, 16)
	tc := snapshot(agg)

	t.Logf("FULL-saturation cascade (production caps; 16 subjects/combo):")
	t.Logf("  legitimate degraded cardinality=%d  add() calls=%d", deg, calls)
	t.Logf("  full=%d (globalCount=%d, cap=%d)  degraded=%d (cap=%d)  droppedDegradedOverflow=%d",
		tc.full, tc.globalCount, defaultEvalGlobalCap,
		tc.degraded, defaultEvalDegradedCap, tc.dropped)
	t.Logf("  Σ counts=%d / calls=%d (preserved=%v)", tc.sumCounts, calls, tc.sumCounts == calls)

	if tc.full > defaultEvalGlobalCap {
		t.Errorf("full tier %d exceeded globalCap %d", tc.full, defaultEvalGlobalCap)
	}
	if tc.globalCount != defaultEvalGlobalCap {
		t.Errorf("full tier did not saturate: globalCount=%d, want %d", tc.globalCount, defaultEvalGlobalCap)
	}
	if tc.degraded == 0 {
		t.Error("expected degraded tier to absorb full-tier overflow")
	}
	if tc.dropped != 0 {
		t.Errorf("unexpected degraded overflow drops: got %d", tc.dropped)
	}
	if tc.sumCounts != calls {
		t.Errorf("count preservation violated: Σ=%d != calls=%d", tc.sumCounts, calls)
	}
}

// TestScaleHotFlagPerFlagCap drives a single flag past perFlagCap and asserts that overflow
// reaches the degraded tier, then becomes counted drops once degradedCap is saturated.
func TestScaleHotFlagPerFlagCap(t *testing.T) {
	agg := newTestAggregator(
		defaultEvalGlobalCap,
		defaultEvalPerFlagCap,
		defaultEvalDegradedCap,
	)
	nowMs := time.Now().UnixMilli()

	// One hot flag, many distinct (variant, subject) combos so it blows past perFlagCap and then
	// keeps generating distinct degraded keys. To fill the resized degradedCap we need that many
	// distinct schema-visible variant/allocation combinations for this one flag.
	const distinctVariants = 50_000
	var calls int64
	for v := range distinctVariants {
		d := evalDetails{
			flagKey:       "hot-flag",
			variant:       fmt.Sprintf("v%d", v),
			allocationKey: "alloc-0",
			targetingKey:  fmt.Sprintf("user-%d", v),
		}
		agg.add(d, map[string]any{"k": v}, nowMs)
		calls++
	}
	tc := snapshot(agg)

	t.Logf("HOT-flag perFlagCap path (single flag, %d distinct variants):", distinctVariants)
	t.Logf("  full=%d (perFlagCap=%d)  degraded=%d (cap=%d)  droppedDegradedOverflow=%d",
		tc.full, defaultEvalPerFlagCap, tc.degraded, defaultEvalDegradedCap, tc.dropped)
	t.Logf("  Σ counts=%d / calls=%d (preserved=%v)", tc.sumCounts, calls, tc.sumCounts == calls)
	if tc.full != defaultEvalPerFlagCap {
		t.Errorf("full tier did not stop at perFlagCap: full=%d, want %d", tc.full, defaultEvalPerFlagCap)
	}
	if tc.degraded != defaultEvalDegradedCap {
		t.Errorf("degraded tier did not stop at degradedCap: degraded=%d, want %d", tc.degraded, defaultEvalDegradedCap)
	}
	if tc.dropped == 0 {
		t.Error("expected counted drops after single hot flag saturates degradedCap")
	}
	if tc.sumCounts != calls {
		t.Errorf("count preservation violated: Σ=%d != calls=%d", tc.sumCounts, calls)
	}
}
