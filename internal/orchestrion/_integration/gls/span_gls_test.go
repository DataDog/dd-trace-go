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
// `tracer-internal: true` aspects that add the liveness cell, the popper field,
// and an identity-match pop into Span.Finish). The tracer SOURCE has
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
// goroutine's GLS stack must stay bounded because Span.Finish marks the span's
// liveness cell and contextStack.Push drops finished entries on the next push.
//
// Without the injected fix this goroutine's GLS grows by one entry per record
// (an unbounded leak proportional to the record count); with it, depth stays ~1.
//
// This finishes the span BEFORE re-injecting it, which is a deliberate
// use-after-Finish, so it is not run with the experimental span pool — the pool
// may legitimately have recycled the object by then. Pooled coexistence is covered
// by the live-inject TestGLSNoHeapLeakWithSpanPool in the gls-leak package.
func TestSpanGLSNoLeakCrossGoroutine(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("GLS only exists in orchestrion builds")
	}
	require.True(t, built.WithOrchestrion)

	// WithSpanPool(false) is explicit rather than assumed. This test finishes the
	// span before handing it to the worker, which is a deliberate use-after-Finish
	// that the pool would legitimately recycle. Now that the Orchestrion gate is
	// gone, an inherited DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED=true would turn
	// pooling on here and make this exercise that unsupported path instead of the
	// non-pooled reclaim it is written for.
	require.NoError(t, tracer.Start(tracer.WithLogStartup(false), tracer.WithSpanPool(false)))
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
// Like TestSpanGLSNoLeakCrossGoroutine, this uses the finish-then-inject order and
// so is not run under the experimental span pool; TestGLSNoHeapLeakWithSpanPool
// covers the pooled, live-inject path.
func TestSpanGLSNoHeapLeakCrossGoroutine(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("GLS only exists in orchestrion builds")
	}
	require.True(t, built.WithOrchestrion)

	// WithSpanPool(false) is explicit rather than assumed. This test finishes the
	// span before handing it to the worker, which is a deliberate use-after-Finish
	// that the pool would legitimately recycle. Now that the Orchestrion gate is
	// gone, an inherited DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED=true would turn
	// pooling on here and make this exercise that unsupported path instead of the
	// non-pooled reclaim it is written for.
	require.NoError(t, tracer.Start(tracer.WithLogStartup(false), tracer.WithSpanPool(false)))
	defer tracer.Stop()

	r := glsleak.MeasureLeak(100_000)
	require.Lessf(t, r.PerRecord, glsleak.MaxRetainedObjectsPerRecord,
		"GLS span leak: %.3f retained heap objects/record (want flat ~0; the leak grows "+
			"one span per record) — the contextStack.Push reclaim in ddtrace/tracer/orchestrion.yml regressed",
		r.PerRecord)
}

// TestSpanGLSNoTraceMergeAfterCrossGoroutineFinish covers the consumer half of
// APMS-20132: a Kafka span created and finished on another goroutine is re-injected
// here via ContextWithSpan, so it sits on this goroutine's GLS stack already
// finished. The next inbound request (fresh context, no upstream headers, so the
// GLS is its only parent source) must not adopt it and must start its own trace.
func TestSpanGLSNoTraceMergeAfterCrossGoroutineFinish(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("GLS only exists in orchestrion builds")
	}
	require.True(t, built.WithOrchestrion)

	require.NoError(t, tracer.Start(tracer.WithLogStartup(false)))
	defer tracer.Stop()

	var kafkaSpan *tracer.Span
	var wg sync.WaitGroup
	wg.Go(func() {
		kafkaSpan = tracer.StartSpan("kafka.consume")
		kafkaSpan.Finish()
	})
	wg.Wait()
	_ = tracer.ContextWithSpan(context.Background(), kafkaSpan)

	httpSpan, _ := tracer.StartSpanFromContext(context.Background(), "http.request")
	defer httpSpan.Finish()

	require.NotEqualf(t,
		kafkaSpan.Context().TraceID(),
		httpSpan.Context().TraceID(),
		"http.request joined the finished kafka.consume span's trace (trace_id=%s); "+
			"the GLS Peek guard is not applied",
		kafkaSpan.Context().TraceID(),
	)
}

// TestSpanGLSFinishedParentOnlyHonoredViaExplicitContext pins the escape hatch
// the Peek guard depends on. Skipping finished entries is only safe because the
// GLS is a fallback: a span handed over explicitly through a context.Context is
// still honored, finished or not, since glsContext.Value checks the explicit
// chain first. A refactor that skipped finished spans everywhere rather than only
// in the fallback would silently break deliberate continuation.
//
// Both halves use the same finished span and the same GLS push. The only thing
// that differs is what the caller passes as ctx, so this asserts the distinction
// rather than either behaviour alone. Asserting only the explicit half would be a
// false signal: StartSpanFromContext resolves an explicitly propagated span from
// the snapshot ContextWithSpan leaves under activeSpanContextKey and returns
// before ever consulting the GLS, so that half passes even with no guard and even
// with no orchestrion.
//
// The span is finished on a third goroutine so its popper is a no-op on the
// subtest goroutines, which is what leaves the entry on their stacks already
// finished. Each subtest runs on its own goroutine and so gets its own GLS stack.
func TestSpanGLSFinishedParentOnlyHonoredViaExplicitContext(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("GLS only exists in orchestrion builds")
	}
	require.True(t, built.WithOrchestrion)

	require.NoError(t, tracer.Start(tracer.WithLogStartup(false)))
	defer tracer.Stop()

	var finished *tracer.Span
	var wg sync.WaitGroup
	wg.Go(func() {
		finished = tracer.StartSpan("batch.job")
		finished.Finish()
	})
	wg.Wait()

	t.Run("explicit context inherits", func(t *testing.T) {
		ctx := tracer.ContextWithSpan(context.Background(), finished)

		child, _ := tracer.StartSpanFromContext(ctx, "explicit.child")
		defer child.Finish()

		require.Equal(t,
			finished.Context().TraceID(),
			child.Context().TraceID(),
			"a finished span propagated explicitly must still parent",
		)
	})

	t.Run("GLS fallback does not inherit", func(t *testing.T) {
		// Same span pushed onto this goroutine's stack, but the caller passes a
		// context that carries nothing, so the GLS is the only parent source.
		_ = tracer.ContextWithSpan(context.Background(), finished)

		child, _ := tracer.StartSpanFromContext(context.Background(), "fallback.child")
		defer child.Finish()

		require.NotEqualf(t,
			finished.Context().TraceID(),
			child.Context().TraceID(),
			"the GLS fallback handed out the finished batch.job span as a parent "+
				"(trace_id=%s); the Peek guard is not applied",
			finished.Context().TraceID(),
		)
	})
}

