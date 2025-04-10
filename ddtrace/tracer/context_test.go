// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	traceinternal "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"

	"github.com/stretchr/testify/assert"
)

func TestSpanFromContext(t *testing.T) {
	t.Run("regular", func(t *testing.T) {
		assert := assert.New(t)
		want := traceinternal.WrapSpan(&v2.Span{})
		ctx := ContextWithSpan(context.Background(), want)
		got, ok := SpanFromContext(ctx)
		assert.True(ok)
		assert.Equal(got, want)
	})
	t.Run("no-op", func(t *testing.T) {
		assert := assert.New(t)
		span, ok := SpanFromContext(context.Background())
		assert.False(ok)
		_, ok = span.(traceinternal.NoopSpan)
		assert.True(ok)
		span, ok = SpanFromContext(context.TODO())
		assert.False(ok)
		_, ok = span.(traceinternal.NoopSpan)
		assert.True(ok)
	})
}

func TestStartSpanFromContext(t *testing.T) {
	_, stop := startTestTracer(t)
	defer stop()

	parent := StartSpan("test")
	parent2 := StartSpan("test", ChildOf(parent.Context()))
	pctx := ContextWithSpan(context.Background(), parent)
	child, ctx := StartSpanFromContext(
		pctx,
		"http.request",
		ServiceName("gin"),
		ResourceName("/"),
		ChildOf(parent2.Context()), // we do this to assert that the span in pctx takes priority.
	)
	assert := assert.New(t)

	sa, ok := child.(traceinternal.SpanV2Adapter)
	assert.True(ok)
	got := sa.Span
	sctx, ok := SpanFromContext(ctx)
	assert.True(ok)
	sactx, ok := sctx.(traceinternal.SpanV2Adapter)
	assert.True(ok)
	gotctx := sactx.Span
	assert.Equal(gotctx, got)

	sm := got.AsMap()
	st := mocktracer.MockSpan(got)
	assert.Equal(parent.Context().TraceID(), sm[ext.MapSpanTraceID])
	assert.Equal(parent.Context().SpanID(), sm[ext.MapSpanParentID])
	assert.Equal("http.request", st.Tag(ext.SpanName))
	assert.Equal("gin", st.Tag(ext.ServiceName))
	assert.Equal("/", st.Tag(ext.ResourceName))
}

func TestStartSpanFromContextDefault(t *testing.T) {
	_, stop := startTestTracer(t)
	defer stop()

	assert := assert.New(t)
	root, ctx := StartSpanFromContext(context.TODO(), "http.request")
	assert.NotNil(root)
	mRoot := mocktracer.MockSpan(root.(traceinternal.SpanV2Adapter).Span)
	assert.Equal("http.request", mRoot.OperationName())
	span, _ := StartSpanFromContext(ctx, "db.query")
	assert.NotNil(span)
	mSpan := mocktracer.MockSpan(span.(traceinternal.SpanV2Adapter).Span)
	assert.Equal("db.query", mSpan.OperationName())
	assert.Equal(mSpan.TraceID(), mRoot.TraceID())
	assert.NotEqual(mSpan.SpanID(), mRoot.SpanID())
}

func TestStartSpanFromContextRace(t *testing.T) {
	_, stop := startTestTracer(t)
	defer stop()

	// Start 100 goroutines that create child spans with StartSpanFromContext in parallel,
	// with a shared options slice. The child spans should get parented to the correct spans
	const numContexts = 100
	options := make([]StartSpanOption, 0, 3)
	outputValues := make(chan uint64, numContexts)
	var expectedTraceIDs []uint64
	for i := 0; i < numContexts; i++ {
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
	var outputs []uint64
	for i := 0; i < numContexts; i++ {
		outputs = append(outputs, <-outputValues)
	}
	assert.Len(t, outputs, numContexts)
	assert.ElementsMatch(t, outputs, expectedTraceIDs)
}

func Test128(t *testing.T) {
	_, stop := startTestTracer(t)
	defer stop()

	t.Run("disable 128 bit trace ids", func(t *testing.T) {
		t.Setenv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", "false")
		span, _ := StartSpanFromContext(context.Background(), "http.request")
		assert.NotZero(t, span.Context().TraceID())
		w3cCtx, ok := span.Context().(ddtrace.SpanContextW3C)
		if !ok {
			assert.Fail(t, "couldn't cast to ddtrace.SpanContextW3C")
		}
		id128 := w3cCtx.TraceID128()
		assert.Len(t, id128, 32) // ensure there are enough leading zeros
		idBytes, err := hex.DecodeString(id128)
		assert.NoError(t, err)
		assert.Equal(t, uint64(0), binary.BigEndian.Uint64(idBytes[:8])) // high 64 bits should be 0
		assert.Equal(t, span.Context().TraceID(), binary.BigEndian.Uint64(idBytes[8:]))
	})

	t.Run("enable 128 bit trace ids", func(t *testing.T) {
		// DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED is true by default
		span128, _ := StartSpanFromContext(context.Background(), "http.request")
		assert.NotZero(t, span128.Context().TraceID())
		w3cCtx, ok := span128.Context().(ddtrace.SpanContextW3C)
		if !ok {
			assert.Fail(t, "couldn't cast to ddtrace.SpanContextW3C")
		}
		id128bit := w3cCtx.TraceID128()
		assert.NotEmpty(t, id128bit)
		assert.Len(t, id128bit, 32)
		// Ensure that the lower order bits match the span's 64-bit trace id
		b, err := hex.DecodeString(id128bit)
		assert.NoError(t, err)
		assert.Equal(t, span128.Context().TraceID(), binary.BigEndian.Uint64(b[8:]))
	})
}

func TestStartSpanFromNilContext(t *testing.T) {
	_, stop := startTestTracer(t)
	defer stop()

	child, ctx := StartSpanFromContext(context.TODO(), "http.request")
	assert := assert.New(t)
	// ensure the returned context works
	assert.Nil(ctx.Value("not_found_key"))

	sa, ok := child.(traceinternal.SpanV2Adapter)
	assert.True(ok)
	internalSpan := mocktracer.MockSpan(sa.Span)
	assert.True(ok)
	assert.Equal("http.request", internalSpan.Tag(ext.SpanName))

	// the returned context includes the span
	ctxSpan, ok := SpanFromContext(ctx)
	assert.True(ok)
	sactx, ok := ctxSpan.(traceinternal.SpanV2Adapter)
	assert.True(ok)
	got := sactx.Span
	assert.Equal(sa.Span, got)
}
