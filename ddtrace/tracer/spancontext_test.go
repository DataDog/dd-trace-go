package tracer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func setupteardown(start, max int) func() {
	oldStartSize := traceStartSize
	oldMaxSize := traceMaxSize
	traceStartSize = start
	traceMaxSize = max
	return func() {
		traceStartSize = oldStartSize
		traceMaxSize = oldMaxSize
	}
}

func TestNewSpanContextPushError(t *testing.T) {
	defer setupteardown(2, 2)()

	tracer, _ := getTestTracer()
	parent := newBasicSpan("test1")                  // 1st span in trace
	parent.context.trace.push(newBasicSpan("test2")) // 2nd span in trace
	child := newSpan("child", "", "", 0, 0, 0, tracer)

	// new context having a parent with a trace of two spans.
	// One more should overflow.
	child.context = newSpanContext(child, parent.context)

	select {
	case err := <-tracer.errorBuffer:
		assert.Equal(t, &errBufferFull{name: "span buffer", size: 2}, err)
	default:
		t.Fatal("no error pushed")
	}
}

func TestSpanTracePushOne(t *testing.T) {
	defer setupteardown(2, 5)()

	assert := assert.New(t)

	tracer := newTracer()
	buffer := newTrace(tracer.pushTrace)
	assert.NotNil(buffer)
	assert.Len(buffer.spans, 0)

	traceID := random.Uint64()
	root := newSpan("name1", "a-service", "a-resource", traceID, traceID, 0, tracer)
	root.context.trace = buffer

	err := buffer.push(root)
	assert.Nil(err)
	assert.Len(buffer.spans, 1, "there is one span in the buffer")
	assert.Equal(root, buffer.spans[0], "the span is the one pushed before")

	root.Finish()

	select {
	case trace := <-tracer.traceBuffer:
		assert.Len(trace, 1, "there was a trace in the channel")
		assert.Equal(root, trace[0], "the trace in the channel is the one pushed before")
		assert.Equal(0, len(buffer.spans), "no more spans in the buffer")
	case err := <-tracer.errorBuffer:
		assert.Fail("unexpected error:", err.Error())
		t.Logf("buffer: %v", buffer)
	}
}

func TestSpanTracePushNoFinish(t *testing.T) {
	defer setupteardown(2, 5)()

	assert := assert.New(t)

	tracer := newTracer()
	buffer := newTrace(tracer.pushTrace)
	assert.NotNil(buffer)
	assert.Len(buffer.spans, 0)

	traceID := random.Uint64()
	root := newSpan("name1", "a-service", "a-resource", traceID, traceID, 0, tracer)
	root.context.trace = buffer

	buffer.push(root)
	assert.Len(buffer.spans, 1, "there is one span in the buffer")
	assert.Equal(root, buffer.spans[0], "the span is the one pushed before")

	select {
	case <-tracer.traceBuffer:
		assert.Fail("span was not finished, should not be flushed")
		t.Logf("buffer: %v", buffer)
	case err := <-tracer.errorBuffer:
		assert.Fail("unexpected error:", err.Error())
		t.Logf("buffer: %v", buffer)
	case <-time.After(time.Second / 10):
		t.Logf("expected timeout, nothing should show up in buffer as the trace is not finished")
	}
}

func TestSpanTracePushSeveral(t *testing.T) {
	defer setupteardown(2, 5)()

	assert := assert.New(t)

	tracer := newTracer()
	buffer := newTrace(tracer.pushTrace)
	assert.NotNil(buffer)
	assert.Len(buffer.spans, 0)

	traceID := random.Uint64()
	root := newSpan("name1", "a-service", "a-resource", traceID, traceID, 0, tracer)
	span2 := newSpan("name2", "a-service", "a-resource", random.Uint64(), traceID, root.SpanID, tracer)
	span3 := newSpan("name3", "a-service", "a-resource", random.Uint64(), traceID, root.SpanID, tracer)
	span3a := newSpan("name3", "a-service", "a-resource", random.Uint64(), traceID, span3.SpanID, tracer)

	trace := []*span{root, span2, span3, span3a}

	for i, span := range trace {
		span.context.trace = buffer
		buffer.push(span)
		assert.Len(buffer.spans, i+1, "there is one more span in the buffer")
		assert.Equal(span, buffer.spans[i], "the span is the one pushed before")
	}

	for _, span := range trace {
		span.Finish()
	}

	select {
	case trace := <-tracer.traceBuffer:
		assert.Len(trace, 4, "there was one trace with the right number of spans in the channel")
		for _, span := range trace {
			assert.Contains(trace, span, "the trace contains the spans")
		}
	case err := <-tracer.errorBuffer:
		assert.Fail("unexpected error:", err.Error())
	}
}

func TestNewSpanContext(t *testing.T) {
	span := &span{
		TraceID:  1,
		SpanID:   2,
		ParentID: 3,
	}
	ctx := newSpanContext(span, nil)

	assert := assert.New(t)
	assert.Equal(ctx.traceID, span.TraceID)
	assert.Equal(ctx.spanID, span.SpanID)
	assert.Equal(ctx.parentID, span.ParentID)
	assert.Contains(ctx.trace.spans, span)
}

func TestSpanContextPropagation(t *testing.T) {
	span := &span{
		TraceID:  1,
		SpanID:   2,
		ParentID: 3,
	}
	parentCtx := &spanContext{
		sampled: false,
		baggage: map[string]string{"A": "A", "B": "B"},
		trace:   newTrace(nil),
	}
	ctx := newSpanContext(span, parentCtx)

	assert := assert.New(t)
	assert.Equal(ctx.traceID, span.TraceID)
	assert.Equal(ctx.spanID, span.SpanID)
	assert.Equal(ctx.parentID, span.ParentID)
	assert.Contains(ctx.trace.spans, span)
	assert.Equal(ctx.sampled, parentCtx.sampled)
	assert.Equal(ctx.baggage, parentCtx.baggage)
}

func TestSpanContextPushFull(t *testing.T) {
	oldMaxSize := traceMaxSize
	defer func() {
		traceMaxSize = oldMaxSize
	}()
	traceMaxSize = 2

	span1 := newBasicSpan("span1")
	span2 := newBasicSpan("span2")
	span3 := newBasicSpan("span3")

	buffer := newTrace(nil)
	assert := assert.New(t)
	assert.NoError(buffer.push(span1))
	assert.NoError(buffer.push(span2))
	err := buffer.push(span3)
	assert.Equal(&errBufferFull{name: "span buffer", size: 2}, err)
}

func TestSpanContextBaggage(t *testing.T) {
	assert := assert.New(t)

	var ctx spanContext
	ctx.setBaggageItem("key", "value")
	assert.Equal("value", ctx.baggage["key"])
}

func TestSpanContextIterator(t *testing.T) {
	assert := assert.New(t)

	got := make(map[string]string)
	ctx := spanContext{baggage: map[string]string{"key": "value"}}
	ctx.ForeachBaggageItem(func(k, v string) bool {
		got[k] = v
		return true
	})

	assert.Len(got, 1)
	assert.Equal("value", got["key"])
}

func TestSpanContextIteratorBreak(t *testing.T) {
	got := make(map[string]string)
	ctx := spanContext{baggage: map[string]string{"key": "value"}}
	ctx.ForeachBaggageItem(func(k, v string) bool {
		return false
	})

	assert.Len(t, got, 0)
}
