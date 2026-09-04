// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package main

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/glsleak"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// orchestrionEnabled is flipped to true by orchestrion at build time via the
// //dd:orchestrion-enabled directive. It is the orchestrion-specific signal, kept
// so TestBuiltWithOrchestrion can cross-check it against the runtime one.
//
//dd:orchestrion-enabled
const orchestrionEnabled = false

// glsWoven reports whether the GLS exists in this build, under either tool. Both
// weave the same reclaim path, so the assertions below have to hold for both.
var glsWoven = built.WithOrchestrion || otelc.Enabled()

func TestBuiltWithOrchestrion(t *testing.T) {
	require.Equal(t, built.WithOrchestrion, orchestrionEnabled)
}

// TestGLSNoHeapLeak is the comprehensive, end-to-end regression gate for
// orchestrion#782: it runs the shared owner/worker cross-goroutine workload
// (glsleak.MeasureLeak) at a soak-sized record count and fails if the retained
// heap objects per record regress. Without the reclaim fix the worker's GLS stack
// grows by one span per record (retention proportional to the record count); the
// fix keeps it flat.
//
// This is the orchestrion-native home for the korECM repro: internal/apps is
// built without orchestrion, so the leak (which only exists under orchestrion)
// cannot be exercised there — only this _integration lane runs woven.
func TestGLSNoHeapLeak(t *testing.T) {
	if !glsWoven {
		t.Skip("the GLS only exists in woven builds")
	}

	// WithSpanPool(false) is explicit rather than assumed. This test finishes the
	// span before handing it to the worker, which is a deliberate use-after-Finish
	// that the pool would legitimately recycle. Now that the Orchestrion gate is
	// gone, an inherited DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED=true would turn
	// pooling on here and make this exercise that unsupported path instead of the
	// non-pooled reclaim it is written for.
	require.NoError(t, tracer.Start(tracer.WithLogStartup(false), tracer.WithSpanPool(false)))
	defer tracer.Stop()

	r := glsleak.MeasureLeak(200_000)
	require.Lessf(t, r.PerRecord, glsleak.MaxRetainedObjectsPerRecord,
		"GLS span leak: %.3f retained heap objects/record (want flat ~0; the leak grows "+
			"one span per record) — the contextStack.Push reclaim in ddtrace/tracer/orchestrion.yml regressed",
		r.PerRecord)
}

// TestGLSNoHeapLeakWithSpanPool is the gate for running the experimental span pool
// together with orchestrion GLS — the combination that used to be force-disabled
// and that the liveness cell makes safe. It uses the live-inject workload, which
// respects the pool's "do not touch a span after Finish" contract, and turns
// pooling on explicitly so WithSpanPool(true) is exercised rather than merely
// accepted.
//
// The worker's GLS stack must stay flat even though every finished span is
// recycled. If the liveness signal ever moves back onto the span, this is what
// catches it: recycled spans would either leak (stale entries the drain no longer
// recognises) or resurface as the wrong active span. Run under -race in CI it also
// covers the span-pool-vs-GLS data races on the woven fields.
//
// TestGLSNoHeapLeak above stays the no-pool gate: its finish-then-inject order is
// a deliberate use-after-Finish that the pool would legitimately recycle, so it is
// not run pooled.
func TestGLSNoHeapLeakWithSpanPool(t *testing.T) {
	if !glsWoven {
		t.Skip("the GLS only exists in woven builds")
	}

	require.NoError(t, tracer.Start(tracer.WithLogStartup(false), tracer.WithSpanPool(true)))
	defer tracer.Stop()

	r := glsleak.MeasureLeakLiveInject(200_000)

	// Depth first: it is the assertion that actually fails when reclaim breaks.
	// The worker is kept alive across the measurement precisely so this can be
	// read, because orchestrion clears a goroutine's GLS in runtime.goexit1 and
	// an exited worker always measures clean.
	//
	// The retained-object bound below is kept, but on its own it does not catch
	// the pooled regression: recycled spans mean thousands of stale entries point
	// at a handful of objects, so objects/record stays near zero while the leak is
	// linear in entries and bytes.
	require.LessOrEqualf(t, r.Depth, glsleak.MaxRetainedEntries,
		"worker still holds %d GLS entries after %d records (want <= %d) — the cell-based "+
			"reclaim regressed, or the span pool recycled a span and stranded its entry",
		r.Depth, r.Records, glsleak.MaxRetainedEntries)

	require.Lessf(t, r.PerRecord, glsleak.MaxRetainedObjectsPerRecord,
		"GLS span leak with the span pool enabled: %.3f retained heap objects/record "+
			"(want flat ~0) — either the cell-based reclaim regressed, or the span pool and "+
			"orchestrion GLS no longer coexist safely",
		r.PerRecord)

	t.Logf("worker GLS depth=%d, retained %.3f objects/record, %.1f bytes/record over %d records",
		r.Depth, r.PerRecord, r.BytesPerRecord, r.Records)
}
