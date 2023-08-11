// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"

	"github.com/stretchr/testify/assert"
)

var warnPrefix = fmt.Sprintf("Datadog Tracer %v WARN: ", version.Tag)

func TestReportAbandonedSpans(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)

	tickerInterval = 100 * time.Millisecond

	tp.Ignore("appsec: ", telemetry.LogPrefix)

	t.Run("on", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		defer stop()
		assert.True(tracer.config.debugAbandonedSpans)
		assert.Equal(tracer.config.spanTimeout, 500*time.Millisecond)
	})

	t.Run("finished", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		defer stop()
		s := tracer.StartSpan("operation").(*span)
		s.Finish()
		expected := fmt.Sprintf("%s[name: %s, span_id: %d, trace_id: %d, age: %d],", warnPrefix, s.Name, s.SpanID, s.TraceID, s.Duration)
		assert.NotContains(tp.Logs(), expected)
	})

	t.Run("open", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		defer stop()
		s := tracer.StartSpan("operation").(*span)
		time.Sleep(time.Second)
		expected := fmt.Sprintf("%s[name: %s, span_id: %d, trace_id: %d, age: %d],", warnPrefix, s.Name, s.SpanID, s.TraceID, s.Duration)
		assert.Contains(tp.Logs(), fmt.Sprintf("%s%d abandoned spans:", warnPrefix, 1))
		assert.Contains(tp.Logs(), expected)
		s.Finish()
	})

	t.Run("both", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		defer stop()
		sf := tracer.StartSpan("op").(*span)
		sf.Finish()
		s := tracer.StartSpan("op2").(*span)
		time.Sleep(time.Second)
		notExpected := fmt.Sprintf("%s[name: %s, span_id: %d, trace_id: %d, age: %d],[name: %s, span_id: %d, trace_id: %d, age: %d],", warnPrefix, sf.Name, sf.SpanID, sf.TraceID, sf.Duration, s.Name, s.SpanID, s.TraceID, s.Duration)
		expected := fmt.Sprintf("%s[name: %s, span_id: %d, trace_id: %d, age: %d],", warnPrefix, s.Name, s.SpanID, s.TraceID, s.Duration)
		assert.Contains(tp.Logs(), fmt.Sprintf("%s%d abandoned spans:", warnPrefix, 1))
		assert.NotContains(tp.Logs(), notExpected)
		assert.Contains(tp.Logs(), expected)
		s.Finish()
	})

	t.Run("many", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		defer stop()
		var sb strings.Builder
		sb.WriteString(warnPrefix)
		for i := 0; i < 10; i++ {
			s := tracer.StartSpan(fmt.Sprintf("operation%d", i)).(*span)
			if i%2 == 0 {
				s.Finish()
			} else {
				e := fmt.Sprintf("[name: %s, span_id: %d, trace_id: %d, age: %d],", s.Name, s.SpanID, s.TraceID, s.Duration)
				sb.WriteString(e)
				time.Sleep(500 * time.Millisecond)
			}
		}
		time.Sleep(time.Second)
		assert.Contains(tp.Logs(), sb.String())
	})

	t.Run("many buckets", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		defer stop()
		var sb strings.Builder
		sb.WriteString(warnPrefix)

		for i := 0; i < 5; i++ {
			s := tracer.StartSpan(fmt.Sprintf("operation%d", i))
			s.Finish()
			time.Sleep(150 * time.Millisecond)
		}
		for i := 0; i < 5; i++ {
			s := tracer.StartSpan(fmt.Sprintf("operation2-%d", i)).(*span)
			sb.WriteString(fmt.Sprintf("[name: %s, span_id: %d, trace_id: %d, age: %d],", s.Name, s.SpanID, s.TraceID, s.Duration))
			time.Sleep(150 * time.Millisecond)
		}
		time.Sleep(time.Second)

		assert.Contains(tp.Logs(), fmt.Sprintf("%s%d abandoned spans:", warnPrefix, 5))
		assert.Contains(tp.Logs(), sb.String())
	})

	t.Run("stop", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(100*time.Millisecond))
		var sb strings.Builder
		sb.WriteString(warnPrefix)

		for i := 0; i < 5; i++ {
			s := tracer.StartSpan(fmt.Sprintf("operation%d", i)).(*span)
			sb.WriteString(fmt.Sprintf("[name: %s, span_id: %d, trace_id: %d, age: %d],", s.Name, s.SpanID, s.TraceID, s.Duration))
		}
		stop()
		time.Sleep(time.Second)
		assert.Contains(tp.Logs(), fmt.Sprintf("%s%d abandoned spans:", warnPrefix, 5))
		assert.Contains(tp.Logs(), sb.String())
	})

	t.Run("wait", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		defer stop()

		s := tracer.StartSpan("operation").(*span)
		expected := fmt.Sprintf("%s[name: %s, span_id: %d, trace_id: %d, age: %d],", warnPrefix, s.Name, s.SpanID, s.TraceID, s.Duration)

		assert.NotContains(tp.Logs(), expected)
		time.Sleep(time.Second)
		assert.Contains(tp.Logs(), expected)
		s.Finish()
	})

	t.Run("truncate", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		logSize = 10

		s := tracer.StartSpan("operation").(*span)
		msg := fmt.Sprintf("%s[name: %s, span_id: %d, trace_id: %d, age: %d],", warnPrefix, s.Name, s.SpanID, s.TraceID, s.Duration)
		stop()
		time.Sleep(500 * time.Millisecond)
		assert.NotContains(tp.Logs(), msg)
		assert.Contains(tp.Logs(), fmt.Sprintf("%sToo many abandoned spans. Truncating message.", warnPrefix))
	})
}

func TestDebugAbandonedSpansOff(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)
	tracer, _, _, stop := startTestTracer(t, WithLogger(tp))
	defer stop()

	tp.Reset()
	tp.Ignore("appsec: ", telemetry.LogPrefix)

	t.Run("default", func(t *testing.T) {
		assert.False(tracer.config.debugAbandonedSpans)
		assert.Equal(time.Duration(0), tracer.config.spanTimeout)
		s := tracer.StartSpan("operation")
		time.Sleep(time.Second)
		expected := fmt.Sprintf("%s Trace %v waiting on span %v", warnPrefix, s.Context().TraceID(), s.Context().SpanID())
		assert.NotContains(tp.Logs(), expected)
		s.Finish()
	})
}
