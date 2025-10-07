// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	globalinternal "github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/statsdtest"
)

func withStatsdClient(s globalinternal.StatsdClient) StartOption {
	return func(c *config) {
		c.statsdClient = s
	}
}

func TestReportRuntimeMetrics(t *testing.T) {
	var tg statsdtest.TestStatsdClient
	trc, err := newUnstartedTracer(withStatsdClient(&tg))
	assert.NoError(t, err)
	defer trc.statsd.Close()

	trc.wg.Add(1)
	go func() {
		defer trc.wg.Done()
		trc.reportRuntimeMetrics(time.Millisecond)
	}()
	assert := assert.New(t)
	err = tg.Wait(assert, 35, 1*time.Second)
	trc.Stop()
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
	statsInterval = time.Millisecond

	tracer, _, flush, stop, err := startTestTracer(t, withStatsdClient(&tg))
	assert.Nil(err)
	defer stop()

	tracer.StartSpan("operation").Finish()
	flush(1)
	tg.Wait(assert, 4, 10*time.Second)

	counts := tg.Counts()
	assert.Equal(int64(1), counts["datadog.tracer.spans_started"])
	assert.Equal(int64(1), counts["datadog.tracer.spans_finished"])
	assert.Equal(int64(0), counts["datadog.tracer.traces_dropped"])
	assert.Equal(int64(1), counts["datadog.tracer.queue.enqueued.traces"])
}

func TestEnqueuedTracesHealthMetric(t *testing.T) {
	assert := assert.New(t)
	var tg statsdtest.TestStatsdClient

	defer func(old time.Duration) { statsInterval = old }(statsInterval)
	statsInterval = time.Nanosecond

	tracer, _, flush, stop, err := startTestTracer(t, withStatsdClient(&tg))
	assert.Nil(err)
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
		tracer, _, _, stop, err := startTestTracer(t, withStatsdClient(&tg))
		assert.Nil(err)
		defer stop()

		tracer.StartSpan("operation").Finish()
		tg.Wait(assert, 1, 100*time.Millisecond)
		assertSpanMetricCountsAreZero(t, tracer.spansStarted)

		counts := tg.Counts()
		assert.Equal(counts["datadog.tracer.spans_started"], int64(1))
		for _, c := range statsdtest.FilterCallsByName(tg.CountCalls(), "datadog.tracer.spans_started") {
			assert.Equal([]string{"integration:manual"}, c.Tags())
		}
	})

	t.Run("custom_integration", func(t *testing.T) {
		tg.Reset()
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, withStatsdClient(&tg))
		assert.Nil(err)
		defer stop()

		sp := tracer.StartSpan("operation", Tag(ext.Component, "contrib"))
		defer sp.Finish()

		tg.Wait(assert, 1, 100*time.Millisecond)
		assertSpanMetricCountsAreZero(t, tracer.spansStarted)

		counts := tg.Counts()
		assert.Equal(counts["datadog.tracer.spans_started"], int64(1))
		for _, c := range statsdtest.FilterCallsByName(tg.CountCalls(), "datadog.tracer.spans_started") {
			assert.Equal([]string{"integration:contrib"}, c.Tags())
		}
	})
}

func TestSpansFinishedTags(t *testing.T) {
	var tg statsdtest.TestStatsdClient

	defer func(old time.Duration) { statsInterval = old }(statsInterval)
	statsInterval = time.Millisecond

	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, withStatsdClient(&tg))
		assert.Nil(err)
		defer stop()

		tracer.StartSpan("operation").Finish()
		tg.Wait(assert, 1, 100*time.Millisecond)
		assertSpanMetricCountsAreZero(t, tracer.spansFinished)

		counts := tg.Counts()
		assert.Equal(counts["datadog.tracer.spans_finished"], int64(1))
		for _, c := range statsdtest.FilterCallsByName(tg.CountCalls(), "datadog.tracer.spans_finished") {
			assert.Equal([]string{"integration:manual"}, c.Tags())
		}
	})

	t.Run("custom_integration", func(t *testing.T) {
		tg.Reset()
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, withStatsdClient(&tg))
		assert.Nil(err)
		defer stop()

		tracer.StartSpan("operation", Tag(ext.Component, "contrib")).Finish()

		tg.Wait(assert, 1, 100*time.Millisecond)
		assertSpanMetricCountsAreZero(t, tracer.spansFinished)

		counts := tg.Counts()
		assert.Equal(counts["datadog.tracer.spans_finished"], int64(1))
		for _, c := range statsdtest.FilterCallsByName(tg.CountCalls(), "datadog.tracer.spans_finished") {
			assert.Equal([]string{"integration:contrib"}, c.Tags())
		}
	})
}

