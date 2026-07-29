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
// Span.clear), both touching the same popper and liveness fields. Both live in
// atomic cells — [GLSPopperCell] and [GLSDoneCell] — so the Swap (finish) and
// Store (reset) are synchronized and this must stay clean under `go test -race`;
// before that, the bare func field was unsynchronized memory and the race
// detector flagged it.
//
// The done cell adds a second thing to get right here: reset must not leave
// finish dereferencing a pointer it already cleared, which is why the
// finish-before-activate path in GLSDeactivate re-checks its load.
func TestGLSFinishResetConcurrentRaces(t *testing.T) {
	for range 1000 {
		var done GLSDoneCell
		var pop GLSPopperCell
		fn := GLSPopper(func() {})
		pop.ptr.Store(&glsExit{pop: fn})

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

// TestGLSActivateFinishResetConcurrentRaces runs all three lifecycle helpers on
// one span at once — the shape the span pool creates, where the owner finishes
// while a worker activates and the tracer recycles. Nothing else in the suite
// exercises GLSActivate concurrently with the other two, so this covers the
// remaining unsynchronised-access surface on both woven fields under -race.
//
// It also pins the invariant that makes the retry in GLSActivate necessary: an
// activation given a non-nil GLSDoneCell must never push an entry without a
// liveness cell. entry.isDone treats a nil cell as permanently live, so such an
// entry is never drained by Push and never skipped by Peek — a finished,
// recycled span would stay on this stack and get handed out as the parent of
// unrelated work, which is the trace merge this whole line of work exists to
// stop. Reaching it needs a GLSReset to land between GLSActivate's failed
// CompareAndSwap and its following load, so the counter below is the guard: if
// scheduling ever hits that window, this fails instead of silently producing an
// immortal entry.
//
// Scope, stated plainly: the interleaving is not forced, so a clean run is not
// proof that the window cannot be hit — only that it was not hit here. The same
// caveat applies to the checked load on GLSDeactivate's create-a-marked-cell
// path, where removing the check does not fail this test. Both stay because the
// alternatives are an immortal GLS entry and a nil *atomic.Bool dereference
// inside Span.Finish, and neither is worth trading for three lines.
func TestGLSActivateFinishResetConcurrentRaces(t *testing.T) {
	t.Cleanup(MockGLSPerGoroutine())

	var nilCells atomic.Int64

	for range 2000 {
		var done GLSDoneCell
		var pop GLSPopperCell

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(3)

		go func() {
			defer wg.Done()
			<-start
			GLSActivate(nil, key("race"), "v", &pop, &done)
			// Read this goroutine's own stack: MockGLSPerGoroutine gives the
			// activation a private one, and the entry was pushed here.
			if s := getDDContextStack(); s != nil {
				for _, e := range s.stacks[key("race")] {
					if e.done == nil {
						nilCells.Add(1)
					}
				}
			}
		}()

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

	if n := nilCells.Load(); n != 0 {
		t.Errorf("GLSActivate pushed %d entries with no liveness cell, want 0: such an entry is "+
			"never drained by Push and never skipped by Peek, so a finished and recycled span "+
			"stays live on this stack and becomes the parent of unrelated work", n)
	}
}
