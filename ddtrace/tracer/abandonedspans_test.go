// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/statsdtest"
	"github.com/DataDog/dd-trace-go/v2/internal/synctest"
	"github.com/DataDog/dd-trace-go/v2/internal/version"

	"github.com/stretchr/testify/assert"
)

var warnPrefix = fmt.Sprintf("Datadog Tracer %v WARN: ", version.Tag)
var spanStartTime = time.Date(2023, time.August, 18, 0, 0, 0, 0, time.UTC)

// spanAge takes in a span and returns the current test duration of the
// span in seconds as a string
func spanAge(s *Span) string {
	return fmt.Sprintf("%d sec", (now()-s.start)/int64(time.Second))
}

func assertProcessedSpans(assert *assert.Assertions, t *tracer, startedSpans, finishedSpans int, ticker time.Duration) {
	d := t.abandonedSpansDebugger
	cond := func() bool {
		return atomic.LoadUint32(&d.addedSpans) >= uint32(startedSpans) &&
			atomic.LoadUint32(&d.removedSpans) >= uint32(finishedSpans)
	}
	assert.Eventually(cond, 1*time.Second, ticker)
	// We expect logs to be generated when startedSpans and finishedSpans are different.
	// At least there should be 3 lines: 1. debugger activation, 2. detected spans warn, and 3. the details.
	if startedSpans == finishedSpans {
		return
	}
	cond = func() bool {
		return len(t.config.logger.(*log.RecordLogger).Logs()) > 2
	}
	assert.Eventually(cond, 1*time.Second, ticker)
}

func formatSpanString(s *Span) string {
	name, spanID, traceID, integration := s.debugInfo()
	msg := fmt.Sprintf("[name: %s, integration: %s, span_id: %d, trace_id: %d, age: %s],", name, integration, spanID, traceID, spanAge(s))
	return msg
}

func TestAbandonedSpansMetric(t *testing.T) {
	tp := new(log.RecordLogger)
	tickerInterval = 100 * time.Millisecond
	t.Run("finished", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bubbleNow := time.Now()
			var tg statsdtest.TestStatsdClient
			assert := assert.New(t)
			tp.Reset()
			tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond), withStatsdClient(&tg), withNopInfoHTTPClient())
			assert.NoError(err)
			defer stop()
			s := tracer.StartSpan("operation", StartTime(bubbleNow.Add(-10*time.Minute)))
			s.Finish()
			assertProcessedSpans(assert, tracer, 1, 1, tickerInterval/10)
			assert.Empty(tg.GetCallsByName("datadog.tracer.abandoned_spans"))
		})
	})
	t.Run("open", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bubbleNow := time.Now()
			var tg statsdtest.TestStatsdClient
			assert := assert.New(t)
			tp.Reset()
			tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond), withStatsdClient(&tg), withNopInfoHTTPClient())
			assert.NoError(err)
			defer stop()
			tracer.StartSpan("operation", StartTime(bubbleNow.Add(-10*time.Minute)), Tag(ext.Component, "some_integration_name"))
			assertProcessedSpans(assert, tracer, 1, 0, tickerInterval/10)
			// Wait for the ticker to fire and send metrics
			assert.Eventually(func() bool {
				calls := tg.GetCallsByName("datadog.tracer.abandoned_spans")
				return len(calls) == 1
			}, 2*time.Second, tickerInterval/10)
			calls := tg.GetCallsByName("datadog.tracer.abandoned_spans")
			assert.Len(calls, 1)
			call := calls[0]
			assert.Equal([]string{"name:operation", "integration:some_integration_name"}, call.Tags())
		})
	})

	t.Run("both", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bubbleNow := time.Now()
			var tg statsdtest.TestStatsdClient
			assert := assert.New(t)
			tp.Reset()
			tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond), withStatsdClient(&tg), withNopInfoHTTPClient())
			assert.NoError(err)
			defer stop()
			sf := tracer.StartSpan("op", StartTime(bubbleNow.Add(-10*time.Minute)))
			sf.Finish()
			s := tracer.StartSpan("op2", StartTime(bubbleNow.Add(-10*time.Minute)))
			assertProcessedSpans(assert, tracer, 2, 1, tickerInterval/10)
			// Wait for the ticker to fire and send metrics
			assert.Eventually(func() bool {
				calls := tg.GetCallsByName("datadog.tracer.abandoned_spans")
				return len(calls) == 1
			}, 2*time.Second, tickerInterval/10)
			calls := tg.GetCallsByName("datadog.tracer.abandoned_spans")
			assert.Len(calls, 1)
			s.Finish()
		})
	})
}

