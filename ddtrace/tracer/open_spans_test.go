// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"
)

func TestReportOpenSpans(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)

	tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(true), WithDebugMode(true))
	defer stop()

	tp.Reset()
	tp.Ignore("appsec: ", telemetry.LogPrefix)

	t.Run("finished", func(t *testing.T) {
		s := tracer.StartSpan("operation")
		s.Finish()
		expected := fmt.Sprintf("Datadog Tracer %v DEBUG: Trace %v waiting on span %v", version.Tag, s.Context().TraceID(), s.Context().SpanID())
		assert.NotContains(tp.Logs(), expected)
	})

	t.Run("open", func(t *testing.T) {
		s := tracer.StartSpan("operation")
		time.Sleep(3 * time.Second)
		expected := fmt.Sprintf("Datadog Tracer %v DEBUG: Trace %v waiting on span %v", version.Tag, s.Context().TraceID(), s.Context().SpanID())
		assert.Contains(tp.Logs(), expected)
		s.Finish()
	})

	t.Run("both", func(t *testing.T) {
		sf := tracer.StartSpan("op")
		sf.Finish()
		s := tracer.StartSpan("op2")
		time.Sleep(3 * time.Second)
		notExpected := fmt.Sprintf("Datadog Tracer %v DEBUG: Trace %v waiting on span %v", version.Tag, sf.Context().TraceID(), sf.Context().SpanID())
		expected := fmt.Sprintf("Datadog Tracer %v DEBUG: Trace %v waiting on span %v", version.Tag, s.Context().TraceID(), s.Context().SpanID())
		assert.NotContains(tp.Logs(), notExpected)
		assert.Contains(tp.Logs(), expected)
		s.Finish()
	})

	t.Run("many", func(t *testing.T) {
		expected := []string{}
		finished := []string{}
		for i := 0; i < 10; i++ {
			s := tracer.StartSpan(fmt.Sprintf("operation%d", i)).(*span)
			e := fmt.Sprintf("Datadog Tracer %v DEBUG: Trace %v waiting on span %v", version.Tag, s.Context().TraceID(), s.Context().SpanID())
			if i%2 == 0 {
				s.Finish()
				finished = append(finished, e)
				time.Sleep(2 * time.Second)
			} else {
				expected = append(expected, e)
			}
		}
		time.Sleep(3 * time.Second)
		l := tp.Logs()
		for _, e := range expected {
			assert.Contains(l, e)
		}
		for _, e := range finished {
			assert.NotContains(l, e)
		}
	})

	t.Run("wait", func(t *testing.T) {
		os.Setenv("DD_TRACE_OPEN_SPAN_TIMEOUT", fmt.Sprint(2*time.Second))
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(true), WithDebugMode(true))
		defer stop()

		s := tracer.StartSpan("operation")
		expected := fmt.Sprintf("Datadog Tracer %v DEBUG: Trace %v waiting on span %v", version.Tag, s.Context().TraceID(), s.Context().SpanID())

		assert.NotContains(tp.Logs(), expected)
		time.Sleep(4 * time.Second)
		assert.Contains(tp.Logs(), expected)
		s.Finish()
	})
}

func TestDebugOpenSpansOff(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)
	tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugMode(true))
	defer stop()

	tp.Reset()
	tp.Ignore("appsec: ", telemetry.LogPrefix)

	t.Run("default", func(t *testing.T) {
		assert.False(tracer.config.debugOpenSpans)
		assert.Equal(time.Duration(0), tracer.config.spanTimeout)
		s := tracer.StartSpan("operation")
		time.Sleep(3 * time.Second)
		expected := fmt.Sprintf("Datadog Tracer %v DEBUG: Trace %v waiting on span %v", version.Tag, s.Context().TraceID(), s.Context().SpanID())
		assert.NotContains(tp.Logs(), expected)
		s.Finish()
	})
}
