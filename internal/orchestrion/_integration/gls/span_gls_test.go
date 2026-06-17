// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package gls

import (
	"context"
	"runtime"
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/require"
)

// These tests are the regression facility for orchestrion#782. The GLS
// over-pop and cross-goroutine reclaim fix is woven into ddtrace/tracer at
// build time by orchestrion (see ddtrace/tracer/orchestrion.yml: the
// `tracer-internal: true` aspects that add a finished flag, a GLSReclaimable
// method, and an identity-match pop into Span.Finish). The tracer SOURCE has
// no GLS pop/reclaim code, so a plain `go build`/`go test` cannot exercise it
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
// (~15 retained objects each in the original report); with it, depth stays ~1.
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
	// reclaim, this goroutine's stack would be ~%d.
	require.LessOrEqualf(t, depth, 2,
		"GLS leaked: depth=%d after %d cross-goroutine push/finish cycles; "+
			"the reclaim injection in ddtrace/tracer/orchestrion.yml is not applied",
		depth, iterations)
}

// TestSpanGLSNoHeapLeakCrossGoroutine is the end-to-end, heap-level counterpart
// to the GLS-depth assertion above: it reproduces the korECM repro
// (github.com/korECM/dd-trace-go-leak) in-process — an owner goroutine creates
// and finishes each span while a worker goroutine re-injects it via
// ContextWithSpan — and asserts the retained heap objects per record stay flat.
// Before the reclaim fix this leaked ~15 objects/record (millions retained over a
// run); the fix keeps it at ~0. Asserting on retained heap objects, not just GLS
// depth, additionally catches a regression that still pushes but stops reclaiming
// in a way the bounded-depth check might not.
func TestSpanGLSNoHeapLeakCrossGoroutine(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("GLS only exists in orchestrion builds")
	}
	require.True(t, built.WithOrchestrion)

	require.NoError(t, tracer.Start(tracer.WithLogStartup(false)))
	defer tracer.Stop()

	const records = 100_000
	base := context.Background()

	// Owner goroutine creates + finishes each span (pop runs there); the worker
	// (this goroutine) re-injects it via ContextWithSpan, pushing onto this
	// goroutine's GLS stack — a push whose matching pop ran elsewhere, i.e. the
	// orchestrion#782 leak shape. With the reclaim fix the worker's stack (and so
	// the live heap) stays bounded instead of growing one span per record.
	run := func() {
		spanCh := make(chan *tracer.Span, 1024)
		var wg sync.WaitGroup
		wg.Go(func() {
			defer close(spanCh)
			for range records {
				s := tracer.StartSpan("kafka.consume")
				spanCh <- s
				s.Finish()
			}
		})
		for s := range spanCh {
			_ = tracer.ContextWithSpan(base, s)
		}
		wg.Wait()
	}

	run() // warm up so first-run/lazy allocations don't count toward the delta

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	run()

	tracer.Flush() // drop buffered spans so only a GLS leak can retain them
	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	perRecord := float64(int64(after.HeapObjects)-int64(before.HeapObjects)) / records
	// Generous bound well above the GC/alloc noise floor (~0/record with the fix)
	// and far below a regression (~15/record without it).
	require.Lessf(t, perRecord, 1.0,
		"GLS span leak: %.3f retained heap objects/record (was ~15 before the "+
			"reclaim fix); the contextStack.Push reclaim in ddtrace/tracer/orchestrion.yml regressed",
		perRecord)
}

// TestSpanGLSDoubleFinishSameGoroutine verifies the injected pop both restores
// the parent as the active span when a child finishes, and is idempotent: a
// second Finish on the same span must not pop the unrelated parent (the
// over-pop bug). It exercises the Span.Finish identity-match pop injection.
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
