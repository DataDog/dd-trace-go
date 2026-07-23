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

// TestGLSFinishResetConcurrentRaces is a regression guard for the span-pool ×
// GLS concurrency review (orchestrion#782). A span can be finished on one
// goroutine (GLSDeactivate, woven into Span.Finish) while the tracer's
// sync.Pool recycles and resets it on another (GLSReset, woven into
// Span.clear). All accesses to the shared fields go through atomic operations
// (GLSPopperCell.ptr, GLSDoneCell.ptr), so this must stay clean under
// `go test -race`.
func TestGLSFinishResetConcurrentRaces(t *testing.T) {
	for range 1000 {
		var done GLSDoneCell
		cell := new(atomic.Bool)
		done.ptr.Store(cell)
		var pop GLSPopperCell
		fn := GLSPopper(func() {})
		pop.ptr.Store(&fn)

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			<-start
			GLSDeactivate(&done, &pop)
		}()

		go func() {
			defer wg.Done()
			<-start
			GLSReset(&done, &pop)
		}()

		close(start)
		wg.Wait()
	}
}
