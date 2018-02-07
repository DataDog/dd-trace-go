package tracer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func setupteardown() func() {
	oldStartSize := traceStartSize
	oldMaxSize := traceMaxSize
	traceStartSize = 2
	traceMaxSize = 5
	return func() {
		traceStartSize = oldStartSize
		traceMaxSize = oldMaxSize
	}
}

func TestSpanTracePushOne(t *testing.T) {
	defer setupteardown()()

	assert := assert.New(t)

	buffer := newTrace(defaultTestTracer.pushTrace)
	assert.NotNil(buffer)
	assert.Len(buffer.trace, 0)

	traceID := random.Uint64()
	root := newSpan("name1", "a-service", "a-resource", traceID, traceID, 0, defaultTestTracer)
	root.context.trace = buffer

	buffer.push(root)
	assert.Len(buffer.trace, 1, "there is one span in the buffer")
	assert.Equal(root, buffer.trace[0], "the span is the one pushed before")

	root.Finish()

	select {
	case trace := <-defaultTestTracer.traceBuffer:
		assert.Len(trace, 1, "there was a trace in the channel")
		assert.Equal(root, trace[0], "the trace in the channel is the one pushed before")
		assert.Equal(0, len(buffer.trace), "no more spans in the buffer")
	case err := <-defaultTestTracer.errorBuffer:
		assert.Fail("unexpected error:", err.Error())
		t.Logf("buffer: %v", buffer)
	}
}

func TestSpanTracePushNoFinish(t *testing.T) {
	defer setupteardown()()

	assert := assert.New(t)

	buffer := newTrace(defaultTestTracer.pushTrace)
	assert.NotNil(buffer)
	assert.Len(buffer.trace, 0)

	traceID := random.Uint64()
	root := newSpan("name1", "a-service", "a-resource", traceID, traceID, 0, defaultTestTracer)
	root.context.trace = buffer

	buffer.push(root)
	assert.Len(buffer.trace, 1, "there is one span in the buffer")
	assert.Equal(root, buffer.trace[0], "the span is the one pushed before")

	select {
	case <-defaultTestTracer.traceBuffer:
		assert.Fail("span was not finished, should not be flushed")
		t.Logf("buffer: %v", buffer)
	case err := <-defaultTestTracer.errorBuffer:
		assert.Fail("unexpected error:", err.Error())
		t.Logf("buffer: %v", buffer)
	case <-time.After(time.Second / 10):
		t.Logf("expected timeout, nothing should show up in buffer as the trace is not finished")
	}
}

func TestSpanTracePushSeveral(t *testing.T) {
	defer setupteardown()()

	assert := assert.New(t)

	buffer := newTrace(defaultTestTracer.pushTrace)
	assert.NotNil(buffer)
	assert.Len(buffer.trace, 0)

	traceID := random.Uint64()
	root := newSpan("name1", "a-service", "a-resource", traceID, traceID, 0, defaultTestTracer)
	span2 := newSpan("name2", "a-service", "a-resource", random.Uint64(), traceID, root.SpanID, defaultTestTracer)
	span3 := newSpan("name3", "a-service", "a-resource", random.Uint64(), traceID, root.SpanID, defaultTestTracer)
	span3a := newSpan("name3", "a-service", "a-resource", random.Uint64(), traceID, span3.SpanID, defaultTestTracer)

	trace := []*span{root, span2, span3, span3a}

	for i, span := range trace {
		span.context.trace = buffer
		buffer.push(span)
		assert.Len(buffer.trace, i+1, "there is one more span in the buffer")
		assert.Equal(span, buffer.trace[i], "the span is the one pushed before")
	}

	for _, span := range trace {
		span.Finish()
	}

	select {
	case trace := <-defaultTestTracer.traceBuffer:
		assert.Len(trace, 4, "there was one trace with the right number of spans in the channel")
		for _, span := range trace {
			assert.Contains(trace, span, "the trace contains the spans")
		}
	case err := <-defaultTestTracer.errorBuffer:
		assert.Fail("unexpected error:", err.Error())
	}
}

func TestSpanContextBaggage(t *testing.T) {
	assert := assert.New(t)

	ctx := &spanContext{}
	ctx.setBaggageItem("key", "value")
	assert.Equal("value", ctx.baggage["key"])
}

func TestSpanContextIterator(t *testing.T) {
	assert := assert.New(t)

	baggageIterator := make(map[string]string)
	ctx := spanContext{baggage: map[string]string{"key": "value"}}
	ctx.ForeachBaggageItem(func(k, v string) bool {
		baggageIterator[k] = v
		return true
	})

	assert.Len(baggageIterator, 1)
	assert.Equal("value", baggageIterator["key"])
}
