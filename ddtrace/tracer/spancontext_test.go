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

	tracer, _, stop := startTestTracer()
	defer stop()
	parent := newBasicSpan("test1")                  // 1st span in trace
	parent.context.trace.push(newBasicSpan("test2")) // 2nd span in trace
	child := newSpan("child", "", "", 0, 0, 0)

	// new context having a parent with a trace of two spans.
	// One more should overflow.
	child.context = newSpanContext(child, parent.context)

	select {
	case err := <-tracer.errorBuffer:
		assert.Equal(t, &spanBufferFullError{}, err)
	default:
		t.Fatal("no error pushed")
	}
}

func TestSpanTracePushOne(t *testing.T) {
	defer setupteardown(2, 5)()

	assert := assert.New(t)

	tracer, transport, stop := startTestTracer()
	defer stop()

	traceID := random.Uint64()
	root := newSpan("name1", "a-service", "a-resource", traceID, traceID, 0)
	trace := root.context.trace

	assert.Len(tracer.errorBuffer, 0)
	assert.Len(trace.spans, 1)
	assert.Equal(root, trace.spans[0], "the span is the one pushed before")

	root.Finish()
	tracer.forceFlush()

	select {
	case err := <-tracer.errorBuffer:
		assert.Fail("unexpected error:", err.Error())
		t.Logf("trace: %v", trace)
	default:
		traces := transport.Traces()
		assert.Len(tracer.errorBuffer, 0)
		assert.Len(traces, 1)
		trc := traces[0]
		assert.Len(trc, 1, "there was a trace in the channel")
		comparePayloadSpans(t, root, trc[0])
		assert.Equal(0, len(trace.spans), "no more spans in the trace")
	}
}

func TestSpanTracePushNoFinish(t *testing.T) {
	defer setupteardown(2, 5)()

	assert := assert.New(t)

	tracer, _, stop := startTestTracer()
	defer stop()

	buffer := newTrace()
	assert.NotNil(buffer)
	assert.Len(buffer.spans, 0)

	traceID := random.Uint64()
	root := newSpan("name1", "a-service", "a-resource", traceID, traceID, 0)
	root.context.trace = buffer

	buffer.push(root)
	assert.Len(tracer.errorBuffer, 0)
	assert.Len(buffer.spans, 1, "there is one span in the buffer")
	assert.Equal(root, buffer.spans[0], "the span is the one pushed before")

	select {
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

	tracer, transport, stop := startTestTracer()
	defer stop()
	buffer := newTrace()
	assert.NotNil(buffer)
	assert.Len(buffer.spans, 0)

	traceID := random.Uint64()
	root := newSpan("name1", "a-service", "a-resource", traceID, traceID, 0)
	span2 := newSpan("name2", "a-service", "a-resource", random.Uint64(), traceID, root.SpanID)
	span3 := newSpan("name3", "a-service", "a-resource", random.Uint64(), traceID, root.SpanID)
	span3a := newSpan("name3", "a-service", "a-resource", random.Uint64(), traceID, span3.SpanID)

	trace := []*span{root, span2, span3, span3a}

	for i, span := range trace {
		span.context.trace = buffer
		buffer.push(span)
		assert.Len(tracer.errorBuffer, 0)
		assert.Len(buffer.spans, i+1, "there is one more span in the buffer")
		assert.Equal(span, buffer.spans[i], "the span is the one pushed before")
	}

	for _, span := range trace {
		span.Finish()
	}
	tracer.forceFlush()

	select {
	case err := <-tracer.errorBuffer:
		assert.Fail("unexpected error:", err.Error())
	default:
		traces := transport.Traces()
		assert.Len(traces, 1)
		trace := traces[0]
		assert.Len(trace, 4, "there was one trace with the right number of spans in the channel")
		for _, span := range trace {
			assert.Contains(trace, span, "the trace contains the spans")
		}
	}
}

func TestNewSpanContext(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		span := &span{
			TraceID:  1,
			SpanID:   2,
			ParentID: 3,
		}
		ctx := newSpanContext(span, nil)
		assert := assert.New(t)
		assert.Equal(ctx.traceID, span.TraceID)
		assert.Equal(ctx.spanID, span.SpanID)
		assert.Equal(ctx.priority, 0)
		assert.False(ctx.hasPriority)
		assert.NotNil(ctx.trace)
		assert.Contains(ctx.trace.spans, span)
	})

	t.Run("priority", func(t *testing.T) {
		span := &span{
			TraceID:  1,
			SpanID:   2,
			ParentID: 3,
			Metrics:  map[string]float64{samplingPriorityKey: 1},
		}
		ctx := newSpanContext(span, nil)
		assert := assert.New(t)
		assert.Equal(ctx.traceID, span.TraceID)
		assert.Equal(ctx.spanID, span.SpanID)
		assert.Equal(ctx.TraceID(), span.TraceID)
		assert.Equal(ctx.SpanID(), span.SpanID)
		assert.Equal(ctx.priority, 1)
		assert.True(ctx.hasPriority)
		assert.NotNil(ctx.trace)
		assert.Contains(ctx.trace.spans, span)
	})
}

func TestSpanContextParent(t *testing.T) {
	s := &span{
		TraceID:  1,
		SpanID:   2,
		ParentID: 3,
	}
	for name, parentCtx := range map[string]*spanContext{
		"basic": &spanContext{
			baggage: map[string]string{"A": "A", "B": "B"},
			trace:   newTrace(),
			drop:    true,
		},
		"nil-trace": &spanContext{
			drop: true,
		},
		"priority": &spanContext{
			baggage:     map[string]string{"A": "A", "B": "B"},
			trace:       &trace{spans: []*span{newBasicSpan("abc")}},
			hasPriority: true,
			priority:    2,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := newSpanContext(s, parentCtx)
			assert := assert.New(t)
			assert.Equal(ctx.traceID, s.TraceID)
			assert.Equal(ctx.spanID, s.SpanID)
			if parentCtx.trace != nil {
				assert.Equal(len(ctx.trace.spans), len(parentCtx.trace.spans))
			}
			assert.NotNil(ctx.trace)
			assert.Contains(ctx.trace.spans, s)
			assert.Equal(ctx.hasPriority, parentCtx.hasPriority)
			assert.Equal(ctx.priority, parentCtx.priority)
			assert.Equal(ctx.drop, parentCtx.drop)
			assert.Equal(ctx.baggage, parentCtx.baggage)
		})
	}
}

func TestSpanContextPushFull(t *testing.T) {
	oldMaxSize := traceMaxSize
	defer func() {
		traceMaxSize = oldMaxSize
	}()
	traceMaxSize = 2
	tracer, _, stop := startTestTracer()
	defer stop()

	span1 := newBasicSpan("span1")
	span2 := newBasicSpan("span2")
	span3 := newBasicSpan("span3")

	buffer := newTrace()
	assert := assert.New(t)
	buffer.push(span1)
	assert.Len(tracer.errorBuffer, 0)
	buffer.push(span2)
	assert.Len(tracer.errorBuffer, 0)
	buffer.push(span3)
	assert.Len(tracer.errorBuffer, 1)
	err := <-tracer.errorBuffer
	assert.Equal(&spanBufferFullError{}, err)
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
