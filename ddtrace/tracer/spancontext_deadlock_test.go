// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build deadlock

package tracer

import (
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
// the partial flush fires mid-trace.
//
// Phase 2 must trigger the reverse ordering "root.mu → childA.mu" on the *same two
// Span instances so the detector finds the existing edge and panics. It reuses the
// childA and root instances in swapped roles: newRoot is the childA instance and
// newChildA is the root instance. newChildA.Finish() then crosses the threshold with
// s=root, fSpan=childA, recording "root.mu → childA.mu" — the reverse of Phase 1 on
// the same mutexes.
//
// The two instances are fed back into StartSpan via the testAcquireSpan hook (see
// span_pool_testhook.go). An earlier version recycled them through the global span
// pool, but that flaked: spanPool is process-global, the tracer worker concurrently
// Puts into it (confirmed by -race), and sync.Pool guarantees no Get/Put ordering or
// identity — even single-threaded, with GOMAXPROCS=1 and GC disabled, a Put-then-Get
// is not guaranteed to return the same instance once the pool has been churned across
// multiple Ps by sibling tests. The hook sidesteps the pool entirely: it is read only
// on the StartSpan path, which only this goroutine drives while the hook is set, so the
// handoff is deterministic. The span pool is irrelevant to the lock-ordering being
// tested, so this test does not enable it.
//
// Under the fixed code, s.mu is released before fSpan.mu is acquired, so neither
// direction is ever recorded and the test passes cleanly.
func TestPartialFlushSpanLockOrderingCycle(t *testing.T) {
	t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
	t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "2")

	tracer, transport, flush, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	// Phase 1: establish ordering "childA.mu → root.mu".
	// Three spans so the partial flush fires mid-trace, while childB is still open:
	// that is the !finishingSpanIsFirstInChunk path where s=childA but fSpan=root.
	root := tracer.StartSpan("root", Tag(ext.ManualKeep, true))
	childA := tracer.StartSpan("childA", ChildOf(root.Context()))
	childB := tracer.StartSpan("childB", ChildOf(root.Context()))

	root.Finish()
	childA.Finish() // triggers partial flush: s=childA (holds childA.mu), fSpan=root
	flush(1)
	transport.Traces()

	// Phase 2: reuse the childA and root instances in swapped roles.
	//
	// Clear both spans before reuse so StartSpan can re-initialize them and so
	// newChildA.Finish() does not exit early on the s.finished guard.
	childA.clear()
	root.clear()

	// Hand the two instances back to the next two StartSpan calls, in order:
	// newRoot := childA instance, newChildA := root instance. testAcquireSpan is
	// consulted only on the StartSpan path, which only this goroutine drives here,
	// so no synchronization is needed; clear it immediately afterwards so the
	// remaining spans allocate normally.
	reuse := []*Span{childA, root}
	testAcquireSpan = func() *Span {
		if len(reuse) == 0 {
			return nil
		}
		s := reuse[0]
		reuse = reuse[1:]
		return s
	}

	newRoot := tracer.StartSpan("newRoot", Tag(ext.ManualKeep, true))
	newChildA := tracer.StartSpan("newChildA", ChildOf(newRoot.Context()))
	testAcquireSpan = nil

	require.Same(t, childA, newRoot)
	require.Same(t, root, newChildA)

	newChildB := tracer.StartSpan("newChildB", ChildOf(newRoot.Context()))

	newRoot.Finish()   // childA instance is now finishedSpans[0] = fSpan
	newChildA.Finish() // root instance crosses threshold: s=root, fSpan=childA
	// Under the buggy code the detector fires here: it sees "root.mu → childA.mu"
	// and finds the earlier "childA.mu → root.mu" entry — AB/BA cycle.
	flush(1)
	transport.Traces()

	// Cleanup: finish the two leftover spans.
	childB.Finish()
	newChildB.Finish()
	flush(2)
}
