// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextWithSpan(t *testing.T) {
	t.Run("OK", func(t *testing.T) {
		want := &Span{spanID: 123}
		ctx := ContextWithSpan(context.Background(), want)
		got := ctx.Value(internal.ActiveSpanKey)
		assert := assert.New(t)
		assert.Equal(got, want)
	})

	t.Run("nil context", func(t *testing.T) {
		assert.NotPanics(t, func() {
			want := &Span{spanID: 123}
			ctx := ContextWithSpan(nil, want)
			got := ctx.Value(internal.ActiveSpanKey)
			assert := assert.New(t)
			assert.Equal(got, want)
		})
	})
}

func TestSpanFromContext(t *testing.T) {
	t.Run("regular", func(t *testing.T) {
		assert := assert.New(t)
		want := &Span{spanID: 123}
		ctx := ContextWithSpan(context.Background(), want)
		got, ok := SpanFromContext(ctx)
		assert.True(ok)
		assert.Equal(got, want)
	})
	t.Run("no-op", func(t *testing.T) {
		assert := assert.New(t)
		span, ok := SpanFromContext(context.Background())
		assert.False(ok)
		assert.Nil(span)
		span, ok = SpanFromContext(context.TODO())
		assert.False(ok)
		assert.Nil(span)
	})
}

func TestStartSpanFromContext(t *testing.T) {
	_, _, _, stop, err := startTestTracer(t)
	assert.Nil(t, err)

	defer stop()

	parent := &Span{context: &SpanContext{spanID: 123, traceID: traceIDFrom64Bits(456)}}
	parent2 := &Span{context: &SpanContext{spanID: 789, traceID: traceIDFrom64Bits(456)}}
	pctx := ContextWithSpan(context.Background(), parent)
	child, ctx := StartSpanFromContext(
		pctx,
		"http.request",
		ServiceName("gin"),
		ResourceName("/"),
		ChildOf(parent2.Context()), // we do this to assert that the span in pctx takes priority.
	)
	assert := assert.New(t)

	got := child
	assert.NotNil(child)
	gotctx, ok := SpanFromContext(ctx)
	assert.True(ok)
	assert.Equal(gotctx, got)
	assert.Equal(uint64(456), got.traceID)
	assert.Equal(uint64(123), got.parentID)
	assert.Equal("http.request", got.name)
	assert.Equal("gin", got.service)
	assert.Equal("/", got.resource)
}

func TestStartSpanFromContextDefault(t *testing.T) {
	_, _, _, stop, err := startTestTracer(t)
	assert.NoError(t, err)
	defer stop()

	assert := assert.New(t)
	root, ctx := StartSpanFromContext(context.TODO(), "http.request")
	assert.NotNil(root)
	assert.Equal("http.request", root.name)
	span, _ := StartSpanFromContext(ctx, "db.query")
	assert.NotNil(span)
	assert.Equal("db.query", span.name)
	assert.Equal(span.traceID, root.traceID)
	assert.NotEqual(span.spanID, root.spanID)
}

func TestStartSpanWithSpanLinks(t *testing.T) {
	_, _, _, stop, err := startTestTracer(t)
	assert.NoError(t, err)
	defer stop()
	spanLink := SpanLink{TraceID: 789, TraceIDHigh: 0, SpanID: 789, Attributes: map[string]string{"reason": "terminated_context", "context_headers": "datadog"}, Flags: 0}
	ctx := &SpanContext{spanLinks: []SpanLink{spanLink}, spanID: 789, traceID: traceIDFrom64Bits(789)}

	t.Run("create span from spancontext with links", func(t *testing.T) {
		var s *Span
		s, _ = StartSpanFromContext(
			context.Background(),
			"http.request",
			WithSpanLinks([]SpanLink{spanLink}),
			ChildOf(ctx),
		)

		assert.Equal(t, 1, len(s.spanLinks))
		assert.Equal(t, spanLink, s.spanLinks[0])

		assert.Equal(t, 0, len(s.context.spanLinks)) // ensure that the span links are not added to the parent context
	})
}

func TestStartSpanFromContextRace(t *testing.T) {
	_, _, _, stop, err := startTestTracer(t)
	assert.Nil(t, err)
	defer stop()

	// Start 100 goroutines that create child spans with StartSpanFromContext in parallel,
	// with a shared options slice. The child spans should get parented to the correct spans
	const numContexts = 100
	options := make([]StartSpanOption, 0, 3)
	outputValues := make(chan string, numContexts)
	var expectedTraceIDs []string
	for range numContexts {
		parent, childCtx := StartSpanFromContext(context.Background(), "parent")
		expectedTraceIDs = append(expectedTraceIDs, parent.Context().TraceID())
		go func() {
			span, _ := StartSpanFromContext(childCtx, "testoperation", options...)
			defer span.Finish()
			outputValues <- span.Context().TraceID()
		}()
		parent.Finish()
	}

	// collect the outputs
	var outputs []string
	for range numContexts {
		outputs = append(outputs, <-outputValues)
	}
	assert.Len(t, outputs, numContexts)
	assert.ElementsMatch(t, outputs, expectedTraceIDs)
}

