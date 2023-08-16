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
var spanStart = time.Date(2023, time.August, 18, 0, 0, 0, 0, time.UTC)

// setTestTime() sets the current time, which will be used to calculate the
// duration of abandoned spans.
func setTestTime() func() {
	current := spanStart.UnixNano() + 10*time.Minute.Nanoseconds() //use a fixed time instead of now
	now = func() int64 { return current }

	return func() {
		now = func() int64 { return time.Now().UnixNano() }
	}
}

// spanAge takes in a span and returns the current test duration of the
// span in seconds as a string
func spanAge(s *span) string {
	return fmt.Sprintf("%d sec", (now()-s.Start)/int64(time.Second))
}

func formatSpanString(s *span) string {
	s.Lock()
	msg := fmt.Sprintf("[name: %s, span_id: %d, trace_id: %d, age: %s],", s.Name, s.SpanID, s.TraceID, spanAge(s))
	s.Unlock()
	return msg
}

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
		tp.Reset()
		defer setTestTime()()
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		defer stop()
		s := tracer.StartSpan("operation", StartTime(spanStart)).(*span)
		s.Finish()
		expected := fmt.Sprintf("%s%s", warnPrefix, formatSpanString(s))
		assert.NotContains(tp.Logs(), expected)
	})

	t.Run("open", func(t *testing.T) {
		tp.Reset()
		defer setTestTime()()
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		defer stop()
		s := tracer.StartSpan("operation", StartTime(spanStart)).(*span)
		time.Sleep(200 * time.Millisecond)
		assert.Contains(tp.Logs(), fmt.Sprintf("%s%d abandoned spans:", warnPrefix, 1))
		assert.Contains(tp.Logs(), fmt.Sprintf("%s%s", warnPrefix, formatSpanString(s)))
	})

	t.Run("both", func(t *testing.T) {
		tp.Reset()
		defer setTestTime()()
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		defer stop()
		sf := tracer.StartSpan("op", StartTime(spanStart)).(*span)
		sf.Finish()
		s := tracer.StartSpan("op2", StartTime(spanStart)).(*span)
		notExpected := fmt.Sprintf("%s%s,%s,", warnPrefix, formatSpanString(sf), formatSpanString(s))
		expected := fmt.Sprintf("%s%s", warnPrefix, formatSpanString(s))
		time.Sleep(500 * time.Millisecond)
		assert.Contains(tp.Logs(), fmt.Sprintf("%s%d abandoned spans:", warnPrefix, 1))
		assert.NotContains(tp.Logs(), notExpected)
		assert.Contains(tp.Logs(), expected)
		s.Finish()
	})

	t.Run("many", func(t *testing.T) {
		tp.Reset()
		defer setTestTime()()
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		defer stop()
		var sb strings.Builder
		sb.WriteString(warnPrefix)
		for i := 0; i < 10; i++ {
			s := tracer.StartSpan(fmt.Sprintf("operation%d", i), StartTime(spanStart)).(*span)
			if i%2 == 0 {
				s.Finish()
			} else {
				sb.WriteString(formatSpanString(s))
			}
		}
		time.Sleep(200 * time.Millisecond)
		assert.Contains(tp.Logs(), sb.String())
	})

	t.Run("many buckets", func(t *testing.T) {
		tp.Reset()
		defer setTestTime()()
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		defer stop()
		var sb strings.Builder
		sb.WriteString(warnPrefix)
		for i := 0; i < 5; i++ {
			s := tracer.StartSpan(fmt.Sprintf("operation%d", i), StartTime(spanStart))
			s.Finish()
			time.Sleep(150 * time.Millisecond)
		}
		for i := 0; i < 5; i++ {
			s := tracer.StartSpan(fmt.Sprintf("operation2-%d", i), StartTime(spanStart)).(*span)
			sb.WriteString(formatSpanString(s))
			time.Sleep(150 * time.Millisecond)
		}

		assert.Contains(tp.Logs(), fmt.Sprintf("%s%d abandoned spans:", warnPrefix, 5))
		assert.Contains(tp.Logs(), sb.String())
	})

	t.Run("stop", func(t *testing.T) {
		tp.Reset()
		defer setTestTime()()
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(100*time.Millisecond))
		var sb strings.Builder
		sb.WriteString(warnPrefix)

		for i := 0; i < 5; i++ {
			s := tracer.StartSpan(fmt.Sprintf("operation%d", i), StartTime(spanStart)).(*span)
			sb.WriteString(formatSpanString(s))
		}
		stop()
		time.Sleep(200 * time.Millisecond)
		assert.Contains(tp.Logs(), fmt.Sprintf("%s%d abandoned spans:", warnPrefix, 5))
		assert.Contains(tp.Logs(), sb.String())
	})

	t.Run("wait", func(t *testing.T) {
		tp.Reset()
		defer setTestTime()()
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		defer stop()

		s := tracer.StartSpan("operation", StartTime(spanStart)).(*span)
		expected := fmt.Sprintf("%s%s", warnPrefix, formatSpanString(s))

		assert.NotContains(tp.Logs(), expected)
		time.Sleep(150 * time.Millisecond)
		assert.Contains(tp.Logs(), expected)
		s.Finish()
	})

	t.Run("truncate", func(t *testing.T) {
		tp.Reset()
		defer setTestTime()()
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		logSize = 10

		s := tracer.StartSpan("operation", StartTime(spanStart)).(*span)
		msg := formatSpanString(s)
		stop()
		time.Sleep(200 * time.Millisecond)
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
		s := tracer.StartSpan("operation", StartTime(spanStart))
		time.Sleep(150 * time.Millisecond)
		expected := fmt.Sprintf("%s Trace %v waiting on span %v", warnPrefix, s.Context().TraceID(), s.Context().SpanID())
		assert.NotContains(tp.Logs(), expected)
		s.Finish()
	})
}