func TestMultipleSpanIntegrationTags(t *testing.T) {
	var tg statsdtest.TestStatsdClient
	tg.Reset()

	defer func(old time.Duration) { statsInterval = old }(statsInterval)
	statsInterval = time.Millisecond

	assert := assert.New(t)
	tracer, _, flush, stop, err := startTestTracer(t, withStatsdClient(&tg))
	assert.Nil(err)
	defer stop()

	// integration:manual
	for range 5 {
		tracer.StartSpan("operation").Finish()
	}

	// integration:net/http
	for range 3 {
		tracer.StartSpan("operation", Tag(ext.Component, "net/http")).Finish()
	}

	// integration:contrib
	for range 2 {
		tracer.StartSpan("operation", Tag(ext.Component, "contrib")).Finish()
	}
	flush(10)
	// Wait until the specific counts we care about have all been reported to avoid flakiness
	assert.Eventually(func() bool {
		counts := tg.Counts()
		return counts["datadog.tracer.spans_started"] == 10 && counts["datadog.tracer.spans_finished"] == 10
	}, 1*time.Second, 10*time.Millisecond)

	counts := tg.Counts()
	assert.Equal(int64(10), counts["datadog.tracer.spans_started"])
	assert.Equal(int64(10), counts["datadog.tracer.spans_finished"])

	assertSpanMetricCountsAreZero(t, tracer.spansStarted)
	assertSpanMetricCountsAreZero(t, tracer.spansFinished)

	startCalls := statsdtest.FilterCallsByName(tg.CountCalls(), "datadog.tracer.spans_started")
	assert.Equal(int64(5), tg.CountCallsByTag(startCalls, "integration:manual"))
	assert.Equal(int64(3), tg.CountCallsByTag(startCalls, "integration:net/http"))
	assert.Equal(int64(2), tg.CountCallsByTag(startCalls, "integration:contrib"))

	finishedCalls := statsdtest.FilterCallsByName(tg.CountCalls(), "datadog.tracer.spans_finished")
	assert.Equal(int64(5), tg.CountCallsByTag(finishedCalls, "integration:manual"))
	assert.Equal(int64(3), tg.CountCallsByTag(finishedCalls, "integration:net/http"))
	assert.Equal(int64(2), tg.CountCallsByTag(finishedCalls, "integration:contrib"))

}

func TestHealthMetricsRaceCondition(t *testing.T) {
	assert := assert.New(t)

	defer func(old time.Duration) { statsInterval = old }(statsInterval)
	statsInterval = time.Millisecond

	var tg statsdtest.TestStatsdClient
	tracer, _, flush, stop, err := startTestTracer(t, withStatsdClient(&tg))
	assert.Nil(err)
	defer stop()

	wg := sync.WaitGroup{}
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sp := tracer.StartSpan("operation")
			sp.Finish()
		}()
	}
	time.Sleep(150 * time.Millisecond)
	flush(5)
	tg.Wait(assert, 10, 100*time.Millisecond)
	wg.Wait()

	counts := tg.Counts()
	assert.Equal(int64(5), counts["datadog.tracer.spans_started"])
	assert.Equal(int64(5), counts["datadog.tracer.spans_finished"])

	assertSpanMetricCountsAreZero(t, tracer.spansStarted)
	assertSpanMetricCountsAreZero(t, tracer.spansFinished)
}

func TestTracerMetrics(t *testing.T) {
	assert := assert.New(t)
	var tg statsdtest.TestStatsdClient
	tracer, _, flush, stop, err := startTestTracer(t, withStatsdClient(&tg))
	assert.Nil(err)

	tracer.StartSpan("operation").Finish()
	flush(1)
	tg.Wait(assert, 5, 500*time.Millisecond)

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

func BenchmarkSpansMetrics(b *testing.B) {
	defer func(old time.Duration) { statsInterval = old }(statsInterval)
	statsInterval = time.Millisecond

	var tg statsdtest.TestStatsdClient
	tracer, _, _, stop, err := startTestTracer(b, withStatsdClient(&tg))
	assert.Nil(b, err)
	defer stop()
	for n := 0; n < b.N; n++ {
		for range n {
			go tracer.StartSpan("operation").Finish()
		}
	}
}

func assertSpanMetricCountsAreZero(t *testing.T, metric globalinternal.XSyncMapCounterMap) {
	for _, v := range metric.GetAndReset() {
		assert.Equal(t, int64(0), v)
	}
}
