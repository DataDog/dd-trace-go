// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

// Command gls-leak reproduces the orchestrion#782 goroutine-local-storage (GLS)
// span leak end-to-end, with no contrib involved. It is the in-repo form of the
// external korECM repro (github.com/korECM/dd-trace-go-leak).
//
// ContextWithSpan pushes the active span onto the calling goroutine's GLS stack,
// and Span.Finish pops the stack of whichever goroutine runs it. Push on one
// goroutine and finish on another and the push is never popped — one span leaks
// per call — unless the reclaim fix drops finished entries on the next push.
//
//	# baseline: GLS off, nothing leaks
//	go run ./gls-leak -n 200000
//
//	# leak path: orchestrion turns the GLS on (fixed build stays flat)
//	orchestrion go run ./gls-leak -n 200000
//
// The companion TestGLSNoHeapLeak in this package runs the same workload under
// the orchestrion CI lane and fails if the per-record retention regresses.
package main

import (
	"context"
	"flag"
	"fmt"
	"runtime"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// leakResult is the retained-heap measurement of a workload run.
type leakResult struct {
	records   int
	objects   int64
	bytes     int64
	perRecord float64
}

// measureLeak runs the cross-goroutine push/finish workload n times (twice: once
// to warm up, once measured) and returns the heap objects retained across the
// measured run — the GLS-leak signal. The owner goroutine creates and finishes
// each span (the pop runs there); the worker (caller goroutine) re-injects the
// span via ContextWithSpan, pushing onto the worker's GLS stack — a push whose
// matching pop ran elsewhere. With the reclaim fix the worker's stack (and the
// live heap) stays bounded; without it, one span leaks per record.
//
// The tracer must already be started by the caller.
func measureLeak(n int) leakResult {
	base := context.Background()

	run := func() {
		spanCh := make(chan *tracer.Span, 1024)
		var wg sync.WaitGroup
		wg.Go(func() {
			defer close(spanCh)
			for range n {
				s := tracer.StartSpan("kafka.consume")
				spanCh <- s
				s.Finish() // pop runs here, on the owner goroutine
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
	bytes := int64(after.HeapInuse) - int64(before.HeapInuse)
	return leakResult{
		records:   n,
		objects:   objects,
		bytes:     bytes,
		perRecord: float64(objects) / float64(n),
	}
}

func main() {
	n := flag.Int("n", 200_000, "number of records to simulate")
	flag.Parse()

	if err := tracer.Start(tracer.WithLogStartup(false)); err != nil {
		panic(err)
	}
	defer tracer.Stop()

	r := measureLeak(*n)
	fmt.Printf("records simulated     : %d\n", r.records)
	fmt.Printf("retained heap objects : %+d  (%.3f per record)\n", r.objects, r.perRecord)
	fmt.Printf("retained heap bytes   : %+d  (%.1f KiB)\n", r.bytes, float64(r.bytes)/1024)
	fmt.Println()
	fmt.Println("Interpretation:")
	fmt.Println("  plain `go run .`        -> ~0 per record (GLS disabled, nothing leaks)")
	fmt.Println("  orchestrion + fix       -> ~0 per record (finished entries reclaimed on push)")
	fmt.Println("  orchestrion, no reclaim -> ~15 per record (GLS push never popped)")
}
