// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

// Package glsleak holds the shared, in-process reproduction of the orchestrion#782
// GLS span leak (the korECM workload). Both the runnable gls-leak command and the
// _integration/gls regression test use MeasureLeak so the measurement methodology
// lives in exactly one place.
package glsleak

import (
	"context"
	"runtime"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// Result is the retained-heap measurement of a MeasureLeak run.
type Result struct {
	Records   int
	Objects   int64
	Bytes     int64
	PerRecord float64
}

// MaxRetainedObjectsPerRecord is the per-record retained-heap-object ceiling the
// GLS-leak gates assert on Result.PerRecord. With the reclaim fix the workload
// retains ~0 objects per record; without it the GLS stack grows by one span per
// record (orchestrion#782), so retention rises in proportion to the record count
// — far above this bound. The threshold only needs to sit between "negligible"
// and "one span's worth of objects", so it is deliberately loose, not tuned.
const MaxRetainedObjectsPerRecord = 1.0

// MeasureLeak runs the cross-goroutine push/finish workload n times (once to warm
// up, once measured) and reports the heap objects retained across the measured
// run — the GLS-leak signal for orchestrion#782. An owner goroutine creates and
// finishes each span; the worker (caller goroutine) re-injects the span via
// ContextWithSpan, pushing onto the worker's GLS stack — a push whose matching pop
// ran elsewhere. With the reclaim fix the worker's stack (and live heap) stays
// bounded; without it, one span leaks per record.
//
// The owner finishes each span BEFORE handing it to the worker, so Finish and the
// worker's ContextWithSpan never touch the span's injected GLS fields concurrently
// (the channel send/receive orders them). The workload keeps the same leak shape
// — a worker push with no matching pop on the worker — while being data-race-free
// under -race.
//
// The tracer must already be started by the caller. n <= 0 returns a zero Result.
func MeasureLeak(n int) Result {
	if n <= 0 {
		return Result{Records: n}
	}
	base := context.Background()

	run := func() {
		spanCh := make(chan *tracer.Span, 1024)
		var wg sync.WaitGroup
		wg.Go(func() {
			defer close(spanCh)
			for range n {
				s := tracer.StartSpan("kafka.consume")
				s.Finish()  // pop runs here, on the owner goroutine
				spanCh <- s // hand the already-finished span to the worker
			}
		})
		for s := range spanCh {
			_ = tracer.ContextWithSpan(base, s) // push runs here, on the worker
		}
		wg.Wait()
	}

	run() // warm up so first-run/lazy allocations don't count toward the delta

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	run()

	tracer.Flush() // drop buffered spans so only a GLS leak can retain them
	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	objects := int64(after.HeapObjects) - int64(before.HeapObjects)
	return Result{
		Records:   n,
		Objects:   objects,
		Bytes:     int64(after.HeapInuse) - int64(before.HeapInuse),
		PerRecord: float64(objects) / float64(n),
	}
}
