// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mocktracer

import (
	"fmt"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
)

func TestStart(t *testing.T) {
	trc := Start()
	if tt, ok := getGlobalTracer().(Tracer); !ok || tt != trc {
		t.Fail()
	}
	// If the tracer isn't stopped it leaks goroutines, and breaks other tests.
	trc.Stop()
}

func TestTracerStop(t *testing.T) {
	Start().Stop()
	tr := getGlobalTracer()
	if _, ok := tr.(*tracer.NoopTracer); !ok {
		t.Errorf("tracer is not a NoopTracer: %T", tr)
	}
}

func TestTracerStartSpan(t *testing.T) {
	parentTags := map[string]interface{}{ext.ServiceName: "root-service", ext.ManualDrop: true}
	// Need to round the monotonic clock so parsed UnixNano values are equal.
	// See time.Time documentation for details:
	// https://pkg.go.dev/time#Time
	startTime := time.Now().Round(0)

	t.Run("with-service", func(t *testing.T) {
		mt := newMockTracer()
		defer mt.Stop()
		parent := MockSpan(newSpan("http.request", &tracer.StartSpanConfig{Tags: parentTags}))
		s := MockSpan(mt.StartSpan(
			"db.query",
			tracer.ServiceName("my-service"),
			tracer.StartTime(startTime),
			tracer.ChildOf(parent.Context()),
		))

		assert := assert.New(t)
		assert.NotNil(s)
		assert.Equal("db.query", s.OperationName())
		assert.Equal(startTime, s.StartTime())
		assert.Equal("my-service", s.Tag(ext.ServiceName))
		assert.Equal(parent.SpanID(), s.ParentID())
		assert.Equal(parent.TraceID(), s.TraceID())
		sp, ok := parent.Context().SamplingPriority()
		assert.True(ok)
		assert.Equal(-1, sp)
	})

	t.Run("inherit", func(t *testing.T) {
		mt := newMockTracer()
		defer mt.Stop()
		parent := MockSpan(newSpan("http.request", &tracer.StartSpanConfig{Tags: parentTags}))
		s := MockSpan(mt.StartSpan("db.query", tracer.ChildOf(parent.Context())))

		assert := assert.New(t)
		assert.NotNil(s)
		assert.Equal("db.query", s.OperationName())
		assert.Equal("root-service", s.Tag(ext.ServiceName))
		assert.Equal(parent.SpanID(), s.ParentID())
		assert.Equal(parent.TraceID(), s.TraceID())
		sp, ok := parent.Context().SamplingPriority()
		assert.True(ok)
		assert.Equal(-1, sp)
	})
}

func TestTracerFinishedSpans(t *testing.T) {
	mt := Start()
	t.Cleanup(func() {
		mt.Stop()
	})

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
			assert.Equal(t, parent, s.Unwrap())
			found++
		case "db.query":
			assert.Equal(t, child, s.Unwrap())
			found++
		}
	}
	assert.Equal(t, 2, found)
}

func TestTracerOpenSpans(t *testing.T) {
	mt := Start()
	t.Cleanup(func() {
		mt.Stop()
	})

	assert.Empty(t, mt.OpenSpans())
	parent := mt.StartSpan("http.request")
	child := mt.StartSpan("db.query", tracer.ChildOf(parent.Context()))

	assert.Len(t, mt.OpenSpans(), 2)
	assert.Contains(t, UnwrapSlice(mt.OpenSpans()), parent)
	assert.Contains(t, UnwrapSlice(mt.OpenSpans()), child)

	child.Finish()
	assert.Len(t, mt.OpenSpans(), 1)
	assert.NotContains(t, mt.OpenSpans(), child)

	parent.Finish()
	assert.Empty(t, mt.OpenSpans())
}

