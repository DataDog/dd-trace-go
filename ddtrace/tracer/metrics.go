// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"runtime"
	"runtime/debug"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/internal/tracerstats"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// defaultMetricsReportInterval specifies the interval at which runtime metrics will
// be reported.
const defaultMetricsReportInterval = 10 * time.Second

// reportRuntimeMetrics periodically reports go runtime metrics at
// the given interval.
func (t *tracer) reportRuntimeMetrics(interval time.Duration) {
	var ms runtime.MemStats
	gc := debug.GCStats{
		// When len(stats.PauseQuantiles) is 5, it will be filled with the
		// minimum, 25%, 50%, 75%, and maximum pause times. See the documentation
		// for (runtime/debug).ReadGCStats.
		PauseQuantiles: make([]time.Duration, 5),
	}

	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			log.Debug("Reporting runtime metrics...")
			runtime.ReadMemStats(&ms)
			debug.ReadGCStats(&gc)

			statsd := t.statsd
			// CPU statistics
			statsd.Gauge("runtime.go.num_cpu", float64(runtime.NumCPU()), nil, 1)
			statsd.Gauge("runtime.go.num_goroutine", float64(runtime.NumGoroutine()), nil, 1)
			statsd.Gauge("runtime.go.num_cgo_call", float64(runtime.NumCgoCall()), nil, 1)
			// General statistics
			statsd.Gauge("runtime.go.mem_stats.alloc", float64(ms.Alloc), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.total_alloc", float64(ms.TotalAlloc), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.sys", float64(ms.Sys), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.lookups", float64(ms.Lookups), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.mallocs", float64(ms.Mallocs), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.frees", float64(ms.Frees), nil, 1)
			// Heap memory statistics
			statsd.Gauge("runtime.go.mem_stats.heap_alloc", float64(ms.HeapAlloc), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.heap_sys", float64(ms.HeapSys), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.heap_idle", float64(ms.HeapIdle), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.heap_inuse", float64(ms.HeapInuse), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.heap_released", float64(ms.HeapReleased), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.heap_objects", float64(ms.HeapObjects), nil, 1)
			// Stack memory statistics
			statsd.Gauge("runtime.go.mem_stats.stack_inuse", float64(ms.StackInuse), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.stack_sys", float64(ms.StackSys), nil, 1)
			// Off-heap memory statistics
			statsd.Gauge("runtime.go.mem_stats.m_span_inuse", float64(ms.MSpanInuse), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.m_span_sys", float64(ms.MSpanSys), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.m_cache_inuse", float64(ms.MCacheInuse), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.m_cache_sys", float64(ms.MCacheSys), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.buck_hash_sys", float64(ms.BuckHashSys), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.gc_sys", float64(ms.GCSys), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.other_sys", float64(ms.OtherSys), nil, 1)
			// Garbage collector statistics
			statsd.Gauge("runtime.go.mem_stats.next_gc", float64(ms.NextGC), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.last_gc", float64(ms.LastGC), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.pause_total_ns", float64(ms.PauseTotalNs), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.num_gc", float64(ms.NumGC), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.num_forced_gc", float64(ms.NumForcedGC), nil, 1)
			statsd.Gauge("runtime.go.mem_stats.gc_cpu_fraction", ms.GCCPUFraction, nil, 1)
			for i, p := range []string{"min", "25p", "50p", "75p", "max"} {
				statsd.Gauge("runtime.go.gc_stats.pause_quantiles."+p, float64(gc.PauseQuantiles[i]), nil, 1)
			}

		case <-t.stop:
			return
		}
	}
}

// reportHealthMetricsAtInterval reports noisy health metrics at the specified interval.
// The periodic reporting ensures metrics are delivered without overwhelming the system or logs.
func (t *tracer) reportHealthMetricsAtInterval(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			// if there are started spans, report the number of spans with their integration, then
			// reset the count
			// the Count() function reports the total number of event occurrences in one time interval. We reset
			// our count to 0 regardless of if Count succeeded to cleanup before the next interval.

			for k, v := range t.spansStarted.GetAndReset() {
				t.statsd.Count("datadog.tracer.spans_started", v, []string{"integration:" + k}, 1)
			}

			// if there are finished spans, report the number of spans with their integration, then
			// reset the count
			// the Count() function reports the total number of event occurrences in one time interval. We reset
			// our count to 0 regardless of if Count succeeded to cleanup before the next interval.
			for k, v := range t.spansFinished.GetAndReset() {
				t.statsd.Count("datadog.tracer.spans_finished", v, []string{"integration:" + k}, 1)
			}

			t.statsd.Count("datadog.tracer.traces_dropped", int64(tracerstats.Count(tracerstats.TracesDropped)), []string{"reason:trace_too_large"}, 1)
		case <-t.stop:
			return
		}
	}
}
