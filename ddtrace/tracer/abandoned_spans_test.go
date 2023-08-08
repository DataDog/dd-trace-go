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

func TestAbandonedSpansLists(t *testing.T) {
	assert := assert.New(t)

	tracer, _, _, stop := startTestTracer(t)
	defer stop()

	t.Run("spans list", func(t *testing.T) {
		t.Run("append", func(t *testing.T) {
			sl := spansList{}
			s := tracer.StartSpan("operation").(*span)
			sl.Append(s)
			assert.Equal(fmt.Sprintf("[Span Name: %v, Span ID: %v, Trace ID: %v],", s.Name, s.SpanID, s.TraceID), sl.String())
		})

		t.Run("nil remove", func(t *testing.T) {
			sl := spansList{}
			s := tracer.StartSpan("operation").(*span)
			sl.Remove(s)
			assert.Equal("", sl.String())
		})

		sl := spansList{}
		expected := []string{}
		t.Run("append many", func(t *testing.T) {
			for i := 0; i < 6; i++ {
				s := tracer.StartSpan(fmt.Sprintf("operation%d", i)).(*span)
				sl.Append(s)
				expected = append(expected, fmt.Sprintf("[Span Name: %v, Span ID: %v, Trace ID: %v],", s.Name, s.SpanID, s.TraceID))
			}

			ls := sl.String()
			for _, v := range expected {
				assert.Contains(ls, v)
			}
		})

		t.Run("remove head", func(t *testing.T) {
			s := sl.head.Element
			sl.Remove(s)

			ls := sl.String()
			for i, v := range expected {
				if i == 0 {
					assert.NotContains(ls, v)
					continue
				}
				assert.Contains(ls, v)
			}
		})

		t.Run("remove tail", func(t *testing.T) {
			s := sl.tail.Element
			sl.Remove(s)

			ls := sl.String()
			for i, v := range expected {
				if i == 0 || i == 5 {
					assert.NotContains(ls, v)
					continue
				}
				assert.Contains(ls, v)
			}
		})

		t.Run("remove middle", func(t *testing.T) {
			s := sl.head.Next.Element
			sl.Remove(s)

			ls := sl.String()
			for i, v := range expected {
				if i == 0 || i == 5 || i == 2 {
					assert.NotContains(ls, v)
					continue
				}
				assert.Contains(ls, v)
			}
		})
	})

	t.Run("abandoned spans", func(t *testing.T) {
		t.Run("extend", func(t *testing.T) {
			a := AbandonedList{}
			s := tracer.StartSpan("operation1").(*span)
			a.Extend(s)
			assert.Equal(fmt.Sprintf("[Span Name: %v, Span ID: %v, Trace ID: %v],", s.Name, s.SpanID, s.TraceID), a.String())
		})

		t.Run("nil remove", func(t *testing.T) {
			a := AbandonedList{}
			sl := &spansList{
				head: &spanNode{
					Element: tracer.StartSpan("operation").(*span),
				},
			}
			a.RemoveBucket(sl)
			assert.Equal("", a.String())
		})

		a := AbandonedList{}
		expected := []string{}
		t.Run("extend many", func(t *testing.T) {
			for i := 0; i < 5; i++ {
				s := tracer.StartSpan(fmt.Sprintf("operation%d", i)).(*span)
				a.Extend(s)
				expected = append(expected, fmt.Sprintf("[Span Name: %v, Span ID: %v, Trace ID: %v],", s.Name, s.SpanID, s.TraceID))
			}

			sa := a.String()
			for _, v := range expected {
				assert.Contains(sa, v)
			}
		})

		t.Run("remove head", func(t *testing.T) {
			h := a.head
			a.RemoveBucket(h)

			sa := a.String()
			for i, v := range expected {
				if i == 0 {
					assert.NotContains(sa, v)
					continue
				}
				assert.Contains(sa, v)
			}
		})

		t.Run("remove tail", func(t *testing.T) {
			h := a.tail
			a.RemoveBucket(h)

			sa := a.String()
			for i, v := range expected {
				if i == 0 || i == 4 {
					assert.NotContains(sa, v)
					continue
				}
				assert.Contains(sa, v)
			}
		})

		t.Run("remove", func(t *testing.T) {
			h := a.head.Next
			a.RemoveBucket(h)

			sa := a.String()
			for i, v := range expected {
				if i == 0 || i == 4 || i == 2 {
					assert.NotContains(sa, v)
					continue
				}
				assert.Contains(sa, v)
			}
		})

	})
}

func TestReportAbandonedSpans(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)

	tickerInterval = 100 * time.Millisecond
	tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
	defer stop()

	tp.Ignore("appsec: ", telemetry.LogPrefix)

	t.Run("on", func(t *testing.T) {
		assert.True(tracer.config.debugAbandonedSpans)
		assert.Equal(tracer.config.spanTimeout, 500*time.Millisecond)
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
			s := tracer.StartSpan(fmt.Sprintf("operation%d", i))
			if i%2 == 0 {
				s.Finish()
			} else {
				e := fmt.Sprintf("Datadog Tracer %v WARN: Trace %v waiting on span %v", version.Tag, s.Context().TraceID(), s.Context().SpanID())
				expected = append(expected, e)
			}
			time.Sleep(200 * time.Millisecond)
		}
		time.Sleep(time.Second)
		for _, e := range expected {
			assert.Contains(tp.Logs(), e)
		}
	})

	t.Run("many buckets", func(t *testing.T) {
		tp.Reset()
		expected := []string{}

		for i := 0; i < 5; i++ {
			s := tracer.StartSpan(fmt.Sprintf("operation%d", i))
			s.Finish()
			time.Sleep(150 * time.Millisecond)
		}
		for i := 0; i < 5; i++ {
			s := tracer.StartSpan(fmt.Sprintf("operation%d", i))
			expected = append(expected, fmt.Sprintf("Datadog Tracer %v WARN: Trace %v waiting on span %v", version.Tag, s.Context().TraceID(), s.Context().SpanID()))
			time.Sleep(150 * time.Millisecond)
		}

		time.Sleep(time.Second)

		for _, e := range expected {
			assert.Contains(tp.Logs(), e)
		}
	})

	t.Run("stop", func(t *testing.T) {
		tp.Reset()
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(100*time.Millisecond))
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Datadog Tracer %v WARN: ", version.Tag))
		sb.WriteString("Abandoned Spans: ")

		for i := 0; i < 5; i++ {
			s := tracer.StartSpan(fmt.Sprintf("operation%d", i)).(*span)
			sb.WriteString(fmt.Sprintf("[Span Name: %v, Span ID: %v, Trace ID: %v],", s.Name, s.SpanID, s.TraceID))
		}
		stop()
		time.Sleep(time.Second)
		assert.Contains(tp.Logs(), sb.String())
	})

	t.Run("wait", func(t *testing.T) {
		tp.Reset()
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond))
		defer stop()

		s := tracer.StartSpan("operation")
		expected := fmt.Sprintf("Datadog Tracer %v WARN: Trace %v waiting on span %v", version.Tag, s.Context().TraceID(), s.Context().SpanID())

		assert.NotContains(tp.Logs(), expected)
		time.Sleep(time.Second)
		assert.Contains(tp.Logs(), expected)
		s.Finish()
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
		expected := fmt.Sprintf("Datadog Tracer %v WARN: Trace %v waiting on span %v", version.Tag, s.Context().TraceID(), s.Context().SpanID())
		assert.NotContains(tp.Logs(), expected)
		s.Finish()
	})
}
