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

// These tests drive flagEvaluationAggregator.add directly (no client, no hooks, no async
// worker) to deterministically simulate the team's >=2,500-flag scale target with realistic
// flag structure, then inspect the two aggregation maps. The goal is to answer:
//
//  1. Does 2,500 flags' worth of legitimate schema-visible degraded buckets fit under degradedCap,
//     or does it overflow and drop under a 2-tier design?
//  2. What degradedCap/globalCap values make a 2-tier design never drop legitimate counts at
//     2,500 flags?
//
// They are not assertions about correct behavior so much as a measurement harness; each test
// logs its numbers via t.Logf and is intended to be read with `go test -run TestScale -v`.

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
// This is the count a 2-tier design must be able to hold without dropping.
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
// saturation => earlier cascade into degraded (and potentially ultra).
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

// tierCounts returns observable aggregator state after a run. In the 2-tier design the terminal
// tier is degraded; over-cap counts land in droppedDegradedOverflow (observable, not silent).
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

// TestScaleDegradedCardinality2500Flags reports the LEGITIMATE degraded cardinality for the
// realistic 2,500-flag shape and compares it to the production degradedCap (10,000). This is the
// cap-sizing math that decides whether a 2-tier design drops legitimate counts.
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

	if deg > defaultEvalDegradedCap {
		t.Logf("RESULT: legitimate degraded cardinality %d EXCEEDS degradedCap %d by %d "+
			"=> under a 2-tier design these would DROP. degradedCap must be raised.",
			deg, defaultEvalDegradedCap, deg-defaultEvalDegradedCap)
	} else {
		t.Logf("RESULT: legitimate degraded cardinality %d FITS under degradedCap %d (headroom %d).",
			deg, defaultEvalDegradedCap, defaultEvalDegradedCap-deg)
	}

	// Recommendation math: degraded must hold full legitimate cardinality; global (full tier)
	// must hold flags × contexts up to a reasonable subject bound. Report a 2x-headroom rec.
	recDegraded := roundUpTo(deg*2, 1000)
	t.Logf("RECOMMENDATION: degradedCap >= %d (2x headroom over %d legitimate buckets).", recDegraded, deg)
}

func roundUpTo(v, mult int) int {
	if v%mult == 0 {
		return v
	}
	return ((v / mult) + 1) * mult
}

// TestScaleDropTriggerSweep is the decisive test for the 2-tier design: across a
// context-cardinality sweep at 2,500 flags, it reports whether the terminal-tier DROP
// (droppedDegradedOverflow) ever fires, and under what conditions. It runs each sweep point with
// the (resized) production caps.
func TestScaleDropTriggerSweep(t *testing.T) {
	const n = 2500
	flags := makeScaleFlags(n)
	deg := legitimateDegradedCardinality(flags)
	t.Logf("2,500 flags; legitimate degraded cardinality = %d; production caps "+
		"full=%d perFlag=%d degraded=%d (2-tier: full -> degraded -> drop)",
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

			if tc.dropped > 0 {
				t.Logf("  DROP TRIGGERED: %d evaluation(s) dropped (degraded saturated at %d).",
					tc.dropped, defaultEvalDegradedCap)
			} else {
				t.Logf("  DROP NOT TRIGGERED at this sweep point — no legitimate count lost.")
			}
		})
	}
}

// TestScaleDropRequiresDegradedSaturation isolates the precondition for a terminal-tier drop:
// the degraded tier must be FULL. With the resized degradedCap and realistic 2,500-flag
// structure, it forces the worst case (globalCap=0, everything cascades to degraded) and reports
// whether legitimate degraded cardinality fits under degradedCap — i.e. whether a 2-tier design
// drops any legitimate counts at the scale target.
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
		t.Errorf("legitimate degraded cardinality %d EXCEEDS degradedCap %d — 2-tier design would DROP "+
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

// TestScaleFullSaturationCascade saturates the FULL tier naturally (production caps) with
// 2,500 flags × enough distinct subjects that globalCount exceeds globalCap, then reports which
// tier absorbs the overflow. In the 2-tier design, global-cap overflow cascades to degraded
// (which is sized to hold the legitimate degraded cardinality) before any drop.
func TestScaleFullSaturationCascade(t *testing.T) {
	const n = 2500
	flags := makeScaleFlags(n)
	deg := legitimateDegradedCardinality(flags)

	agg := newTestAggregator(
		defaultEvalGlobalCap,
		defaultEvalPerFlagCap,
		defaultEvalDegradedCap,
	)
	// legitimate degraded cardinality is the schema-visible combo count; with enough distinct subjects per combo the
	// full tier sees ~173k distinct full keys, over globalCap, forcing overflow into degraded.
	calls := driveScale(agg, flags, 1_000_000, 8)
	tc := snapshot(agg)

	t.Logf("FULL-saturation cascade (production caps; 8 subjects/combo):")
	t.Logf("  legitimate degraded cardinality=%d  add() calls=%d", deg, calls)
	t.Logf("  full=%d (globalCount=%d, cap=%d)  degraded=%d (cap=%d)  droppedDegradedOverflow=%d",
		tc.full, tc.globalCount, defaultEvalGlobalCap,
		tc.degraded, defaultEvalDegradedCap, tc.dropped)
	t.Logf("  Σ counts=%d / calls=%d (preserved=%v)", tc.sumCounts, calls, tc.sumCounts == calls)

	if tc.full > defaultEvalGlobalCap {
		t.Errorf("full tier %d exceeded globalCap %d", tc.full, defaultEvalGlobalCap)
	}
	if tc.sumCounts != calls {
		t.Errorf("count preservation violated: Σ=%d != calls=%d", tc.sumCounts, calls)
	}
	t.Logf("  STRUCTURAL NOTE: global-cap overflow cascades to degraded (degraded=%d). "+
		"A drop only occurs once degraded itself reaches degradedCap=%d.",
		tc.degraded, defaultEvalDegradedCap)
}

// TestScaleHotFlagPerFlagCap drives a SINGLE flag past perFlagCap (10,000 distinct full
// buckets) to demonstrate the per-flag overflow path into the degraded tier, and reports whether
// that fill can in turn saturate degradedCap and trigger a terminal-tier drop.
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
	if tc.dropped > 0 {
		t.Logf("  DROP TRIGGERED via a single abusive/hot flag (degraded saturated at %d). "+
			"This is exactly the abuse case the drop counter must make observable.", tc.degraded)
	}
	if tc.sumCounts != calls {
		t.Errorf("count preservation violated: Σ=%d != calls=%d", tc.sumCounts, calls)
	}
}
