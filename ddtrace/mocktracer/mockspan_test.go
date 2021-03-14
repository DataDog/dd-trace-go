// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mocktracer

import (
	"errors"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
)

// basicSpan returns a span with no configuration, having the set operation name.
func basicSpan(operationName string) *mockspan {
	return newSpan(&mocktracer{}, operationName, &ddtrace.StartSpanConfig{})
}

func TestNewSpan(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		s := basicSpan("http.request")

		assert := assert.New(t)
		assert.Equal("http.request", s.name)
		assert.False(s.startTime.IsZero())
		assert.Zero(s.parentID)
		assert.NotNil(s.context)
		assert.NotZero(s.context.spanID)
		assert.Equal(s.context.spanID, s.context.traceID)
	})

	t.Run("options", func(t *testing.T) {
		tr := new(mocktracer)
		startTime := time.Now()
		tags := map[string]interface{}{"k": "v", "k1": "v1"}
		opts := &ddtrace.StartSpanConfig{
			StartTime: startTime,
			Tags:      tags,
		}
		s := newSpan(tr, "http.request", opts)

		assert := assert.New(t)
		assert.Equal(tr, s.tracer)
		assert.Equal("http.request", s.name)
		assert.Equal(startTime, s.startTime)
		assert.Equal(tags, s.tags)
	})

	t.Run("parent", func(t *testing.T) {
		baggage := map[string]string{"A": "B", "C": "D"}
		parentctx := &spanContext{spanID: 1, traceID: 2, baggage: baggage}
		opts := &ddtrace.StartSpanConfig{Parent: parentctx}
		s := newSpan(&mocktracer{}, "http.request", opts)

		assert := assert.New(t)
		assert.NotNil(s.context)
		assert.Equal(uint64(1), s.parentID)
		assert.Equal(uint64(2), s.context.traceID)
		assert.Equal(baggage, s.context.baggage)
	})
}

func TestSpanSetTag(t *testing.T) {
	s := basicSpan("http.request")
	s.SetTag("a", "b")
	s.SetTag("c", "d")

	assert := assert.New(t)
	assert.Len(s.Tags(), 3)
	assert.Equal("http.request", s.Tag(ext.ResourceName))
	assert.Equal("b", s.Tag("a"))
	assert.Equal("d", s.Tag("c"))
}

func TestSpanSetTagPriority(t *testing.T) {
	assert := assert.New(t)
	s := basicSpan("http.request")
	assert.False(s.context.hasSamplingPriority())
	s.SetTag(ext.SamplingPriority, -1)
	assert.True(s.context.hasSamplingPriority())
	assert.Equal(-1, s.context.samplingPriority())
}

func TestSpanSetTagName(t *testing.T) {
	assert := assert.New(t)
	s := basicSpan("http.request")
	s.SetTag(ext.SpanName, "my.request")
	assert.Equal("my.request", s.OperationName())
	assert.Equal("my.request", s.Tag(ext.SpanName))
}

func TestSpanTagImmutability(t *testing.T) {
	s := basicSpan("http.request")
	s.SetTag("a", "b")
	tags := s.Tags()
	tags["a"] = 123
	tags["b"] = 456

	assert := assert.New(t)
	assert.Equal("b", s.tags["a"])
	assert.Zero(s.tags["b"])
}

func TestSpanStartTime(t *testing.T) {
	startTime := time.Now()
	s := newSpan(&mocktracer{}, "http.request", &ddtrace.StartSpanConfig{StartTime: startTime})

	assert := assert.New(t)
	assert.Equal(startTime, s.startTime)
	assert.Equal(startTime, s.StartTime())
}

func TestSpanFinishTime(t *testing.T) {
	s := basicSpan("http.request")
	finishTime := time.Now()
	s.Finish(tracer.FinishTime(finishTime))

	assert := assert.New(t)
	assert.Equal(finishTime, s.finishTime)
	assert.Equal(finishTime, s.FinishTime())
}

func TestSpanOperationName(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		s := basicSpan("http.request")
		assert.Equal(t, "http.request", s.name)
		assert.Equal(t, "http.request", s.OperationName())
	})

	t.Run("default", func(t *testing.T) {
		s := basicSpan("http.request")
		s.SetOperationName("db.query")
		assert.Equal(t, "db.query", s.name)
		assert.Equal(t, "db.query", s.OperationName())
	})
}

func TestSpanBaggageFunctions(t *testing.T) {
	t.Run("SetBaggageItem", func(t *testing.T) {
		s := basicSpan("http.request")
		s.SetBaggageItem("a", "b")
		assert.Equal(t, "b", s.context.baggage["a"])
	})

	t.Run("BaggageItem", func(t *testing.T) {
		s := basicSpan("http.request")
		s.SetBaggageItem("a", "b")
		assert.Equal(t, "b", s.BaggageItem("a"))
	})
}

func TestSpanContext(t *testing.T) {
	t.Run("Context", func(t *testing.T) {
		s := basicSpan("http.request")
		assert.Equal(t, s.context, s.Context())
	})

	t.Run("IDs", func(t *testing.T) {
		parent := basicSpan("http.request")
		child := newSpan(&mocktracer{}, "db.query", &ddtrace.StartSpanConfig{
			Parent: parent.Context(),
		})

		assert := assert.New(t)
		assert.Equal(parent.SpanID(), child.ParentID())
		assert.Equal(parent.TraceID(), child.TraceID())
		assert.NotZero(child.SpanID())
	})
}

func TestSpanFinish(t *testing.T) {
	s := basicSpan("http.request")
	want := errors.New("some error")
	s.Finish(tracer.WithError(want))

	assert := assert.New(t)
	assert.False(s.FinishTime().IsZero())
	assert.True(s.FinishTime().Before(time.Now()))
	assert.Equal(want, s.Tag(ext.Error))
}

func TestSpanFinishTwice(t *testing.T) {
	s := basicSpan("http.request")
	wantError := errors.New("some error")
	s.Finish(tracer.WithError(wantError))

	assert := assert.New(t)
	wantTime := s.finishTime
	time.Sleep(2 * time.Millisecond)
	s.Finish(tracer.WithError(errors.New("new error")))
	assert.Equal(wantTime, s.finishTime)
	assert.Equal(wantError, s.Tag(ext.Error))
	assert.Equal(len(s.tracer.finishedSpans), 1)
}

func TestSpanWithID(t *testing.T) {
	spanID := uint64(123456789)
	span := newMockTracer().StartSpan("", tracer.WithSpanID(spanID))

	assert := assert.New(t)
	assert.Equal(spanID, span.Context().SpanID())
}
