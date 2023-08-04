// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"

	"github.com/stretchr/testify/assert"
)

func TestReportOpenSpans(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)

	os.Setenv("DD_TRACE_OPEN_SPAN_TIMEOUT", fmt.Sprint(100*time.Millisecond))
	defer os.Unsetenv("DD_TRACE_OPEN_SPAN_TIMEOUT")
	tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode())
	defer stop()

	tp.Reset()
	tp.Ignore("appsec: ", telemetry.LogPrefix)

	t.Run("on", func(t *testing.T) {
		assert.True(tracer.config.debugOpenSpans)
		assert.Equal(tracer.config.spanTimeout, 100*time.Millisecond)
	})

	t.Run("finished", func(t *testing.T) {
		s := tracer.StartSpan("operation")
		s.Finish()
		expected := fmt.Sprintf("Datadog Tracer %v WARN: Trace %v waiting on span %v", version.Tag, s.Context().TraceID(), s.Context().SpanID())
		assert.NotContains(tp.Logs(), expected)
	})

	t.Run("open", func(t *testing.T) {
		tp.Reset()
		s := tracer.StartSpan("operation")
		time.Sleep(time.Second)
		expected := fmt.Sprintf("Datadog Tracer %v WARN: Trace %v waiting on span %v", version.Tag, s.Context().TraceID(), s.Context().SpanID())
		assert.Contains(tp.Logs(), expected)
		s.Finish()
	})

	t.Run("both", func(t *testing.T) {
		tp.Reset()
		sf := tracer.StartSpan("op")
		sf.Finish()
		s := tracer.StartSpan("op2")
		time.Sleep(time.Second)
		notExpected := fmt.Sprintf("Datadog Tracer %v WARN: Trace %v waiting on span %v", version.Tag, sf.Context().TraceID(), sf.Context().SpanID())
		expected := fmt.Sprintf("Datadog Tracer %v WARN: Trace %v waiting on span %v", version.Tag, s.Context().TraceID(), s.Context().SpanID())
		assert.NotContains(tp.Logs(), notExpected)
		assert.Contains(tp.Logs(), expected)
		s.Finish()
	})

	t.Run("many", func(t *testing.T) {
		tp.Reset()
		expected := []string{}
		for i := 0; i < 10; i++ {
			s := tracer.StartSpan(fmt.Sprintf("operation%d", i)).(*span)
			e := fmt.Sprintf("Datadog Tracer %v WARN: Trace %v waiting on span %v", version.Tag, s.Context().TraceID(), s.Context().SpanID())
			if i%2 == 0 {
				s.Finish()
				time.Sleep(200 * time.Millisecond)
			} else {
				expected = append(expected, e)
			}
		}
		time.Sleep(500 * time.Millisecond)
		for _, e := range expected {
			assert.Contains(tp.Logs(), e)
		}
	})

	t.Run("many buckets", func(t *testing.T) {
		tp.Reset()
		expected := []string{}

		for i := 0; i < 5; i++ {
			s := tracer.StartSpan(fmt.Sprintf("operation%d", i))
			time.Sleep(150 * time.Millisecond)
			s.Finish()
		}
		for i := 0; i < 5; i++ {
			s := tracer.StartSpan(fmt.Sprintf("operation%d", i))
			expected = append(expected, fmt.Sprintf("Datadog Tracer %v WARN: Trace %v waiting on span %v", version.Tag, s.Context().TraceID(), s.Context().SpanID()))
			time.Sleep(150 * time.Millisecond)
		}

		for _, e := range expected {
			assert.Contains(tp.Logs(), e)
		}
	})

	t.Run("wait", func(t *testing.T) {
		tp.Reset()
		os.Setenv("DD_TRACE_OPEN_SPAN_TIMEOUT", fmt.Sprint(500*time.Millisecond))
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode())
		defer stop()

		s := tracer.StartSpan("operation")
		expected := fmt.Sprintf("Datadog Tracer %v WARN: Trace %v waiting on span %v", version.Tag, s.Context().TraceID(), s.Context().SpanID())

		assert.NotContains(tp.Logs(), expected)
		time.Sleep(time.Second)
		assert.Contains(tp.Logs(), expected)
		s.Finish()
	})

	t.Run("print", func(t *testing.T) {
		tp.Reset()
		os.Setenv("DD_TRACE_OPEN_SPAN_TIMEOUT", fmt.Sprint(500*time.Millisecond))
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode())
		defer stop()
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Datadog Tracer %v WARN: Remaining open spans: ", version.Tag))

		s := tracer.StartSpan("operation")
		sb.WriteString(fmt.Sprintf("[[%v]]", s))
		time.Sleep(500 * time.Millisecond)

		PrintOpenSpans()

		assert.Contains(tp.Logs(), sb.String())
	})
}

func TestDebugOpenSpansOff(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)
	tracer, _, _, stop := startTestTracer(t, WithLogger(tp))
	defer stop()

	tp.Reset()
	tp.Ignore("appsec: ", telemetry.LogPrefix)

	t.Run("default", func(t *testing.T) {
		assert.False(tracer.config.debugOpenSpans)
		assert.Equal(time.Duration(0), tracer.config.spanTimeout)
		s := tracer.StartSpan("operation")
		PrintOpenSpans()
		time.Sleep(2 * time.Second)
		expected := fmt.Sprintf("Datadog Tracer %v WARN: Trace %v waiting on span %v", version.Tag, s.Context().TraceID(), s.Context().SpanID())
		assert.NotContains(tp.Logs(), expected)
		assert.Contains(tp.Logs(), fmt.Sprintf("Datadog Tracer %v WARN: Debugging open spans is not enabled", version.Tag))
		s.Finish()
	})
}
