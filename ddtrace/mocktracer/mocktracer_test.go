// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mocktracer

import (
	"testing"
	"time"

	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
)

func TestStart(t *testing.T) {
	trc := Start()
	if tt, ok := internal.GetGlobalTracer().(Tracer); !ok || tt != trc {
		t.Fail()
	}
}

func TestTracerStop(t *testing.T) {
	Start().Stop()
	if _, ok := internal.GetGlobalTracer().(*internal.NoopTracer); !ok {
		t.Fail()
	}
}

func TestTracerStartSpan(t *testing.T) {
	parentTags := map[string]interface{}{ext.ServiceName: "root-service", ext.SamplingPriority: -1}
	startTime := time.Now()

	t.Run("with-service", func(t *testing.T) {
		mt := newMockTracer()
		parent := newSpan(mt, "http.request", &ddtrace.StartSpanConfig{Tags: parentTags})
		s, ok := mt.StartSpan(
			"db.query",
			tracer.ServiceName("my-service"),
			tracer.StartTime(startTime),
			tracer.ChildOf(parent.Context()),
		).(*mockspan)

		assert := assert.New(t)
		assert.True(ok)
		assert.Equal("db.query", s.OperationName())
		assert.Equal(startTime, s.StartTime())
		assert.Equal("my-service", s.Tag(ext.ServiceName))
		assert.Equal(parent.SpanID(), s.ParentID())
		assert.Equal(parent.TraceID(), s.TraceID())
		assert.True(parent.context.hasSamplingPriority())
		assert.Equal(-1, parent.context.samplingPriority())
	})

	t.Run("inherit", func(t *testing.T) {
		mt := newMockTracer()
		parent := newSpan(mt, "http.request", &ddtrace.StartSpanConfig{Tags: parentTags})
		s, ok := mt.StartSpan("db.query", tracer.ChildOf(parent.Context())).(*mockspan)

		assert := assert.New(t)
		assert.True(ok)
		assert.Equal("db.query", s.OperationName())
		assert.Equal("root-service", s.Tag(ext.ServiceName))
		assert.Equal(parent.SpanID(), s.ParentID())
		assert.Equal(parent.TraceID(), s.TraceID())
		assert.True(s.context.hasSamplingPriority())
		assert.Equal(-1, s.context.samplingPriority())
	})
}

func TestTracerFinishedSpans(t *testing.T) {
	mt := newMockTracer()
	assert.Empty(t, mt.FinishedSpans())
	parent := mt.StartSpan("http.request")
	child := mt.StartSpan("db.query", tracer.ChildOf(parent.Context()))
	assert.Empty(t, mt.FinishedSpans())
	child.Finish()
	parent.Finish()
	found := 0
	for _, s := range mt.FinishedSpans() {
		switch s.OperationName() {
		case "http.request":
			assert.Equal(t, parent, s)
			found++
		case "db.query":
			assert.Equal(t, child, s)
			found++
		}
	}
	assert.Equal(t, 2, found)
}

func TestTracerOpenSpans(t *testing.T) {
	mt := newMockTracer()
	assert.Empty(t, mt.OpenSpans())
	parent := mt.StartSpan("http.request")
	child := mt.StartSpan("db.query", tracer.ChildOf(parent.Context()))

	assert.Len(t, mt.OpenSpans(), 2)
	assert.Contains(t, mt.OpenSpans(), parent)
	assert.Contains(t, mt.OpenSpans(), child)

	child.Finish()
	assert.Len(t, mt.OpenSpans(), 1)
	assert.NotContains(t, mt.OpenSpans(), child)

	parent.Finish()
	assert.Empty(t, mt.OpenSpans())
}

func TestTracerReset(t *testing.T) {
	assert := assert.New(t)
	mt := newMockTracer()

	span := mt.StartSpan("parent")
	_ = mt.StartSpan("child", tracer.ChildOf(span.Context()))
	assert.Len(mt.openSpans, 2)

	span.Finish()
	assert.Len(mt.finishedSpans, 1)
	assert.Len(mt.openSpans, 1)

	mt.Reset()

	assert.Empty(mt.finishedSpans)
	assert.Empty(mt.openSpans)
}

