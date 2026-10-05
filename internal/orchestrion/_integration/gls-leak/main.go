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
// per call — unless the reclaim fix drops finished entries on the next push. The
// measurement itself lives in the shared glsleak package so the runnable command
// and the _integration/gls regression test stay in lockstep.
//
//	# baseline: GLS off, nothing leaks
//	go run ./gls-leak -n 200000
//
//	# leak path: orchestrion turns the GLS on (fixed build stays flat)
//	orchestrion go run ./gls-leak -n 200000
//
// Expected retained heap per record:
//
//	plain go run .          ~0          GLS disabled, nothing leaks
//	orchestrion + reclaim   ~0          finished entries reclaimed on the next push
//	orchestrion, no reclaim one span    GLS push never popped — grows with N (orchestrion#782)
//
// The command only reports the measurement; the regression gate is the companion
// TestGLSNoHeapLeak, which runs the same workload under the orchestrion CI lane
// and fails if the per-record retention regresses.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/glsleak"
)

func main() {
	n := flag.Int("n", 200_000, "number of records to simulate (must be > 0)")
	flag.Parse()
	if *n <= 0 {
		fmt.Fprintln(os.Stderr, "gls-leak: -n must be > 0")
		os.Exit(2)
	}

	if err := tracer.Start(tracer.WithLogStartup(false)); err != nil {
		panic(err)
	}
	defer tracer.Stop()

	r := glsleak.MeasureLeak(*n)
	fmt.Printf(`records simulated     : %d
retained heap objects : %+d  (%.3f per record)
retained heap bytes   : %+d  (%.1f KiB)
`, r.Records, r.Objects, r.PerRecord, r.Bytes, float64(r.Bytes)/1024)
}
