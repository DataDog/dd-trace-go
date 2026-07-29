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
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"
)

// Result is the retained-heap measurement of a MeasureLeak run.
//
// Depth is only populated by [MeasureLeakLiveInject], which keeps its worker
// goroutine alive across the measurement and can therefore ask it how many GLS
// entries it is still holding. It is the direct reading; Objects and Bytes are
// indirect and, under the span pool, weak. See MaxRetainedEntries.
type Result struct {
	Records        int
	Objects        int64
	Bytes          int64
	PerRecord      float64
	BytesPerRecord float64
	Depth          int
}

// MaxRetainedEntries is the ceiling on GLS entries a worker may still hold after
// a [MeasureLeakLiveInject] run. Reclaim is lazy by design: an entry is dropped
// by the next Push under the same key, so the last record's entry can legitimately
// still be there, and the trailing run is bounded by one. A leak instead grows
// with the record count.
const MaxRetainedEntries = 8

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

// MeasureLeakLiveInject is MeasureLeak with the two steps swapped: the span is
// injected while still LIVE and finished only after the worker has pushed it.
//
// That order is what makes it usable with the experimental span pool. MeasureLeak
// finishes first and hands the worker an already-finished span, which is a
// deliberate use-after-Finish — legitimate for the leak measurement, but outside
// the pool's contract, since the pool may have recycled the object by then. This
// variant touches the span only while it is live, and matches the real franz-go
// shape more closely anyway: a span goes into the context in flight and is
// finished afterwards.
//
// It is the workload for asserting that the pool and orchestrion GLS coexist. The
// entry captures its liveness cell at push time while the span is live, Finish
// marks the cell, and the worker's next push drains the entry — even though the
// span object itself has since gone back to the pool and been handed to unrelated
// work.
//
// A per-item handshake orders the worker's push ahead of the owner's Finish, so
// the worker never reads a span's fields while the owner finishes it or the pool
// recycles it: the workload stays data-race-free under -race while still
// exercising pool reuse and cross-goroutine reclaim.
//
// The tracer must already be started by the caller, with the span pool enabled
// (tracer.WithSpanPool(true)) for this to measure anything the plain
// MeasureLeak gate does not. n <= 0 returns a zero Result.
func MeasureLeakLiveInject(n int) Result {
	if n <= 0 {
		return Result{Records: n}
	}
	base := context.Background()

	// The worker has to outlive the measurement, which is why it is started once
	// here rather than per run. Orchestrion weaves `getg().__dd_gls_v2 = nil`
	// into runtime.goexit1, so a worker that has already returned has no GLS
	// stack left to look at: every entry it accumulated becomes unreachable the
	// moment it exits, and the retained-heap delta reads ~0 however broken
	// reclaim is. Parking it on the channel until after ReadMemStats is what
	// gives this gate teeth — an earlier version waited for the worker first and
	// passed with reclaim entirely disabled.
	spanCh := make(chan *tracer.Span)
	pushedCh := make(chan struct{})
	depthCh := make(chan int)
	var wg sync.WaitGroup
	wg.Go(func() { // worker: pushes the live span onto its own GLS stack
		for s := range spanCh {
			if s == nil { // depth probe: report this goroutine's own GLS depth
				depthCh <- orchestrion.GLSStackDepth()
				continue
			}
			_ = tracer.ContextWithSpan(base, s)
			pushedCh <- struct{}{} // done reading s; the owner may finish it
		}
	})

	inject := func() {
		for range n { // owner: creates and, once the worker pushed, finishes the span
			s := tracer.StartSpan("kafka.consume")
			spanCh <- s // hand the LIVE span to the worker
			<-pushedCh  // wait until the worker pushed it (orders push before finish)
			s.Finish()  // finish on the owner; the worker's popper is a no-op here
		}
	}

	inject() // warm up so first-run/lazy allocations don't count toward the delta

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	inject()

	tracer.Flush() // drop buffered spans so only a GLS leak can retain them
	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after) // worker still parked on spanCh, its GLS intact

	// Ask the worker for its own stack depth before letting it exit. This is the
	// assertion with no measurement noise in it: retained bytes and objects are
	// both indirect, and under pooling they are especially weak, because stale
	// entries sit inline in one geometrically grown slice and point at a small
	// set of recycled spans — so object count barely moves while the real leak is
	// linear. Depth counts the entries themselves.
	spanCh <- nil
	depth := <-depthCh

	close(spanCh)
	wg.Wait()

	objects := int64(after.HeapObjects) - int64(before.HeapObjects)
	bytes := int64(after.HeapInuse) - int64(before.HeapInuse)
	return Result{
		Records:        n,
		Objects:        objects,
		Bytes:          bytes,
		PerRecord:      float64(objects) / float64(n),
		BytesPerRecord: float64(bytes) / float64(n),
		Depth:          depth,
	}
}
