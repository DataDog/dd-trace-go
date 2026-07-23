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

// TestPushReuseNoBuriedRecycledEntry is a regression test for the ABA hazard in
// contextStack.Push's drain (DataDog/orchestrion#782). In the orchestrion GLS
// weave, a span can be pushed on one goroutine, finished elsewhere (setting its
// done cell to true via GLSDeactivate), and then returned to the tracer's
// sync.Pool (GLSReset clears the span's reference to the cell). The span is then
// handed out for a new, unrelated scope.
//
// In the previous design the reclaim flag lived on the span itself, so GLSReset
// flipped it back to false — making the old stack entry look live again (ABA).
// With the cell-based design the contextStack entry holds its own reference to
// the original *atomic.Bool cell; GLSReset only clears the span's copy. The
// cell's true value persists, so the next Push correctly drains the stale entry.
func TestPushReuseNoBuriedRecycledEntry(t *testing.T) {
	s := contextStack(make(map[any][]stackEntry))
	k := stackTestKey{}

	const iterations = 1000
	for i := range iterations {
		// GLSActivate: allocate a fresh cell, store it in the stack entry.
		cell := new(atomic.Bool)

		s.Push(k, i, cell)

		// GLSDeactivate: span finished cross-goroutine; cell is set to true.
		cell.Store(true)

		// GLSReset: span returned to pool. In the OLD design this would flip
		// the reclaim flag back to false, making the stack entry look live.
		// With cells the span simply forgets about this cell (it will allocate
		// a new one on its next activation). The stack entry's copy of cell is
		// unaffected — it still reads true.
		// (We simulate this by leaving cell unchanged; the span's field is gone.)
	}

	// A correct drain must reclaim all stale entries even when the span objects
	// were recycled. Depth must be ≤ 2 regardless of the number of iterations:
	// at most the final push (drained on the NEXT push, which we don't do here)
	// plus its predecessor.
	require.LessOrEqual(t, s.Depth(), 2,
		"pool-recycled entries must not leak; depth = %d, want ≤ 2", s.Depth())
}

// TestPushReuseNoPeekSurface is the correctness half of the ABA hazard: after
// pool reuse the Peek must never resurface a recycled span as the active value.
func TestPushReuseNoPeekSurface(t *testing.T) {
	s := contextStack(make(map[any][]stackEntry))
	k := stackTestKey{}

	cellA := new(atomic.Bool)
	s.Push(k, "spanA", cellA)
	cellA.Store(true) // finished cross-goroutine
	// Pool reuse: span's cell reference is cleared; cellA is still true.

	// Allocate a separate cell for spanB (a different activation).
	cellB := new(atomic.Bool)
	s.Push(k, "spanB", cellB) // drain: cellA.Load()==true → drain "spanA". Push "spanB".
	require.Equal(t, "spanB", s.Peek(k), "spanB should be active before pop")

	popped := s.Pop(k)
	require.Equal(t, "spanB", popped, "pop should remove the live top span")

	assert.Nil(t, s.Peek(k),
		"finished-then-recycled spanA must not resurface as this goroutine's active value")
}
