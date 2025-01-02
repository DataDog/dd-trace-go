// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"slices"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	globalinternal "gopkg.in/DataDog/dd-trace-go.v1/internal"

	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/statsdtest"
)

func withStatsdClient(s globalinternal.StatsdClient) StartOption {
	return func(c *config) {
		c.statsdClient = s
	}
}

func TestReportRuntimeMetrics(t *testing.T) {
	var tg statsdtest.TestStatsdClient
	trc := newUnstartedTracer(withStatsdClient(&tg))
	defer trc.statsd.Close()

	trc.wg.Add(1)
	go func() {
		defer trc.wg.Done()
		trc.reportRuntimeMetrics(time.Millisecond)
	}()
	assert := assert.New(t)
	err := tg.Wait(assert, 35, 1*time.Second)
	close(trc.stop)
	assert.NoError(err)
	calls := tg.CallNames()
	assert.True(len(calls) > 30)
	assert.Contains(calls, "runtime.go.num_cpu")
	assert.Contains(calls, "runtime.go.mem_stats.alloc")
	assert.Contains(calls, "runtime.go.gc_stats.pause_quantiles.75p")
}

func TestReportHealthMetricsAtInterval(t *testing.T) {
	assert := assert.New(t)
	var tg statsdtest.TestStatsdClient

	defer func(old time.Duration) { statsInterval = old }(statsInterval)
	statsInterval = time.Nanosecond

	tracer, _, flush, stop := startTestTracer(t, withStatsdClient(&tg))
	defer stop()

	tracer.StartSpan("operation").Finish()
	flush(1)
	tg.Wait(assert, 4, 10*time.Second)

	counts := tg.Counts()
	assert.GreaterOrEqual(counts["datadog.tracer.spans_started"], int64(1))
	assert.GreaterOrEqual(counts["datadog.tracer.spans_finished"], int64(1))
	assert.Equal(int64(0), counts["datadog.tracer.traces_dropped"])
	assert.Equal(int64(1), counts["datadog.tracer.queue.enqueued.traces"])
}

func TestEnqueuedTracesHealthMetric(t *testing.T) {
	assert := assert.New(t)
	var tg statsdtest.TestStatsdClient

	defer func(old time.Duration) { statsInterval = old }(statsInterval)
	statsInterval = time.Nanosecond

	tracer, _, flush, stop := startTestTracer(t, withStatsdClient(&tg))
	defer stop()

	for i := 0; i < 3; i++ {
		tracer.StartSpan("operation").Finish()
	}
	flush(3)
	tg.Wait(assert, 1, 10*time.Second)

	counts := tg.Counts()
	assert.Equal(int64(3), counts["datadog.tracer.queue.enqueued.traces"])
	w, ok := tracer.traceWriter.(*agentTraceWriter)
	assert.True(ok)
	assert.Equal(uint32(0), w.tracesQueued)
}

func TestSpansStartedTags(t *testing.T) {
	var tg statsdtest.TestStatsdClient

	defer func(old time.Duration) { statsInterval = old }(statsInterval)
	statsInterval = time.Millisecond

	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, withStatsdClient(&tg))
		defer stop()

		tracer.StartSpan("operation").Finish()
		tg.Wait(assert, 1, 100*time.Millisecond)

		counts := tg.Counts()
		assert.GreaterOrEqual(counts["datadog.tracer.spans_started"], int64(1))
		for _, c := range tg.CountCalls() {
			if c.Name() != "datadog.tracer.spans_started" {
				continue
			}
			if slices.Equal(c.Tags(), []string{"integration:manual"}) {
				return
			}
		}
		assert.Fail("expected integration:manual tag in spans_started")
	})

	t.Run("other_source", func(t *testing.T) {
		tg.Reset()
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, withStatsdClient(&tg))
		defer stop()

		sp := tracer.StartSpan("operation", Tag(ext.Component, "contrib"))
		defer sp.Finish()

		tg.Wait(assert, 1, 100*time.Millisecond)

		counts := tg.Counts()
		assert.GreaterOrEqual(counts["datadog.tracer.spans_started"], int64(1))
		for _, c := range tg.CountCalls() {
			if c.Name() != "datadog.tracer.spans_started" {
				continue
			}
			if slices.Equal(c.Tags(), []string{"integration:contrib"}) {
				return
			}
		}
		assert.Fail("expected integration:contrib tag in spans_started")

	})
}

func TestSpansFinishedTags(t *testing.T) {
	var tg statsdtest.TestStatsdClient

	defer func(old time.Duration) { statsInterval = old }(statsInterval)
	statsInterval = time.Millisecond

	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, withStatsdClient(&tg))
		defer stop()

		tracer.StartSpan("operation").Finish()
		tg.Wait(assert, 1, 100*time.Millisecond)

		counts := tg.Counts()
		assert.GreaterOrEqual(counts["datadog.tracer.spans_finished"], int64(1))
		for _, c := range tg.CountCalls() {
			if c.Name() != "datadog.tracer.spans_finished" {
				continue
			}
			if slices.Equal(c.Tags(), []string{"integration:manual"}) {
				return
			}
		}
		assert.Fail("expected integration:manual tag in spans_finished")
	})

	t.Run("other_source", func(t *testing.T) {
		tg.Reset()
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, withStatsdClient(&tg))
		defer stop()

		tracer.StartSpan("operation", Tag(ext.Component, "contrib")).Finish()

		tg.Wait(assert, 1, 100*time.Millisecond)

		counts := tg.Counts()
		assert.GreaterOrEqual(counts["datadog.tracer.spans_finished"], int64(1))
		for _, c := range tg.CountCalls() {
			if c.Name() != "datadog.tracer.spans_finished" {
				continue
			}
			if slices.Equal(c.Tags(), []string{"integration:contrib"}) {
				return
			}
		}
		assert.Fail("expected integration:contrib tag in spans_finished")

	})
}

func TestTracerMetrics(t *testing.T) {
	assert := assert.New(t)
	var tg statsdtest.TestStatsdClient
	tracer, _, flush, stop := startTestTracer(t, withStatsdClient(&tg))

	tracer.StartSpan("operation").Finish()
	flush(1)
	tg.Wait(assert, 5, 100*time.Millisecond)

	calls := tg.CallsByName()
	counts := tg.Counts()
	assert.Equal(1, calls["datadog.tracer.started"])
	assert.True(calls["datadog.tracer.flush_triggered"] >= 1)
	assert.Equal(1, calls["datadog.tracer.flush_duration"])
	assert.Equal(1, calls["datadog.tracer.flush_bytes"])
	assert.Equal(1, calls["datadog.tracer.flush_traces"])
	assert.Equal(int64(1), counts["datadog.tracer.flush_traces"])
	assert.False(tg.Closed())

	tracer.StartSpan("operation").Finish()
	stop()
	calls = tg.CallsByName()
	assert.Equal(1, calls["datadog.tracer.stopped"])
	assert.True(tg.Closed())
}
