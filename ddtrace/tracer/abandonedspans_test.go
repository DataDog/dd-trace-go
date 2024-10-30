// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"

	"github.com/stretchr/testify/assert"
)

var warnPrefix = fmt.Sprintf("Datadog Tracer %v WARN: ", version.Tag)
var spanStart = time.Date(2023, time.August, 18, 0, 0, 0, 0, time.UTC)

// setTestTime() sets the current time, which will be used to calculate the
// duration of abandoned spans.
func setTestTime() func() {
	current := spanStart.UnixNano() + 10*time.Minute.Nanoseconds() // use a fixed time instead of now
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

func assertProcessedSpans(assert *assert.Assertions, t *tracer, startedSpans, finishedSpans int) {
	d := t.abandonedSpansDebugger
	cond := func() bool {
		return atomic.LoadUint32(&d.addedSpans) >= uint32(startedSpans) &&
			atomic.LoadUint32(&d.removedSpans) >= uint32(finishedSpans)
	}
	assert.Eventually(cond, 1*time.Second, 75*time.Millisecond)
	// We expect logs to be generated when startedSpans and finishedSpans are different.
	// At least there should be 3 lines: 1. debugger activation, 2. detected spans warn, and 3. the details.
	if startedSpans == finishedSpans {
		return
	}
	cond = func() bool {
		return len(t.config.logger.(*log.RecordLogger).Logs()) > 2
	}
	assert.Eventually(cond, 1*time.Second, 75*time.Millisecond)
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

	t.Run("on", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(100*time.Millisecond))
		defer stop()
		assert.True(tracer.config.debugAbandonedSpans)
		assert.Equal(tracer.config.spanTimeout, 100*time.Millisecond)
	})

	t.Run("finished", func(t *testing.T) {
		tp.Reset()
		defer setTestTime()()
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		defer stop()
		s := tracer.StartSpan("operation", StartTime(spanStart)).(*span)
		s.Finish()
		assertProcessedSpans(assert, tracer, 1, 1)
		expected := fmt.Sprintf("%s%s", warnPrefix, formatSpanString(s))
		assert.NotContains(tp.Logs(), expected)
	})

	t.Run("open", func(t *testing.T) {
		tp.Reset()
		defer setTestTime()()
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		defer stop()
		s := tracer.StartSpan("operation", StartTime(spanStart)).(*span)
		assertProcessedSpans(assert, tracer, 1, 0)
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
		assertProcessedSpans(assert, tracer, 2, 1)
		assert.Contains(tp.Logs(), fmt.Sprintf("%s%d abandoned spans:", warnPrefix, 1))
		assert.NotContains(tp.Logs(), notExpected)
		assert.Contains(tp.Logs(), expected)
		s.Finish()
	})

	t.Run("timeout", func(t *testing.T) {
		tp.Reset()
		defer setTestTime()()
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(3*time.Minute))
		defer stop()
		s1 := tracer.StartSpan("op", StartTime(spanStart)).(*span)
		delayedStart := spanStart.Add(8 * time.Minute)
		s2 := tracer.StartSpan("op2", StartTime(delayedStart)).(*span)
		notExpected := fmt.Sprintf("%s%s,%s,", warnPrefix, formatSpanString(s1), formatSpanString(s2))
		expected := fmt.Sprintf("%s%s", warnPrefix, formatSpanString(s1))
		assertProcessedSpans(assert, tracer, 2, 0)
		assert.Contains(tp.Logs(), fmt.Sprintf("%s%d abandoned spans:", warnPrefix, 1))
		assert.NotContains(tp.Logs(), notExpected)
		assert.Contains(tp.Logs(), expected)
	})

	// This test ensures that the debug mode works as expected and returns invalid information
	// given invalid inputs.
	t.Run("invalid", func(t *testing.T) {
		tp.Reset()
		defer setTestTime()()
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(10*time.Minute))
		defer stop()
		delayedStart := spanStart.Add(1 * time.Minute)
		s1 := tracer.StartSpan("op", StartTime(delayedStart)).(*span)
		s2 := tracer.StartSpan("op2", StartTime(spanStart)).(*span)
		notExpected := fmt.Sprintf("%s%s,%s,", warnPrefix, formatSpanString(s1), formatSpanString(s2))
		notExpected2 := fmt.Sprintf("%s%s,%s,", warnPrefix, formatSpanString(s1), formatSpanString(s2))
		assertProcessedSpans(assert, tracer, 2, 0)
		assert.NotContains(tp.Logs(), notExpected)
		assert.NotContains(tp.Logs(), notExpected2)
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
		assertProcessedSpans(assert, tracer, 10, 5)
		b := sb.String()
		assert.Contains(tp.Logs(), b)
	})

	t.Run("many buckets", func(t *testing.T) {
		tp.Reset()
		defer setTestTime()()
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(100*time.Millisecond))
		defer stop()
		var sb strings.Builder
		sb.WriteString(warnPrefix)
		for i := 0; i < 5; i++ {
			s := tracer.StartSpan(fmt.Sprintf("operation%d", i), StartTime(spanStart))
			s.Finish()
			time.Sleep(15 * time.Millisecond)
		}
		for i := 0; i < 5; i++ {
			s := tracer.StartSpan(fmt.Sprintf("operation2-%d", i), StartTime(spanStart)).(*span)
			sb.WriteString(formatSpanString(s))
			time.Sleep(15 * time.Millisecond)
		}
		assertProcessedSpans(assert, tracer, 10, 5)
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
		assertProcessedSpans(assert, tracer, 5, 0)
		stop()
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
		assertProcessedSpans(assert, tracer, 1, 0)
		assert.Contains(tp.Logs(), expected)
		s.Finish()
	})

	t.Run("truncate", func(t *testing.T) {
		tp.Reset()
		defer setTestTime()()
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		// Forget to revert this global variable will lead to broken tests if run multiples times through `-count`.
		logSize = 10
		defer func() {
			logSize = 9000
		}()

		s := tracer.StartSpan("operation", StartTime(spanStart)).(*span)
		msg := formatSpanString(s)
		assertProcessedSpans(assert, tracer, 1, 0)
		stop()
		assert.NotContains(tp.Logs(), msg)
		assert.Contains(tp.Logs(), fmt.Sprintf("%sToo many abandoned spans. Truncating message.", warnPrefix))
	})
}

func TestDebugAbandonedSpansOff(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)
	tracer, _, _, stop := startTestTracer(t, WithLogger(tp))
	defer stop()

	t.Run("default", func(t *testing.T) {
		assert.False(tracer.config.debugAbandonedSpans)
		assert.Equal(time.Duration(0), tracer.config.spanTimeout)
		expected := "Abandoned spans logs enabled."
		s := tracer.StartSpan("operation", StartTime(spanStart))
		time.Sleep(100 * time.Millisecond)
		assert.NotContains(tp.Logs(), expected)
		s.Finish()
	})
}
