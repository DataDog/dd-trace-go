package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
			sampled: false,
			baggage: map[string]string{"A": "A", "B": "B"},
		},
		"nil-trace": &spanContext{
			sampled: false,
		},
		"priority": &spanContext{
			sampled:     true,
			baggage:     map[string]string{"A": "A", "B": "B"},
			hasPriority: true,
			priority:    2,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := newSpanContext(s, parentCtx)
			assert := assert.New(t)
			assert.Equal(ctx.traceID, s.TraceID)
			assert.Equal(ctx.spanID, s.SpanID)
			assert.Equal(ctx.hasPriority, parentCtx.hasPriority)
			assert.Equal(ctx.priority, parentCtx.priority)
			assert.Equal(ctx.sampled, parentCtx.sampled)
			assert.Equal(ctx.baggage, parentCtx.baggage)
		})
	}
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