func TestTracerSetUser(t *testing.T) {
	mt := Start()
	defer mt.Stop() // TODO (hannahkm): confirm this is correct
	span := mt.StartSpan("http.request")
	tracer.SetUser(span, "test-user",
		tracer.WithUserEmail("email"),
		tracer.WithUserName("name"),
		tracer.WithUserRole("role"),
		tracer.WithUserScope("scope"),
		tracer.WithUserSessionID("session"),
		tracer.WithUserMetadata("key", "value"),
	)

	span.Finish()

	finishedSpan := mt.FinishedSpans()[0]
	assert.Equal(t, "test-user", finishedSpan.Tag("usr.id"))
	assert.Equal(t, "email", finishedSpan.Tag("usr.email"))
	assert.Equal(t, "name", finishedSpan.Tag("usr.name"))
	assert.Equal(t, "role", finishedSpan.Tag("usr.role"))
	assert.Equal(t, "scope", finishedSpan.Tag("usr.scope"))
	assert.Equal(t, "session", finishedSpan.Tag("usr.session_id"))
	assert.Equal(t, "value", finishedSpan.Tag("usr.key"))
}

func TestTracerReset(t *testing.T) {
	assert := assert.New(t)
	mt := Start().(*mocktracer)
	t.Cleanup(func() {
		mt.Stop()
	})

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
		defer mt.Stop()

		assert := assert.New(t)

		err := mt.Inject(&tracer.SpanContext{}, 2)
		assert.Equal(tracer.ErrInvalidCarrier, err) // 2 is not a carrier

		err = mt.Inject(&tracer.SpanContext{}, tracer.TextMapCarrier(map[string]string{}))
		assert.Equal(tracer.ErrInvalidSpanContext, err) // no traceID and spanID

		sp := mt.StartSpan("op")

		err = mt.Inject(sp.Context(), tracer.TextMapCarrier(map[string]string{}))
		assert.Nil(err) // ok
	})

	t.Run("ok", func(t *testing.T) {
		mt := newMockTracer()
		defer mt.Stop()
		assert := assert.New(t)

		sp := mt.StartSpan("op", tracer.WithSpanID(2))
		sp.SetTag(ext.ManualDrop, true)
		sp.SetBaggageItem("A", "B")
		sp.SetBaggageItem("C", "D")
		carrier := make(map[string]string)
		err := (&mocktracer{}).Inject(sp.Context(), tracer.TextMapCarrier(carrier))

		assert.Nil(err)
		assert.Equal(fmt.Sprintf("%d", sp.Context().TraceIDLower()), carrier[traceHeader])
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
		assert.Equal(uint64(1), ctx.TraceIDLower())
		assert.Equal(uint64(2), ctx.SpanID())

		ctx, err = mt.Extract(carry(traceHeader, "1", spanHeader, "2", baggagePrefix+"A", "B", baggagePrefix+"C", "D"))
		assert.Nil(err)
		ctx.ForeachBaggageItem(func(k string, v string) bool {
			if k == "a" {
				assert.Equal("B", v)
			}
			if k == "c" {
				assert.Equal("D", v)
			}
			return true
		})

		ctx, err = mt.Extract(carry(traceHeader, "1", spanHeader, "2", priorityHeader, "-1"))
		assert.Nil(err)
		sp, ok := ctx.SamplingPriority()
		assert.True(ok)
		assert.Equal(-1, sp)
	})

	t.Run("consistency", func(t *testing.T) {
		assert := assert.New(t)

		mt := newMockTracer()
		defer mt.Stop()
		sp := mt.StartSpan("op", tracer.WithSpanID(2))
		sp.SetTag(ext.ManualDrop, true)
		sp.SetBaggageItem("a", "B")
		sp.SetBaggageItem("C", "D")

		mc := tracer.TextMapCarrier(make(map[string]string))
		err := mt.Inject(sp.Context(), mc)
		assert.Nil(err)
		sc, err := mt.Extract(mc)
		assert.Nil(err)

		assert.Equal(sp.Context().TraceID(), sc.TraceID())
		assert.Equal(uint64(2), sc.SpanID())
		sc.ForeachBaggageItem(func(k string, v string) bool {
			if k == "a" {
				assert.Equal("B", v)
			}
			if k == "C" {
				assert.Equal("D", v)
			}
			return true
		})
	})
}
