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

func TestSpanBufferPushOne(t *testing.T) {
	defer setupteardown()()

	assert := assert.New(t)

	buffer := newSpanBuffer(DefaultTracer)
	assert.NotNil(buffer)
	assert.Len(buffer.trace, 0)

	traceID := random.Uint64()
	root := newSpan("name1", "a-service", "a-resource", traceID, traceID, 0, DefaultTracer)
	root.buffer = buffer

	buffer.Push(root)
	assert.Len(buffer.trace, 1, "there is one span in the buffer")
	assert.Equal(root, buffer.trace[0], "the span is the one pushed before")

	root.Finish()

	select {
	case trace := <-buffer.tracer.traceBuffer:
		assert.Len(trace, 1, "there was a trace in the channel")
		assert.Equal(root, trace[0], "the trace in the channel is the one pushed before")
		assert.Equal(0, len(buffer.trace), "no more spans in the buffer")
	case err := <-buffer.tracer.errorBuffer:
		assert.Fail("unexpected error:", err.Error())
		t.Logf("buffer: %v", buffer)
	}
}

func TestSpanBufferPushNoFinish(t *testing.T) {
	defer setupteardown()()

	assert := assert.New(t)

	buffer := newSpanBuffer(DefaultTracer)
	assert.NotNil(buffer)
	assert.Len(buffer.trace, 0)

	traceID := random.Uint64()
	root := newSpan("name1", "a-service", "a-resource", traceID, traceID, 0, DefaultTracer)
	root.buffer = buffer

	buffer.Push(root)
	assert.Len(buffer.trace, 1, "there is one span in the buffer")
	assert.Equal(root, buffer.trace[0], "the span is the one pushed before")

	select {
	case <-buffer.tracer.traceBuffer:
		assert.Fail("span was not finished, should not be flushed")
		t.Logf("buffer: %v", buffer)
	case err := <-buffer.tracer.errorBuffer:
		assert.Fail("unexpected error:", err.Error())
		t.Logf("buffer: %v", buffer)
	case <-time.After(time.Second / 10):
		t.Logf("expected timeout, nothing should show up in buffer as the trace is not finished")
	}
}

func TestSpanBufferPushSeveral(t *testing.T) {
	defer setupteardown()()

	assert := assert.New(t)

	buffer := newSpanBuffer(DefaultTracer)
	assert.NotNil(buffer)
	assert.Len(buffer.trace, 0)

	traceID := random.Uint64()
	root := newSpan("name1", "a-service", "a-resource", traceID, traceID, 0, DefaultTracer)
	span2 := newSpan("name2", "a-service", "a-resource", random.Uint64(), traceID, root.SpanID, DefaultTracer)
	span3 := newSpan("name3", "a-service", "a-resource", random.Uint64(), traceID, root.SpanID, DefaultTracer)
	span3a := newSpan("name3", "a-service", "a-resource", random.Uint64(), traceID, span3.SpanID, DefaultTracer)

	trace := []*span{root, span2, span3, span3a}

	for i, span := range trace {
		span.buffer = buffer
		buffer.Push(span)
		assert.Len(buffer.trace, i+1, "there is one more span in the buffer")
		assert.Equal(span, buffer.trace[i], "the span is the one pushed before")
	}

	for _, span := range trace {
		span.Finish()
	}

	select {
	case trace := <-buffer.tracer.traceBuffer:
		assert.Len(trace, 4, "there was one trace with the right number of spans in the channel")
		for _, span := range trace {
			assert.Contains(trace, span, "the trace contains the spans")
		}
	case err := <-buffer.tracer.errorBuffer:
		assert.Fail("unexpected error:", err.Error())
	}
}
