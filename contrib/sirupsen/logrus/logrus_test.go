// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package logrus

import (
	"context"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestFire_NoSpanInContext(t *testing.T) {
	hook := &DDContextLogHook{}
	e := logrus.NewEntry(logrus.New())
	e.Context = context.Background()
	err := hook.Fire(e)

	assert.NoError(t, err)
	assert.NotContains(t, e.Data, "dd.trace_id")
	assert.NotContains(t, e.Data, "dd.span_id")
}

func TestFire_64BitTraceID(t *testing.T) {
	tracer.Start()
	defer tracer.Stop()
	origSpan, sctx := tracer.StartSpanFromContext(context.Background(), "testSpan", tracer.WithSpanID(1234))
	sctx = tracer.ContextWithSpan(sctx, span64{origSpan})

	hook := &DDContextLogHook{}
	e := logrus.NewEntry(logrus.New())
	e.Context = sctx
	err := hook.Fire(e)

	assert.NoError(t, err)
	assert.Equal(t, "1234", e.Data["dd.trace_id"])
	assert.Equal(t, "1234", e.Data["dd.span_id"])
}

func TestFire_128BitTraceID(t *testing.T) {
	t.Setenv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", "false")
	tracer.Start()
	defer tracer.Stop()
	_, sctx := tracer.StartSpanFromContext(context.Background(), "testSpan", tracer.WithSpanID(1234))

	hook := &DDContextLogHook{}
	e := logrus.NewEntry(logrus.New())
	e.Context = sctx
	err := hook.Fire(e)

	assert.NoError(t, err)
	assert.Equal(t, "000000000000000000000000000004d2", e.Data["dd.trace_id"]) // 0x4d2 = 1234
	assert.Equal(t, "1234", e.Data["dd.span_id"])
}

// span64 is a tracer.Span implementation that masks 128-bit Trace ID support.
type span64 struct {
	s tracer.Span
}

var _ tracer.Span = span64{}

func (s span64) BaggageItem(key string) string {
	return s.s.BaggageItem(key)
}

func (s span64) Context() ddtrace.SpanContext {
	return context64{s.s.Context()}
}

func (s span64) Finish(opts ...ddtrace.FinishOption) {
	s.s.Finish(opts...)
}

func (s span64) SetBaggageItem(key string, val string) {
	s.s.SetBaggageItem(key, val)
}

func (s span64) SetOperationName(operationName string) {
	s.s.SetOperationName(operationName)
}

func (s span64) SetTag(key string, value interface{}) {
	s.s.SetTag(key, value)
}

// context64 is a ddtrace.SpanContext implementation that masks 128-bit Trace ID
// support.
type context64 struct {
	c ddtrace.SpanContext
}

var _ ddtrace.SpanContext = context64{}

func (c context64) SpanID() uint64 {
	return c.c.SpanID()
}

func (c context64) TraceID() uint64 {
	return c.c.TraceID()
}

func (c context64) ForeachBaggageItem(handler func(k, v string) bool) {
	c.c.ForeachBaggageItem(handler)
}