func Test128(t *testing.T) {
	_, _, _, stop, err := startTestTracer(t)
	assert.Nil(t, err)
	defer stop()

	t.Run("disable 128 bit trace ids", func(t *testing.T) {
		old := traceID128BitEnabled.Swap(false)
		defer func(v bool) { traceID128BitEnabled.Store(v) }(old)
		span, _ := StartSpanFromContext(context.Background(), "http.request")
		assert.NotZero(t, span.Context().TraceID())
		w3cCtx := span.Context()
		id128 := w3cCtx.TraceID()
		assert.Len(t, id128, 32) // ensure there are enough leading zeros
		idBytes, err := hex.DecodeString(id128)
		assert.NoError(t, err)
		assert.Equal(t, uint64(0), binary.BigEndian.Uint64(idBytes[:8])) // high 64 bits should be 0
		tid := span.Context().TraceIDBytes()
		assert.Equal(t, tid[:], idBytes)
	})

	t.Run("enable 128 bit trace ids", func(t *testing.T) {
		// DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED is true by default
		span128, _ := StartSpanFromContext(context.Background(), "http.request")
		assert.NotZero(t, span128.Context().TraceID())
		w3cCtx := span128.Context()
		id128bit := w3cCtx.TraceID()
		assert.NotEmpty(t, id128bit)
		assert.Len(t, id128bit, 32)
		// Ensure that the lower order bits match the span's 64-bit trace id
		b, err := hex.DecodeString(id128bit)
		assert.NoError(t, err)
		assert.Equal(t, span128.Context().TraceIDLower(), binary.BigEndian.Uint64(b[8:]))
	})
}

func TestStartSpanFromNilContext(t *testing.T) {
	_, _, _, stop, err := startTestTracer(t)
	assert.Nil(t, err)
	defer stop()

	child, ctx := StartSpanFromContext(context.TODO(), "http.request")
	assert := assert.New(t)
	// ensure the returned context works
	assert.Nil(ctx.Value("not_found_key"))

	internalSpan := child
	assert.Equal("http.request", internalSpan.name)

	// the returned context includes the span
	ctxSpan, ok := SpanFromContext(ctx)
	assert.True(ok)
	assert.Equal(child, ctxSpan)
}

// TestFinishIsIdempotentOnGLS is a regression test for
// https://github.com/DataDog/orchestrion/issues/782 (korECM's report).
//
// Before the fix, Span.Finish unconditionally popped ActiveSpanKey from the
// GLS context stack even when s.finish(t) short-circuited because s.finished
// was already true. Calling Finish twice on the same span popped the stack
// twice — and the second pop removed an unrelated parent span sitting on
// top, causing cross-request trace parenting bugs in production.
//
// This test pushes outer + inner onto the GLS stack via StartSpanFromContext
// (which calls ContextWithSpan), finishes inner twice, and verifies that
// outer is still reachable from a bare context (i.e., still on the GLS
// stack). On main this fails: the second inner.Finish() pops outer.
func TestFinishIsIdempotentOnGLS(t *testing.T) {
	t.Cleanup(orchestrion.MockGLS())

	_, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	outer, outerCtx := StartSpanFromContext(context.Background(), "outer")
	inner, _ := StartSpanFromContext(outerCtx, "inner")

	// Sanity: both pushes occurred. ActiveSpanKey gets one entry per span.
	require.Equal(t, 2, orchestrion.GLSStackDepth(), "expected outer+inner on GLS stack")

	inner.Finish() // expected: pop inner, stack = [outer]
	inner.Finish() // expected: no-op (idempotent). Before fix: pops outer.

	// outer must still be the active span via GLS lookup against a bare ctx.
	top, ok := SpanFromContext(orchestrion.WrapContext(context.Background()))
	assert.True(t, ok, "outer should still be on the GLS stack")
	assert.Equal(t, outer, top, "double inner.Finish() must not pop outer")

	// And the stack must have exactly one entry (outer).
	assert.Equal(t, 1, orchestrion.GLSStackDepth(), "stack should have only outer")

	outer.Finish() // clean up

	assert.Equal(t, 0, orchestrion.GLSStackDepth(), "stack should be empty after outer.Finish")
}

