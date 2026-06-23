// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestGLSDeactivateConcurrentFinishRaces is a regression guard for the
// span-pool × GLS concurrency review (orchestrion#782). Two dyngo
// FinishOperation calls run under the shared RLock and can therefore call
// GLSDeactivate on the same popper field at the same time. The popper lives
// in an atomic [GLSPopperCell], so this must stay clean under `go test -race`.
func TestGLSDeactivateConcurrentFinishRaces(t *testing.T) {
	const iterations = 1000

	for range iterations {
		var done GLSDoneCell
		cell := new(atomic.Bool)
		done.ptr.Store(cell)
		var pop GLSPopperCell
		fn := GLSPopper(func() {})
		pop.ptr.Store(&fn)

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		for range 2 {
			go func() {
				defer wg.Done()
				<-start
				GLSDeactivate(&done, &pop)
			}()
		}

		close(start)
		wg.Wait()
	}
}

// TestGLSDeactivateConcurrentDoubleRunsPopper asserts that across two concurrent
// deactivations of the same cell, the popper runs exactly once.
func TestGLSDeactivateConcurrentDoubleRunsPopper(t *testing.T) {
	const iterations = 1000

	var total atomic.Int64
	for range iterations {
		var done GLSDoneCell
		cell := new(atomic.Bool)
		done.ptr.Store(cell)
		var pop GLSPopperCell
		fn := GLSPopper(func() { total.Add(1) })
		pop.ptr.Store(&fn)

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		for range 2 {
			go func() {
				defer wg.Done()
				<-start
				GLSDeactivate(&done, &pop)
			}()
		}

		close(start)
		wg.Wait()
	}

	if got := total.Load(); got != iterations {
		t.Fatalf("GLSDeactivate ran popper %d times across %d concurrent deactivations; want exactly once per iteration", got, iterations)
	}
}