func TestTracerInject(t *testing.T) {
	t.Run("errors", func(t *testing.T) {
		mt := newMockTracer()
		assert := assert.New(t)

		err := mt.Inject(&spanContext{}, 2)
		assert.Equal(tracer.ErrInvalidCarrier, err) // 2 is not a carrier

		err = mt.Inject(&spanContext{}, tracer.TextMapCarrier(map[string]string{}))
		assert.Equal(tracer.ErrInvalidSpanContext, err) // no traceID and spanID

		err = mt.Inject(&spanContext{traceID: 2}, tracer.TextMapCarrier(map[string]string{}))
		assert.Equal(tracer.ErrInvalidSpanContext, err) // no spanID

		err = mt.Inject(&spanContext{traceID: 2, spanID: 1}, tracer.TextMapCarrier(map[string]string{}))
		assert.Nil(err) // ok
	})

	t.Run("ok", func(t *testing.T) {
		sctx := &spanContext{
			traceID:     1,
			spanID:      2,
			priority:    -1,
			hasPriority: true,
			baggage:     map[string]string{"A": "B", "C": "D"},
		}
		carrier := make(map[string]string)
		err := (&mocktracer{}).Inject(sctx, tracer.TextMapCarrier(carrier))

		assert := assert.New(t)
		assert.Nil(err)
		assert.Equal("1", carrier[traceHeader])
		assert.Equal("2", carrier[spanHeader])
		assert.Equal("-1", carrier[priorityHeader])
		assert.Equal("B", carrier[baggagePrefix+"A"])
		assert.Equal("D", carrier[baggagePrefix+"C"])
	})
}

func TestTracerExtract(t *testing.T) {
	// carry creates a tracer.TextMapCarrier containing the given sequence
	// of key/value pairs.
	carry := func(kv ...string) tracer.TextMapCarrier {
		var k string
		m := make(map[string]string)
		if n := len(kv); n%2 == 0 && n >= 2 {
			for i, v := range kv {
				if (i+1)%2 == 0 {
					m[k] = v
				} else {
					k = v
				}
			}
		}
		return tracer.TextMapCarrier(m)
	}

	// tests carry helper function.
	t.Run("carry", func(t *testing.T) {
		for _, tt := range []struct {
			in  []string
			out tracer.TextMapCarrier
		}{
			{in: []string{}, out: map[string]string{}},
			{in: []string{"A"}, out: map[string]string{}},
			{in: []string{"A", "B", "C"}, out: map[string]string{}},
			{in: []string{"A", "B"}, out: map[string]string{"A": "B"}},
			{in: []string{"A", "B", "C", "D"}, out: map[string]string{"A": "B", "C": "D"}},
		} {
			assert.Equal(t, tt.out, carry(tt.in...))
		}
	})

	var mt mocktracer

	// tests error return values.
	t.Run("errors", func(t *testing.T) {
		assert := assert.New(t)

		_, err := mt.Extract(2)
		assert.Equal(tracer.ErrInvalidCarrier, err)

		_, err = mt.Extract(carry(traceHeader, "a"))
		assert.Equal(tracer.ErrSpanContextCorrupted, err)

		_, err = mt.Extract(carry(spanHeader, "a", traceHeader, "2", baggagePrefix+"x", "y"))
		assert.Equal(tracer.ErrSpanContextCorrupted, err)

		_, err = mt.Extract(carry(spanHeader, "1"))
		assert.Equal(tracer.ErrSpanContextNotFound, err)

		_, err = mt.Extract(carry())
		assert.Equal(tracer.ErrSpanContextNotFound, err)
	})

	t.Run("ok", func(t *testing.T) {
		assert := assert.New(t)

		ctx, err := mt.Extract(carry(traceHeader, "1", spanHeader, "2"))
		assert.Nil(err)
		sc, ok := ctx.(*spanContext)
		assert.True(ok)
		assert.Equal(uint64(1), sc.traceID)
		assert.Equal(uint64(2), sc.spanID)

		ctx, err = mt.Extract(carry(traceHeader, "1", spanHeader, "2", baggagePrefix+"A", "B", baggagePrefix+"C", "D"))
		assert.Nil(err)
		sc, ok = ctx.(*spanContext)
		assert.True(ok)
		assert.Equal("B", sc.baggageItem("a"))
		assert.Equal("D", sc.baggageItem("c"))

		ctx, err = mt.Extract(carry(traceHeader, "1", spanHeader, "2", priorityHeader, "-1"))
		assert.Nil(err)
		sc, ok = ctx.(*spanContext)
		assert.True(ok)
		assert.True(sc.hasSamplingPriority())
		assert.Equal(-1, sc.samplingPriority())
	})

	t.Run("consistency", func(t *testing.T) {
		assert := assert.New(t)
		want := &spanContext{traceID: 1, spanID: 2, baggage: map[string]string{"a": "B", "C": "D"}}
		mc := tracer.TextMapCarrier(make(map[string]string))
		err := mt.Inject(want, mc)
		assert.Nil(err)
		sc, err := mt.Extract(mc)
		assert.Nil(err)
		got, ok := sc.(*spanContext)
		assert.True(ok)

		assert.Equal(uint64(1), got.traceID)
		assert.Equal(uint64(2), got.spanID)
		assert.Equal("D", got.baggageItem("c"))
		assert.Equal("B", got.baggageItem("a"))
	})
}
