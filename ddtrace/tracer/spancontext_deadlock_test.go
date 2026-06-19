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

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/stretchr/testify/require"
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
// the worker only releases root and childA to the pool (LIFO order: root Put first →
// childA on top).
//
// Phase 2 starts immediately after the partial-flush chunk is processed.  The pool
// returns childA first (it was Put last), so the new "root" is the childA instance and
// the new "childA" is the root instance. When new-root (=childA) finishes first and
// new-childA (=root) crosses the threshold, the partial-flush has s=root, fSpan=childA.
// The buggy code would hold root.mu while acquiring childA.mu — the detector checks its
// global map, finds "childA.mu → root.mu" from Phase 1, and panics: AB/BA cycle.
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
	// worker release only root and childA to the pool (not childB).
	root := tracer.StartSpan("root", Tag(ext.ManualKeep, true))
	childA := tracer.StartSpan("childA", ChildOf(root.Context()))
	childB := tracer.StartSpan("childB", ChildOf(root.Context()))

	root.Finish()
	childA.Finish() // triggers partial flush: s=childA (holds childA.mu), fSpan=root
	// flush(1) waits until the worker has processed the partial-flush chunk.
	// The worker calls releaseSpans before the traceWriter.flush() that makes
	// transport.Len()==1, so childA is in the pool by the time flush returns.
	// root is excluded from spansToRelease (the tracer keeps the trace-root span
	// alive for the eventual full-trace submission), so it is NOT in the pool yet.
	// childB is still open and has not been submitted at all.
	flush(1)
	transport.Traces()

	// Restore deterministic pool state for Phase 2.
	//
	// The global spanPool is shared across tests in the same process, so foreign
	// spans from prior tests may be ahead of root/childA in the LIFO queue.
	// Two GC cycles flush all pool entries: the first GC promotes the active pool
	// to the victim cache; the second clears the victim cache.  root and childA
	// are not freed because the test still holds live references to them.
	//
	// After the GCs we need stable P-local pool semantics for Put→Get:
	//   - GOMAXPROCS(1) forces all goroutines onto P0, so Put and Get always
	//     touch the same poolLocal (no cross-P theft alters the LIFO order).
	//   - SetGCPercent(-1) disables GC until cleanup so no GC can evict the
	//     items we are about to Put before Phase 2's Get calls retrieve them.
	//
	// sync.Pool LIFO within a P: Put writes to l.private first (if empty), then
	// pushes to l.shared.head.  Get reads l.private first, then pops l.shared.head.
	// So the FIRST Put (into an empty pool) occupies private and is the FIRST Get.
	// We want Get#1 = childA and Get#2 = root, so we Put childA first (→ private)
	// and root second (→ shared.head).
	//
	// Two GC cycles evict all pool entries (first GC: active → victim cache;
	// second GC: victim cache cleared).  root and childA remain alive because the
	// test holds live references to them, so they are not freed.
	runtime.GC()
	runtime.GC()
	// root was excluded from spansToRelease during Phase 1's partial flush (the
	// tracer exempts the trace-root span from mid-trace pool recycling so it can
	// be included in the eventual full-trace submission).  Its finished field is
	// therefore still true from Phase 1.  Call clear() now so that newChildA.Finish()
	// will not exit early on the s.finished guard in span.finish().
	root.clear()
	// GOMAXPROCS=1 ensures all Put→Get operations land on the same poolLocal so
	// LIFO order is deterministic. GC disabled prevents eviction during the window.
	// Both are restored immediately after the span instances are in hand so that
	// Phase 2's goroutine scheduling (worker, traceWriter) is unaffected.
	numCPU := runtime.GOMAXPROCS(1)
	prevGC := debug.SetGCPercent(-1)
	spanPool.Put(childA) // childA → P0.private   (returned first by Get)
	spanPool.Put(root)   // root → P0.shared.head (returned second by Get)

	// Phase 2: trigger the reverse ordering "root.mu → childA.mu".
	// pool.Get() returns childA first (LIFO), making it the new root.
	// The second Get returns root, making it the new childA.
	newRoot := tracer.StartSpan("newRoot", Tag(ext.ManualKeep, true)) // = childA instance
	newChildA := tracer.StartSpan("newChildA", ChildOf(newRoot.Context())) // = root instance
	// Restore concurrency and GC before Phase 2's span operations so that the
	// worker and traceWriter goroutines can be scheduled normally.
	debug.SetGCPercent(prevGC)
	runtime.GOMAXPROCS(numCPU)
	require.Same(t, childA, newRoot)
	require.Same(t, root, newChildA)
	newChildB := tracer.StartSpan("newChildB", ChildOf(newRoot.Context()))

	newRoot.Finish()    // childA instance is now finishedSpans[0] = fSpan
	newChildA.Finish()  // root instance crosses threshold: s=root, fSpan=childA
	// Under the buggy code the detector fires here: it sees "root.mu → childA.mu"
	// and finds the earlier "childA.mu → root.mu" entry — AB/BA cycle.
	flush(1)
	transport.Traces()

	// Cleanup: finish the two leftover spans.
	childB.Finish()
	newChildB.Finish()
	flush(2)
}