// TestFinishOnDifferentGoroutineDoesNotPopOthersStack is a regression test
// for the cross-goroutine pop bug also surfaced in
// https://github.com/DataDog/orchestrion/issues/782. Before the fix,
// Span.Finish used the raw orchestrion.GLSPopValue, which pops whichever
// goroutine's GLS stack the finisher happens to be running on. If goroutine
// A starts a span and hands it to goroutine B for Finish, B's own GLS stack
// gets corrupted (an entry belonging to a different in-flight span gets
// popped instead).
//
// With the fix in place, the popFunc captured at push time is scoped to the
// pushing goroutine, so Finish on a different goroutine is a no-op for the
// finishing goroutine's stack.
func TestFinishOnDifferentGoroutineDoesNotPopOthersStack(t *testing.T) {
	t.Cleanup(orchestrion.MockGLSPerGoroutine())

	_, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	// Goroutine A: starts spanA, pushing it onto A's GLS.
	spanA, _ := StartSpanFromContext(context.Background(), "spanA")
	require.Equal(t, 1, orchestrion.GLSStackDepth(), "spanA should be on A's stack")

	// Hand spanA to goroutine B and have B start its own spanB and then
	// Finish spanA from B. The finish on B must not pop spanB off B's stack.
	var wg sync.WaitGroup
	var depthInB int
	var topOnB *Span
	var topOnBOk bool
	wg.Go(func() {
		spanB, _ := StartSpanFromContext(context.Background(), "spanB")
		// Now B's GLS has [spanB]. Finishing spanA on this goroutine must
		// be a no-op for B's stack.
		spanA.Finish()

		// B's stack should still contain spanB.
		depthInB = orchestrion.GLSStackDepth()
		topOnB, topOnBOk = SpanFromContext(orchestrion.WrapContext(context.Background()))
		spanB.Finish()
	})
	wg.Wait()

	assert.Equal(t, 1, depthInB, "spanA.Finish on goroutine B must not pop B's stack")
	assert.True(t, topOnBOk, "spanB should still be on B's stack after spanA.Finish")
	if assert.NotNil(t, topOnB) {
		assert.Equal(t, "spanB", topOnB.name)
	}
}

// TestFinishWithoutContextDoesNotPopGLS verifies that a span created via
// StartSpan (without an associated context push) does not pop the GLS stack
// at Finish. Before the fix, Span.Finish always popped, even when no push
// had occurred, which could corrupt unrelated GLS state.
func TestFinishWithoutContextDoesNotPopGLS(t *testing.T) {
	t.Cleanup(orchestrion.MockGLS())

	_, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	// Put an unrelated span on the GLS stack via ContextWithSpan.
	other, _ := StartSpanFromContext(context.Background(), "other")
	require.Equal(t, 1, orchestrion.GLSStackDepth())

	// Create a span the "manual" way — no ContextWithSpan, so no GLS push.
	manual := StartSpan("manual")
	manual.Finish()

	// other must still be on the GLS stack.
	assert.Equal(t, 1, orchestrion.GLSStackDepth(), "StartSpan().Finish() must not pop unrelated GLS entries")
	top, ok := SpanFromContext(orchestrion.WrapContext(context.Background()))
	assert.True(t, ok)
	assert.Equal(t, other, top)

	other.Finish()
	assert.Equal(t, 0, orchestrion.GLSStackDepth())
}

// TestContextWithSpanReclaimsFinishedCrossGoroutine is the in-tree analogue of
// korECM's standalone reproducer (https://github.com/korECM/dd-trace-go-leak)
// for orchestrion issue #782. It models the franz-go / Kafka consumer shape:
// an owner goroutine creates and finishes each span, while a worker goroutine
// re-injects the (already finished) span into a context via ContextWithSpan.
//
// Because the push happens on the worker and the matching pop (Finish) ran on
// the owner, the goroutine-scoped popper from the over-pop fix is a no-op on
// the worker — so without the reclaim-on-push behavior the worker's GLS stack
// would grow by one entry per record (the leak korECM measured at ~16
// objects/record). With reclaim-on-push, ContextWithSpan drops the previous
// finished span from the top before pushing the next, keeping depth bounded.
func TestContextWithSpanReclaimsFinishedCrossGoroutine(t *testing.T) {
	t.Cleanup(orchestrion.MockGLSPerGoroutine())

	_, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	const iterations = 1000

	depthCh := make(chan int, 1)
	go func() { // worker goroutine — all ContextWithSpan pushes happen here
		for range iterations {
			// Owner-equivalent: create AND finish the span on a different
			// goroutine, so the worker never runs the matching pop.
			var s *Span
			var wg sync.WaitGroup
			wg.Go(func() {
				s = StartSpan("kafka.consume")
				s.Finish()
			})
			wg.Wait()

			// Worker re-injects the already-finished span and discards the ctx,
			// exactly like a consumer making its handler a child of the consume
			// span. The push must reclaim the previous finished entry.
			_ = ContextWithSpan(context.Background(), s)
		}
		depthCh <- orchestrion.GLSStackDepth()
	}()

	depth := <-depthCh
	// Without the fix this would be ~iterations. With it, only the most
	// recently pushed (now-finished) span remains, reclaimed on the next push.
	assert.LessOrEqual(t, depth, 1,
		"worker GLS depth must stay bounded under push-here/finish-there, got %d after %d records", depth, iterations)
}
