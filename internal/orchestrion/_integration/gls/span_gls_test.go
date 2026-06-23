// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package gls

import (
	"context"
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/glsleak"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/require"
)

// These tests are the regression facility for orchestrion#782. The GLS
// over-pop and cross-goroutine reclaim fix is woven into ddtrace/tracer at
// build time by orchestrion (see ddtrace/tracer/orchestrion.yml: the
// `tracer-internal: true` aspects that add a per-activation liveness cell
// (__dd_glsDone) and an identity-match pop into Span.Finish). The tracer SOURCE
// has no GLS pop/reclaim code, so a plain `go build`/`go test` cannot exercise it
// — and, crucially, if the injection ever silently stops applying (a renamed
// Span.Finish, a changed join-point selector, a dropped `tracer-internal`
// flag, an orchestrion schema change), the build still succeeds while the fix
// does nothing.
//
// Running under `orchestrion go test` (as CI does for this package), these
// tests turn that silent no-op into a hard failure: the leak/over-pop returns
// and the assertions below fail.

// TestSpanGLSNoLeakCrossGoroutine reproduces the franz-go / Kafka consumer
// shape that leaks in production: a span is re-injected into a context on one
// goroutine (push) while it is created and finished on another (so the
// goroutine-scoped pop never runs on the pushing goroutine). The pushing
// goroutine's GLS stack must stay bounded because Span.Finish marks the span
// reclaimable and contextStack.Push drops finished entries on the next push.
//
// Without the injected fix this goroutine's GLS grows by one entry per record
// (an unbounded leak proportional to the record count); with it, depth stays ~1.
//
// This test deliberately finishes the span BEFORE re-injecting it, so it is not
// run with the experimental span pool, which legitimately recycles a finished
// span (touching it afterward is out of the pool's contract). The span-pool +
// GLS coexistence is covered by the live-inject TestGLSNoHeapLeakWithSpanPool.
func TestSpanGLSNoLeakCrossGoroutine(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("GLS only exists in orchestrion builds")
	}
	require.True(t, built.WithOrchestrion)

	require.NoError(t, tracer.Start(tracer.WithLogStartup(false)))
	defer tracer.Stop()

	const iterations = 5000
	base := context.Background()
	for range iterations {
		// "owner": create AND finish the span on a different goroutine, so the
		// matching pop never runs on this (the pushing) goroutine.
		var s *tracer.Span
		var wg sync.WaitGroup
		wg.Go(func() {
			s = tracer.StartSpan("kafka.consume")
			s.Finish()
		})
		wg.Wait()

		// "worker" (this goroutine): re-inject the finished span and discard the
		// context, the way a consumer makes its handler a child of the consume
		// span. This pushes onto THIS goroutine's GLS stack.
		_ = tracer.ContextWithSpan(base, s)
	}

	depth := orchestrion.GLSStackDepth()
	// Lower bound: the push must actually happen. Because the tracer source is
	// GLS-agnostic, a missing ContextWithSpan injection means nothing is ever
	// pushed and depth would be 0 — so the leak check alone would pass
	// vacuously. Requiring >= 1 turns a missing push injection into a failure.
	require.GreaterOrEqualf(t, depth, 1,
		"GLS push never happened (depth=0): the ContextWithSpan injection in "+
			"ddtrace/tracer/orchestrion.yml is not applied")
	// Upper bound: it must not grow with the number of records. Without the
	// reclaim, this goroutine's stack would grow to one entry per record.
	require.LessOrEqualf(t, depth, 2,
		"GLS leaked: depth=%d after %d cross-goroutine push/finish cycles; "+
			"the reclaim injection in ddtrace/tracer/orchestrion.yml is not applied",
		depth, iterations)
}

// TestSpanGLSNoHeapLeakCrossGoroutine is the end-to-end, heap-level counterpart
// to the GLS-depth assertion above: it runs the shared korECM repro
// (glsleak.MeasureLeak — an owner goroutine creates and finishes each span while
// a worker goroutine re-injects it via ContextWithSpan) and asserts the retained
// heap objects per record stay flat. Without the reclaim fix the worker's GLS
// stack grows by one span per record (retention rising with the record count);
// the fix keeps it flat. Asserting on retained heap objects, not just GLS depth,
// additionally catches a regression that still pushes but stops reclaiming in a
// way the bounded-depth check might not. The runnable gls-leak command exercises
// the same helper.
//
// Like TestSpanGLSNoLeakCrossGoroutine, this uses the finish-then-inject order
// and is therefore not run under the experimental span pool (which recycles the
// finished span); TestGLSNoHeapLeakWithSpanPool covers the pooled, live-inject path.
func TestSpanGLSNoHeapLeakCrossGoroutine(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("GLS only exists in orchestrion builds")
	}
	require.True(t, built.WithOrchestrion)

	require.NoError(t, tracer.Start(tracer.WithLogStartup(false)))
	defer tracer.Stop()

	r := glsleak.MeasureLeak(100_000)
	require.Lessf(t, r.PerRecord, glsleak.MaxRetainedObjectsPerRecord,
		"GLS span leak: %.3f retained heap objects/record (want flat ~0; the leak grows "+
			"one span per record) — the contextStack.Push reclaim in ddtrace/tracer/orchestrion.yml regressed",
		r.PerRecord)
}

// TestSpanGLSDoubleFinishSameGoroutine verifies the injected pop both restores
// the parent as the active span when a child finishes, and is idempotent: a
// second Finish on the same span must not pop the unrelated parent (the
// over-pop bug). The pop is goroutine-scoped and once-only (GLSDeactivate clears
// the captured popper after running it), not an identity match against a
// specific span — so this guards the LIFO-finish + double-finish cases, not
// arbitrary out-of-order finishes.
func TestSpanGLSDoubleFinishSameGoroutine(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("GLS only exists in orchestrion builds")
	}

	require.NoError(t, tracer.Start(tracer.WithLogStartup(false)))
	defer tracer.Stop()

	outer, octx := tracer.StartSpanFromContext(context.Background(), "outer")
	defer outer.Finish()
	inner, _ := tracer.StartSpanFromContext(octx, "inner")

	// GLS top is inner: a bare context resolves to it via the GLS fallback.
	got, ok := tracer.SpanFromContext(context.Background())
	require.True(t, ok)
	require.Equal(t, inner, got, "inner should be the active span via GLS")

	inner.Finish() // injected pop restores outer as the GLS top
	got, ok = tracer.SpanFromContext(context.Background())
	require.True(t, ok)
	require.Equal(t, outer, got, "inner.Finish must restore outer as the active span")

	inner.Finish() // double finish: identity-match pop must NOT remove outer
	got, ok = tracer.SpanFromContext(context.Background())
	require.True(t, ok)
	require.Equal(t, outer, got, "a second inner.Finish must not over-pop outer")
}
