// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal"

	"github.com/stretchr/testify/assert"
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
	expectedTraceIDs := make([]string, 0, numContexts)
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
	outputs := make([]string, 0, numContexts)
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

// TestStartSpanFromContextDetachRegression guards against a regression introduced between
// v2.9.1-rc.3 and v2.10.0-rc.4: ContextWithSpan(ctx, nil) is the way to detach from an
// ambient span while preserving ctx's cancellation/deadline. For a while it only cleared
// internal.ActiveSpanKey and left behind any activeSpanContextKey{} snapshot that a prior
// StartSpanFromContext call had already written into an ancestor of ctx. StartSpanFromContext
// consults that snapshot before falling back to SpanFromContext, so a "detached" context kept
// silently chaining onto the old parent's trace.
func TestStartSpanFromContextDetachRegression(t *testing.T) {
	_, _, _, stop, err := startTestTracer(t)
	assert.NoError(t, err)
	defer stop()

	// Start a "parent" span the way a long-lived handler would, and get back a
	// context carrying it.
	parent, parentCtx := StartSpanFromContext(context.Background(), "parent")
	defer parent.Finish()

	// Sanity check: without detaching, a child does inherit the parent's trace.
	child, _ := StartSpanFromContext(parentCtx, "child")
	defer child.Finish()
	assert.Equal(t, parent.traceID, child.traceID)
	assert.Equal(t, parent.spanID, child.parentID)

	// Detach: pass a nil span to keep ctx's cancellation/deadline but drop the
	// ambient span.
	detachedCtx := ContextWithSpan(parentCtx, nil)

	// SpanFromContext correctly reports no active span on the detached context.
	_, ok := SpanFromContext(detachedCtx)
	assert.False(t, ok, "detached context must not report an active span")

	// StartSpanFromContext must treat detachedCtx as having no parent and start a
	// fresh root span (new trace, no parentID), not inherit the stale
	// activeSpanContextKey{} snapshot left behind by the parent's own
	// StartSpanFromContext call.
	grandchild, _ := StartSpanFromContext(detachedCtx, "grandchild-should-be-root")
	defer grandchild.Finish()

	assert.NotEqual(t, parent.traceID, grandchild.traceID,
		"span started from a detached context must not inherit the old trace ID")
	assert.Zero(t, grandchild.parentID,
		"span started from a detached context must not have a parentID")
}

// TestStartSpanFromContextDetachWithExplicitParent guards the qualifier on
// ContextWithSpan's root-span promise: detaching only suppresses the ambient
// parent. A caller that also passes an explicit parent (e.g. ChildOf) to the
// subsequent StartSpanFromContext call still gets that parent, not a root span.
func TestStartSpanFromContextDetachWithExplicitParent(t *testing.T) {
	_, _, _, stop, err := startTestTracer(t)
	assert.NoError(t, err)
	defer stop()

	parent, parentCtx := StartSpanFromContext(context.Background(), "parent")
	defer parent.Finish()
	detachedCtx := ContextWithSpan(parentCtx, nil)

	explicitParent, _ := StartSpanFromContext(context.Background(), "explicit-parent")
	defer explicitParent.Finish()

	child, _ := StartSpanFromContext(detachedCtx, "child", ChildOf(explicitParent.Context()))
	defer child.Finish()

	assert.Equal(t, explicitParent.traceID, child.traceID,
		"an explicit ChildOf passed alongside a detached context must still be honored")
	assert.Equal(t, explicitParent.spanID, child.parentID,
		"an explicit ChildOf passed alongside a detached context must still be honored")
	assert.NotEqual(t, parent.traceID, child.traceID,
		"the detached ambient parent must not leak through despite the explicit ChildOf")
}

// TestContextWithSpanDoubleDetachIdempotent guards the shadow branch added
// alongside TestStartSpanFromContextDetachRegression: shadowing an
// already-nil snapshot must be a no-op rather than compounding into a second
// write, so detaching an already-detached context behaves exactly like
// detaching it once.
func TestContextWithSpanDoubleDetachIdempotent(t *testing.T) {
	_, _, _, stop, err := startTestTracer(t)
	assert.NoError(t, err)
	defer stop()

	parent, parentCtx := StartSpanFromContext(context.Background(), "parent")
	defer parent.Finish()

	onceDetached := ContextWithSpan(parentCtx, nil)
	twiceDetached := ContextWithSpan(onceDetached, nil)

	_, ok := SpanFromContext(twiceDetached)
	assert.False(t, ok, "double-detached context must not report an active span")

	grandchild, _ := StartSpanFromContext(twiceDetached, "grandchild-should-be-root")
	defer grandchild.Finish()

	assert.NotEqual(t, parent.traceID, grandchild.traceID,
		"span started from a double-detached context must not inherit the old trace ID")
	assert.Zero(t, grandchild.parentID,
		"span started from a double-detached context must not have a parentID")
}

