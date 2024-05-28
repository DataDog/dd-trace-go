// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"testing"
	"time"

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

func TestReportHealthMetrics(t *testing.T) {
	assert := assert.New(t)
	var tg statsdtest.TestStatsdClient

	defer func(old time.Duration) { statsInterval = old }(statsInterval)
	statsInterval = time.Nanosecond

	tracer, _, flush, stop := startTestTracer(t, withStatsdClient(&tg))
	defer stop()

	tracer.StartSpan("operation").Finish()
	flush(1)
	tg.Wait(assert, 3, 10*time.Second)

	counts := tg.Counts()
	assert.Equal(int64(1), counts["datadog.tracer.spans_started"])
	assert.Equal(int64(1), counts["datadog.tracer.spans_finished"])
	assert.Equal(int64(0), counts["datadog.tracer.traces_dropped"])
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
