// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build deadlock

package tracer

import (
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
)

// TestPartialFlushSpanLockOrderingCycle targets the !finishingSpanIsFirstInChunk
// branch of finishedOneLocked and verifies that the deadlock detector never sees
// an AB/BA Span.mu cycle.
//
// How it works (under -tags deadlock):
//
// Phase 1 creates a 3-span trace (root > childA > childB) and finishes root then
// childA. childA.Finish() crosses the partial-flush threshold: s=childA, fSpan=root.
// The buggy code would hold childA.mu while acquiring root.mu, which linkdata/deadlock
// records as "childA.mu → root.mu" in its global ordering map. childB is left open so
// the worker only releases childA to the pool (root is exempt from mid-trace recycling).
//
// Phase 2 needs to trigger the reverse ordering "root.mu → childA.mu" using the
// same Span instances so the detector can find the existing edge and panic. The
// pool-seeding approach seeds the global spanPool with [childA (private), root
// (shared.head)] so that the next two StartSpan calls return them in swapped roles.
//
// Because sync.Pool gives no ordering guarantees (spanPool is process-global and
// shared with the tracer worker, the Go runtime, and every other span-pool-enabled
// tracer in the binary), seeding is wrapped in a bounded retry: the pool is
// explicitly drained of stale/foreign entries, re-seeded in LIFO order, and the
// returned instances are checked for pointer identity. On mismatch the attempt is
// discarded and the loop retries. If all attempts are exhausted the test skips
// with a diagnostic instead of failing non-deterministically.
//
// The same caveat applies to the final StartSpan acquisition. Single-threaded the
// restore->acquire handoff is deterministic, but spanPool is process-global: any
// other goroutine that performs a spanPool.Get in that window (every StartSpan on
// a span-pool-enabled tracer in the binary does one) steals a seeded instance from
// P0, and the acquiring Get then returns a different span. So the acquired
// instances are likewise checked for identity and the test skips (after cleanup)
// rather than failing if they diverge.
//
// Under the fixed code, s.mu is released before fSpan.mu is acquired, so neither
// direction is ever recorded and the test passes cleanly.
func TestPartialFlushSpanLockOrderingCycle(t *testing.T) {
	t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
	t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "2")

	tracer, transport, flush, stop, err := startTestTracer(t, WithSpanPool(true))
	require.NoError(t, err)
	defer stop()

	// Phase 1: establish ordering "childA.mu → root.mu".
	// Three spans so partial flush fires while childB is still open, letting the
	// worker release only childA to the pool (root is exempt from mid-trace
	// recycling until the full trace is submitted; childB is still open).
	root := tracer.StartSpan("root", Tag(ext.ManualKeep, true))
	childA := tracer.StartSpan("childA", ChildOf(root.Context()))
	childB := tracer.StartSpan("childB", ChildOf(root.Context()))

	root.Finish()
	childA.Finish() // triggers partial flush: s=childA (holds childA.mu), fSpan=root
	// flush(1) waits until the worker has processed the partial-flush chunk and
	// called releaseSpans, so childA is in the pool by the time flush returns.
	// root is excluded from spansToRelease (the tracer keeps the trace-root span
	// alive for the eventual full-trace submission), so it is NOT in the pool yet.
	// childB is still open and has not been submitted at all.
	flush(1)
	transport.Traces()

	// Phase 2: seed the pool with [childA, root] and acquire them back via StartSpan
	// so they play swapped roles in a second partial-flush cycle.
	//
	// GOMAXPROCS=1 pins all Put/Get calls to P0, making the LIFO order deterministic
	// on the fast path.  SetGCPercent(-1) prevents GC from evicting seeded entries.
	// Both are restored as soon as the instances are in hand.
	//
	// The acquisition is wrapped in a bounded retry loop because sync.Pool provides
	// no ordering guarantees. Each iteration:
	//   1. Clears both spans (idempotent; ensures clean state for StartSpan reuse).
	//   2. Drains stale/foreign entries from the pool (private + shared + victim).
	//   3. Seeds: childA → P0.private (Get #1), root → P0.shared.head (Get #2).
	//   4. Calls StartSpan twice and checks pointer identity.
	// On mismatch, the attempt's spans are abandoned and the loop retries.
	// On exhaustion the test skips rather than flake-failing.
	const (
		poolDrainCount = 64 // clears private + shared + victim cache in any realistic scenario
		maxAttempts    = 50
	)

	// Clear both spans before seeding. childA was already cleared by releaseSpans
	// after Phase 1; root was not recycled and must be cleared so that
	// newChildA.Finish() does not exit early on the s.finished guard.
	childA.clear()
	root.clear()

	// Verify pool ordering using raw Get calls — intentionally NOT via
	// tracer.StartSpan — so that failed attempts do not create new Span/trace
	// instances that flood the deadlock detector's lock-order map and cause
	// flush timeouts. On each attempt:
	//   1. Drain stale/foreign entries so P0's private+shared+victim are clear.
	//   2. Seed: childA → P0.private (Get #1), root → P0.shared.head (Get #2).
	//   3. Verify pointers via raw Get without initializing the spans.
	//   4. On success, restore them to the pool so StartSpan can acquire them.
	//   5. On failure, discard and retry. childA/root are kept alive by the
	//      test variables, so discarding the raw Gets is safe.
	numCPU := runtime.GOMAXPROCS(1)
	prevGC := debug.SetGCPercent(-1)
	var seeded bool
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		for i := 0; i < poolDrainCount; i++ {
			spanPool.Get() //nolint:staticcheck // intentional pool drain; return value discarded
		}
		spanPool.Put(childA)
		spanPool.Put(root)
		got1 := spanPool.Get().(*Span) //nolint:staticcheck // raw identity check; span not initialized
		got2 := spanPool.Get().(*Span) //nolint:staticcheck
		if got1 == childA && got2 == root {
			// Restore for StartSpan's Get calls below.
			spanPool.Put(childA)
			spanPool.Put(root)
			seeded = true
			break
		}
		// Wrong order: clear and retry. (got1/got2 are discarded safely.)
		childA.clear()
		root.clear()
	}
	if !seeded {
		debug.SetGCPercent(prevGC)
		runtime.GOMAXPROCS(numCPU)
		t.Skipf("span pool did not return the expected instances in %d attempts; "+
			"skipping deadlock-cycle check", maxAttempts)
	}

	// Acquire the instances via StartSpan while GOMAXPROCS=1 and GC are still
	// active.
	//
	// Single-threaded this handoff is deterministic: the verification above left
	// childA in P0.private and root in P0.shared, so the two StartSpan Gets return
	// them in order. But spanPool is process-global. If any other goroutine runs a
	// spanPool.Get in the window between the restore above and these Gets, it pops
	// childA out of P0.private and the first Get here returns root (or a fresh
	// span) instead — every StartSpan on a span-pool-enabled tracer in the binary
	// does exactly one Get, and GOMAXPROCS=1 does not prevent that goroutine from
	// being scheduled. When the seeded pair does not come back the swapped-role
	// reverse-ordering scenario can no longer be built on the same Span pointers,
	// so we clean up and skip rather than fail non-deterministically — consistent
	// with the seeding-exhaustion skip above. On the cooperative happy path the
	// seeded instances come back and the cycle is exercised.
	newRoot := tracer.StartSpan("newRoot", Tag(ext.ManualKeep, true))
	newChildA := tracer.StartSpan("newChildA", ChildOf(newRoot.Context()))
	debug.SetGCPercent(prevGC)
	runtime.GOMAXPROCS(numCPU)

	if newRoot != childA || newChildA != root {
		// The seeded pair did not come back (another goroutine touched the global
		// pool). Tear down the spans we created plus the still-open Phase 1 span,
		// then skip. Clearing rootFlushed keeps Phase 1's full-trace flush from
		// recycling root a second time (root's fate is already decided by the
		// StartSpan Gets above, exactly as on the happy path below).
		childB.context.trace.mu.Lock()
		childB.context.trace.rootFlushed = false
		childB.context.trace.mu.Unlock()
		newChildA.Finish()
		newRoot.Finish()
		childB.Finish()
		t.Skip("span pool returned unexpected instances after seeding " +
			"(concurrent pool access); skipping deadlock-cycle check")
	}
	newChildB := tracer.StartSpan("newChildB", ChildOf(newRoot.Context()))

	newRoot.Finish()   // childA instance is now finishedSpans[0] = fSpan
	newChildA.Finish() // root instance crosses threshold: s=root, fSpan=childA
	// Under the buggy code the detector fires here: it sees "root.mu → childA.mu"
	// and finds the earlier "childA.mu → root.mu" entry — AB/BA cycle.
	flush(1)
	transport.Traces()

	// Prevent double-put: Phase 1's trace has rootFlushed=true, meaning it will
	// try to recycle root (via spansToRelease) when childB.Finish() completes
	// the trace. But Phase 2's partial flush above already recycled root. Clear
	// rootFlushed so the Phase 1 full-trace flush does not put root back in the
	// pool a second time.
	childB.context.trace.mu.Lock()
	childB.context.trace.rootFlushed = false
	childB.context.trace.mu.Unlock()

	// Cleanup: finish the two leftover spans.
	childB.Finish()
	newChildB.Finish()
	flush(2)
}