// TestSpanGLSSequentialRequestsStayIndependent is the APMS-20132 reproduction: the
// shape that produced a reported 9,780-span trace spanning 50+ unrelated endpoints.
//
// Stranding an entry does not need a second goroutine. An exit that matches
// position removes the top of the stack rather than the scope that ended, so any
// non-LIFO finish strands something:
//
//	[srv]                  the request's own span
//	[srv, child]           a child span
//	srv.Finish()           pops the top, which is child, leaving [srv(finished)]
//
// The request's span outlives its own finish and stays on the stack. The next
// request on that goroutine has no span in its context, so the GLS is its only
// parent source, and it adopts the finished predecessor and inherits its trace.
// Then the request after that inherits from it, and the whole connection collapses
// into one trace_id.
//
// A child outliving its parent is ordinary: any span whose finish is tied to a
// resource close or an async continuation rather than to a lexical scope. In a
// request that emits ~89 spans it only has to happen once. A cross-goroutine finish
// (covered by TestSpanGLSNoTraceMergeAfterCrossGoroutineFinish) is just one other
// way to reach the same state.
//
// This runs on a single goroutine, which is what net/http gives a keep-alive
// connection when it serves requests sequentially.
func TestSpanGLSSequentialRequestsStayIndependent(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("GLS only exists in orchestrion builds")
	}
	require.True(t, built.WithOrchestrion)

	require.NoError(t, tracer.Start(tracer.WithLogStartup(false)))
	defer tracer.Stop()

	const requests = 20
	seenTraceIDs := make(map[string]struct{}, requests)
	outliving := make([]*tracer.Span, 0, requests)

	for range requests {
		span, ctx := tracer.StartSpanFromContext(context.Background(), "http.request")
		child, _ := tracer.StartSpanFromContext(ctx, "async.work")
		outliving = append(outliving, child)

		span.Finish() // pops child's slot, so this request's own span is left behind
		seenTraceIDs[span.Context().TraceID()] = struct{}{}
	}
	for _, child := range outliving {
		child.Finish()
	}

	require.Lenf(t, seenTraceIDs, requests,
		"expected %d distinct trace_ids, got %d: requests were chained into a shared trace",
		requests, len(seenTraceIDs))
}

// TestSpanGLSLiveSurvivorDoesNotParentNextRequest is the half of APMS-20132 that
// survives the Peek guard, and the reason the exit has to be by scope rather than
// by position.
//
// TestSpanGLSSequentialRequestsStayIndependent above strands the request's own
// span, which is FINISHED — Peek skips it. Stranding a LIVE span takes one more
// level of nesting and nothing else:
//
//	[srv]                the request's own span
//	[srv, child]         a child that outlives the request
//	[srv, child, leaf]   a child of that child
//	srv.Finish()         a top-pop takes leaf, leaving [srv(finished), child(live)]
//
// Peek walks down, skips the finished srv, and stops at child — which is live, so
// there is nothing about it for a read-side guard to reject. The next request on
// this goroutine carries no span in its context, so the GLS is its only parent
// source, and it joins child's trace. The request after that joins that one, and
// the connection collapses into a single trace_id.
//
// Exiting srv's scope removes srv along with everything opened inside it, so the
// next request finds an empty stack. This runs on a single goroutine, which is
// what net/http gives a keep-alive connection serving requests sequentially.
func TestSpanGLSLiveSurvivorDoesNotParentNextRequest(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("GLS only exists in orchestrion builds")
	}
	require.True(t, built.WithOrchestrion)

	require.NoError(t, tracer.Start(tracer.WithLogStartup(false)))
	defer tracer.Stop()

	const requests = 20
	seenTraceIDs := make(map[string]struct{}, requests)
	outliving := make([]*tracer.Span, 0, 2*requests)

	for range requests {
		span, ctx := tracer.StartSpanFromContext(context.Background(), "http.request")
		child, childCtx := tracer.StartSpanFromContext(ctx, "async.work")
		leaf, _ := tracer.StartSpanFromContext(childCtx, "async.work.step")
		outliving = append(outliving, child, leaf)

		// The non-LIFO finish. A position-matched exit would take leaf's slot
		// here and leave child live on the stack after the scope that opened it
		// has ended; the scope exit takes child and leaf along with srv.
		span.Finish()
		seenTraceIDs[span.Context().TraceID()] = struct{}{}
	}
	for _, span := range outliving {
		span.Finish()
	}

	require.Lenf(t, seenTraceIDs, requests,
		"expected %d distinct trace_ids, got %d: a live span outlived its request's scope "+
			"and parented the next request", requests, len(seenTraceIDs))
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