// customCtxWithSecret is a context.Context that does not implement
// fmt.Stringer, used to check that spanCtx.String() falls back to a type
// name for such a parent rather than a reflective field dump. Embedding the
// context.Context interface only promotes the interface's own methods
// (Deadline, Done, Err, Value), not String, so this type is deliberately not
// a fmt.Stringer.
type customCtxWithSecret struct {
	context.Context
	secret string
}

// TestSpanCtxString guards the fix for a regression Codex review caught:
// fmt.Sprintf("%v", ...) on a non-Stringer parent falls back to a reflective
// dump of its fields, unlike the two chained context.WithValue nodes this
// type replaces, whose String method renders a non-Stringer parent by type
// name only (via the context package's internal contextName helper).
func TestSpanCtxString(t *testing.T) {
	t.Run("Stringer parent", func(t *testing.T) {
		ctx := ContextWithSpan(context.Background(), &Span{spanID: 123})
		s := fmt.Sprint(ctx)
		assert.Contains(t, s, "context.Background")
	})

	t.Run("non-Stringer parent falls back to type name, not a field dump", func(t *testing.T) {
		parent := customCtxWithSecret{Context: context.Background(), secret: "super-secret-value"}
		ctx := ContextWithSpan(parent, &Span{spanID: 123})
		s := fmt.Sprint(ctx)
		assert.Contains(t, s, "customCtxWithSecret")
		assert.NotContains(t, s, "super-secret-value")
	})
}

// TestSpanCtxValue exercises spanCtx.Value directly (via the *spanCtx that
// ContextWithSpan returns), pinning the four behaviors that make it the
// load-bearing logic behind ContextWithSpan's detach guarantee: a live span
// answers both keys, a nil span shadows both keys with a typed nil rather
// than an absent key, and an unrelated key still falls through to the parent
// context.
func TestSpanCtxValue(t *testing.T) {
	_, _, _, stop, err := startTestTracer(t)
	assert.NoError(t, err)
	defer stop()

	t.Run("live span", func(t *testing.T) {
		span := StartSpan("op")
		defer span.Finish()

		ctx := ContextWithSpan(context.Background(), span)

		gotSpan, ok := ctx.Value(internal.ActiveSpanKey).(*Span)
		assert.True(t, ok)
		assert.Equal(t, span, gotSpan)

		gotSnapshot, ok := ctx.Value(activeSpanContextKey{}).(*SpanContext)
		assert.True(t, ok)
		assert.NotNil(t, gotSnapshot)
	})

	t.Run("detach", func(t *testing.T) {
		parent := StartSpan("parent")
		defer parent.Finish()
		parentCtx := ContextWithSpan(context.Background(), parent)

		detachedCtx := ContextWithSpan(parentCtx, nil)

		// internal.ActiveSpanKey must resolve to a non-nil interface holding a
		// typed nil *Span, not an absent key — otherwise Value would fall
		// through to parentCtx and resurrect the span it was meant to hide.
		v := detachedCtx.Value(internal.ActiveSpanKey)
		assert.True(t, v != nil, "interface value must be non-nil (a typed nil *Span)")
		gotSpan, ok := v.(*Span)
		assert.True(t, ok)
		assert.Nil(t, gotSpan)

		// Same two-part check for the snapshotted SpanContext.
		sv := detachedCtx.Value(activeSpanContextKey{})
		assert.True(t, sv != nil, "interface value must be non-nil (a typed nil *SpanContext)")
		gotSnapshot, ok := sv.(*SpanContext)
		assert.True(t, ok)
		assert.Nil(t, gotSnapshot)
	})

	t.Run("unrelated key passthrough", func(t *testing.T) {
		type unrelatedKey struct{}
		base := context.WithValue(context.Background(), unrelatedKey{}, "unrelated-value")

		span := StartSpan("op")
		defer span.Finish()
		ctx := ContextWithSpan(base, span)

		assert.Equal(t, "unrelated-value", ctx.Value(unrelatedKey{}))
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
