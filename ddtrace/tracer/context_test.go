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

	"github.com/DataDog/dd-trace-go/v2/internal"

	"github.com/stretchr/testify/assert"
)

func TestContextWithSpan(t *testing.T) {
	want := &Span{spanID: 123}
	ctx := ContextWithSpan(context.Background(), want)
	got := ctx.Value(internal.ActiveSpanKey)
	assert := assert.New(t)
	assert.Equal(got, want)
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
		//_, ok = span.(*traceinternal.NoopSpan)
		assert.Nil(span)
		//assert.True(ok)
		span, ok = SpanFromContext(nil)
		assert.False(ok)
		//_, ok = span.(*traceinternal.NoopSpan)
		assert.Nil(span)
		//assert.True(ok)
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

	//got, ok := child.(*Span)
	//assert.True(ok)
	got := child
	assert.NotNil(child)
	gotctx, ok := SpanFromContext(ctx)
	assert.True(ok)
	assert.Equal(gotctx, got)
	//_, ok = gotctx.(*traceinternal.NoopSpan)
	//assert.NotNil(gotctx)
	//assert.False(ok)

	assert.Equal(uint64(456), got.traceID)
	assert.Equal(uint64(123), got.parentID)
	assert.Equal("http.request", got.name)
	assert.Equal("gin", got.service)
	assert.Equal("/", got.resource)
}

func TestStartSpanFromContextRace(t *testing.T) {
	_, _, _, stop, err := startTestTracer(t)
	assert.Nil(t, err)
	defer stop()

	// Start 100 goroutines that create child spans with StartSpanFromContext in parallel,
	// with a shared options slice. The child spans should get parented to the correct spans
	const contextKey = "key"
	const numContexts = 100
	options := make([]StartSpanOption, 0, 3)
	outputValues := make(chan string, numContexts)
	var expectedTraceIDs []string
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
	var outputs []string
	for i := 0; i < numContexts; i++ {
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
		t.Setenv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", "false")
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

	child, ctx := StartSpanFromContext(nil, "http.request")
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
