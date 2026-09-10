// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPushReuseNoBuriedRecycledEntry is the regression test for the ABA hazard
// that kept the experimental span pool and orchestrion GLS mutually exclusive
// (DataDog/orchestrion#782).
//
// The sequence it models is one pooled span going round and round:
//
//	GLSActivate    push the span, entry captures a fresh cell
//	GLSDeactivate  finished on another goroutine, cell → true
//	GLSReset       clear() hands the span back to the pool
//	GLSActivate    the SAME object, now an unrelated scope, gets a new cell
//
// When the liveness bit lived on the span, that last step is where it broke:
// clear() reset the bit to false, so the buried entry from the previous lifecycle
// read live again and the drain stopped dead at it. Depth then grew by one per
// recycle. Holding the cell on the entry makes the reset unreachable — the entry
// still reads the cell for the scope it actually opened.
//
// The value pushed is deliberately the same object every iteration, because a
// test that pushed distinct objects would pass even if liveness were still read
// off the value.
func TestPushReuseNoBuriedRecycledEntry(t *testing.T) {
	var s contextStack
	k := stackTestKey{}

	// One object standing in for the span the pool keeps handing back.
	recycled := &scope{id: 1}

	const iterations = 1000
	for range iterations {
		// GLSActivate on a span whose done field is nil (first activation of this
		// lifecycle — GLSReset left it nil last time round): allocate a fresh cell
		// and hand it to Push, which captures it on the entry.
		recycled.done = new(atomic.Bool)
		s.Push(k, recycled, recycled.done, nil)

		// GLSDeactivate: finished on another goroutine, so the popper was a no-op
		// and the entry stays behind, marked done.
		recycled.done.Store(true)

		// GLSReset: clear() drops the span's pointer to the cell. The entry keeps
		// its own, so the true above stays visible to the next drain. This is the
		// step that used to flip the signal back to false.
		recycled.done = nil
	}

	// Every stale entry must have been drained even though the object behind them
	// was recycled the whole time. Only the final push survives — nothing has
	// pushed after it to trigger the drain.
	assert.LessOrEqual(t, s.Depth(), 1,
		"pool-recycled entries must not accumulate; depth = %d after %d recycles", s.Depth(), iterations)
}

// TestPushReuseNoPeekSurface is the correctness half of the same hazard. Bounding
// the stack is not enough: while a stale entry is still on it, a read must not
// offer the recycled object as this goroutine's active scope. Doing so hands
// StartSpanFromContext a parent belonging to a request that already ended — and
// once the pool has reused the object, one that now belongs to somebody else
// entirely.
func TestPushReuseNoPeekSurface(t *testing.T) {
	var s contextStack
	k := stackTestKey{}

	first := newScope(1)
	s.Push(k, first, first.done, nil)
	first.finish() // finished on another goroutine

	// Pool reuse: the span forgets its cell, but the entry still holds it.
	firstCell := first.done
	first.done = nil
	require.True(t, firstCell.Load(), "the entry's cell keeps the finish visible")

	assert.Nil(t, s.Peek(k), "a finished entry must not be offered as the active scope")

	// A later, unrelated activation drains it and becomes the only live entry.
	second := newScope(2)
	s.Push(k, second, second.done, nil)
	require.Equal(t, 1, s.Depth(), "the stale entry is drained, not stacked under")
	require.Same(t, second, s.Peek(k), "the live scope is active")

	// When the live scope pops, the drained predecessor must not come back.
	require.Same(t, second, s.Pop(k), "pop removes the live top")
	assert.Nil(t, s.Peek(k),
		"the finished-then-recycled entry must not resurface once the live scope is gone")
}
