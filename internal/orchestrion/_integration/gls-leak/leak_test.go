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
)

// orchestrionEnabled is flipped to true by orchestrion at build time via the
// //dd:orchestrion-enabled directive, so the gate below only asserts on builds
// where the GLS feature actually exists.
//
//dd:orchestrion-enabled
const orchestrionEnabled = false

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
	if !orchestrionEnabled {
		t.Skip("GLS only exists in orchestrion builds")
	}
	require.True(t, built.WithOrchestrion)

	require.NoError(t, tracer.Start(tracer.WithLogStartup(false)))
	defer tracer.Stop()

	r := glsleak.MeasureLeak(200_000)
	require.Lessf(t, r.PerRecord, glsleak.MaxRetainedObjectsPerRecord,
		"GLS span leak: %.3f retained heap objects/record (want flat ~0; the leak grows "+
			"one span per record) — the contextStack.Push reclaim in ddtrace/tracer/orchestrion.yml regressed",
		r.PerRecord)
}

// TestGLSNoHeapLeakWithSpanPool is the regression gate for running the
// experimental span pool together with orchestrion GLS (the combination #4891
// gated off and this stack re-enables). It uses the live-inject workload, which
// respects the span pool's "do not use a span after Finish" contract, and
// enables pooling via WithSpanPool(true). With the decoupled, cell-based reclaim
// the worker's GLS stack stays flat even though every finished span is recycled
// by the pool; without it, recycled spans would either leak (stale entries never
// drained) or resurface as the wrong active span. Run under -race in CI, it also
// guards against the span-pool-vs-GLS data races from the review.
func TestGLSNoHeapLeakWithSpanPool(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("GLS only exists in orchestrion builds")
	}
	require.True(t, built.WithOrchestrion)

	require.NoError(t, tracer.Start(tracer.WithLogStartup(false), tracer.WithSpanPool(true)))
	defer tracer.Stop()

	r := glsleak.MeasureLeakLiveInject(200_000)
	require.Lessf(t, r.PerRecord, glsleak.MaxRetainedObjectsPerRecord,
		"GLS span leak with span pool: %.3f retained heap objects/record (want flat ~0) — "+
			"the decoupled reclaim regressed, or span pool + orchestrion GLS no longer coexist safely",
		r.PerRecord)
}