func TestReportAbandonedSpans(t *testing.T) {
	tp := new(log.RecordLogger)
	tickerInterval = 100 * time.Millisecond

	t.Run("on", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(100*time.Millisecond))
		assert.Nil(err)
		defer stop()
		assert.True(tracer.config.internalConfig.DebugAbandonedSpans())
		assert.Equal(tracer.config.internalConfig.SpanTimeout(), 100*time.Millisecond)
	})

	t.Run("finished", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bubbleNow := time.Now()
			assert := assert.New(t)
			tp.Reset()
			tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond), withNopInfoHTTPClient(), withNoopStats())
			assert.Nil(err)
			defer stop()
			s := tracer.StartSpan("operation", StartTime(bubbleNow.Add(-10*time.Minute)))
			s.Finish()
			assertProcessedSpans(assert, tracer, 1, 1, tickerInterval/10)
			expected := fmt.Sprintf("%s%s", warnPrefix, formatSpanString(s))
			assert.NotContains(tp.Logs(), expected)
		})
	})

	t.Run("open", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bubbleNow := time.Now()
			assert := assert.New(t)
			tp.Reset()
			tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond), withNopInfoHTTPClient(), withNoopStats())
			assert.Nil(err)
			defer stop()
			s := tracer.StartSpan("operation", StartTime(bubbleNow.Add(-10*time.Minute)))
			assertProcessedSpans(assert, tracer, 1, 0, tickerInterval/10)
			expectedCount := fmt.Sprintf("%s%d abandoned spans:", warnPrefix, 1)
			expectedSpan := fmt.Sprintf("%s%s", warnPrefix, formatSpanString(s))
			assert.Eventually(func() bool {
				logs := tp.Logs()
				return slices.Contains(logs, expectedCount) && slices.Contains(logs, expectedSpan)
			}, 2*time.Second, tickerInterval/10)
		})
	})

	t.Run("both", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bubbleNow := time.Now()
			assert := assert.New(t)
			tp.Reset()
			tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond), withNopInfoHTTPClient(), withNoopStats())
			assert.Nil(err)
			defer stop()
			sf := tracer.StartSpan("op", StartTime(bubbleNow.Add(-10*time.Minute)))
			sf.Finish()
			s := tracer.StartSpan("op2", StartTime(bubbleNow.Add(-10*time.Minute)))
			notExpected := fmt.Sprintf("%s%s,%s,", warnPrefix, formatSpanString(sf), formatSpanString(s))
			expected := fmt.Sprintf("%s%s", warnPrefix, formatSpanString(s))
			expectedCount := fmt.Sprintf("%s%d abandoned spans:", warnPrefix, 1)
			assertProcessedSpans(assert, tracer, 2, 1, tickerInterval/10)
			assert.Eventually(func() bool {
				logs := tp.Logs()
				return slices.Contains(logs, expectedCount) &&
					!slices.Contains(logs, notExpected) &&
					slices.Contains(logs, expected)
			}, 2*time.Second, tickerInterval/10)
			s.Finish()
		})
	})

	t.Run("timeout", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bubbleNow := time.Now()
			assert := assert.New(t)
			tp.Reset()
			tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(3*time.Minute), withNopInfoHTTPClient(), withNoopStats())
			assert.Nil(err)
			defer stop()
			// s1 is 10 min old (older than 3 min timeout) → should be logged
			s1 := tracer.StartSpan("op", StartTime(bubbleNow.Add(-10*time.Minute)))
			// s2 is 2 min old (newer than 3 min timeout) → should not be logged
			s2 := tracer.StartSpan("op2", StartTime(bubbleNow.Add(-2*time.Minute)))
			notExpected := fmt.Sprintf("%s%s,%s,", warnPrefix, formatSpanString(s1), formatSpanString(s2))
			expected := fmt.Sprintf("%s%s", warnPrefix, formatSpanString(s1))
			expectedCount := fmt.Sprintf("%s%d abandoned spans:", warnPrefix, 1)
			assertProcessedSpans(assert, tracer, 2, 0, tickerInterval/10)
			assert.Eventually(func() bool {
				logs := tp.Logs()
				return slices.Contains(logs, expectedCount) &&
					!slices.Contains(logs, notExpected) &&
					slices.Contains(logs, expected)
			}, 2*time.Second, tickerInterval/10)
		})
	})

	// This test ensures that the debug mode works as expected and returns invalid information
	// given invalid inputs.
	t.Run("invalid", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bubbleNow := time.Now()
			assert := assert.New(t)
			tp.Reset()
			tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(10*time.Minute), withNopInfoHTTPClient(), withNoopStats())
			assert.Nil(err)
			defer stop()
			// s1 is 9 min old (newer than 10 min timeout) → should not be logged
			s1 := tracer.StartSpan("op", StartTime(bubbleNow.Add(-9*time.Minute)))
			// s2 is 10 min old (at the boundary) → will be logged individually but s1+s2 combined won't appear
			s2 := tracer.StartSpan("op2", StartTime(bubbleNow.Add(-10*time.Minute)))
			notExpected := fmt.Sprintf("%s%s,%s,", warnPrefix, formatSpanString(s1), formatSpanString(s2))
			notExpected2 := fmt.Sprintf("%s%s,%s,", warnPrefix, formatSpanString(s1), formatSpanString(s2))
			assertProcessedSpans(assert, tracer, 2, 0, tickerInterval/10)
			assert.NotContains(tp.Logs(), notExpected)
			assert.NotContains(tp.Logs(), notExpected2)
		})
	})

	t.Run("many", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bubbleNow := time.Now()
			assert := assert.New(t)
			tp.Reset()
			tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond), withNopInfoHTTPClient(), withNoopStats())
			assert.Nil(err)
			defer stop()
			var sb strings.Builder
			sb.WriteString(warnPrefix)
			for i := range 10 {
				s := tracer.StartSpan(fmt.Sprintf("operation%d", i), StartTime(bubbleNow.Add(-10*time.Minute)))
				if i%2 == 0 {
					s.Finish()
				} else {
					sb.WriteString(formatSpanString(s))
				}
			}
			assertProcessedSpans(assert, tracer, 10, 5, tickerInterval/10)
			expected := sb.String()
			assert.Eventually(func() bool {
				return slices.Contains(tp.Logs(), expected)
			}, 2*time.Second, tickerInterval/10)
		})
	})

	t.Run("many buckets", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bubbleNow := time.Now()
			assert := assert.New(t)
			tp.Reset()
			tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(100*time.Millisecond), withNopInfoHTTPClient(), withNoopStats())
			assert.Nil(err)
			defer stop()
			var sb strings.Builder
			sb.WriteString(warnPrefix)
			for i := range 5 {
				s := tracer.StartSpan(fmt.Sprintf("operation%d", i), StartTime(bubbleNow.Add(-10*time.Minute)))
				s.Finish()
				time.Sleep(15 * time.Millisecond) // instant: fake clock advances 15ms
			}
			for i := range 5 {
				s := tracer.StartSpan(fmt.Sprintf("operation2-%d", i), StartTime(bubbleNow.Add(-10*time.Minute)))
				sb.WriteString(formatSpanString(s))
				time.Sleep(15 * time.Millisecond) // instant: fake clock advances 15ms
			}
			assertProcessedSpans(assert, tracer, 10, 5, tickerInterval/2)
			// Wait for the ticker to fire and log the abandoned spans
			time.Sleep(tickerInterval + 10*time.Millisecond) // instant: advances past ticker
			assert.Eventually(func() bool {
				logs := tp.Logs()
				return slices.Contains(logs, fmt.Sprintf("%s%d abandoned spans:", warnPrefix, 5)) && assert.Contains(logs, sb.String())
			}, 2*time.Second, tickerInterval/10)
		})
	})

	t.Run("stop", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bubbleNow := time.Now()
			assert := assert.New(t)
			tp.Reset()
			tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(100*time.Millisecond), withNopInfoHTTPClient(), withNoopStats())
			assert.Nil(err)
			var sb strings.Builder
			sb.WriteString(warnPrefix)

			for i := range 5 {
				s := tracer.StartSpan(fmt.Sprintf("operation%d", i), StartTime(bubbleNow.Add(-10*time.Minute)))
				sb.WriteString(formatSpanString(s))
			}
			assertProcessedSpans(assert, tracer, 5, 0, tickerInterval/10)
			stop()
			assert.Contains(tp.Logs(), fmt.Sprintf("%s%d abandoned spans:", warnPrefix, 5))
			assert.Contains(tp.Logs(), sb.String())
		})
	})

	t.Run("wait", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bubbleNow := time.Now()
			assert := assert.New(t)
			tp.Reset()
			tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond), withNopInfoHTTPClient(), withNoopStats())
			assert.Nil(err)
			defer stop()

			s := tracer.StartSpan("operation", StartTime(bubbleNow.Add(-10*time.Minute)))
			expected := fmt.Sprintf("%s%s", warnPrefix, formatSpanString(s))

			assert.NotContains(tp.Logs(), expected)
			assertProcessedSpans(assert, tracer, 1, 0, tickerInterval/10)
			assert.Eventually(func() bool {
				return slices.Contains(tp.Logs(), expected)
			}, 2*time.Second, tickerInterval/10)
			s.Finish()
		})
	})

	t.Run("truncate", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bubbleNow := time.Now()
			assert := assert.New(t)
			tp.Reset()
			tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithDebugSpansMode(500*time.Millisecond), withNopInfoHTTPClient(), withNoopStats())
			assert.Nil(err)
			// Forget to revert this global variable will lead to broken tests if run multiples times through `-count`.
			logSize = 10
			defer func() {
				logSize = 9000
			}()

			s := tracer.StartSpan("operation", StartTime(bubbleNow.Add(-10*time.Minute)))
			msg := formatSpanString(s)
			assertProcessedSpans(assert, tracer, 1, 0, tickerInterval/10)
			stop()
			assert.NotContains(tp.Logs(), msg)
			assert.Contains(tp.Logs(), fmt.Sprintf("%sToo many abandoned spans. Truncating message.", warnPrefix))
		})
	})
}

func TestDebugAbandonedSpansOff(t *testing.T) {
	tp := new(log.RecordLogger)
	tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp))
	assert.Nil(t, err)
	defer stop()

	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		assert.False(tracer.config.internalConfig.DebugAbandonedSpans())
		assert.Equal(10*time.Minute, tracer.config.internalConfig.SpanTimeout())
		expected := "Abandoned spans logs enabled."
		s := tracer.StartSpan("operation", StartTime(spanStartTime))
		time.Sleep(100 * time.Millisecond)
		assert.NotContains(tp.Logs(), expected)
		s.Finish()
	})
}
