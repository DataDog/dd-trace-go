// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stackTestKey struct{}

func TestPopNilsBackingArrayElement(t *testing.T) {
	s := contextStack(make(map[any][]stackEntry))

	// Push two values so popping one keeps the map entry alive (len > 0).
	// This lets us inspect the backing array for the cleared slot.
	s.Push(stackTestKey{}, "filler", nil)
	large := make([]byte, 1<<20) // 1 MiB
	s.Push(stackTestKey{}, large, nil)

	popped := s.Pop(stackTestKey{})
	require.NotNil(t, popped)

	// The map entry still exists (one element remains). Check that the
	// backing array slot at index 1 was cleared so GC can collect it.
	stack := s[stackTestKey{}]
	require.Len(t, stack, 1, "one element should remain")
	rawSlice := stack[:cap(stack)]
	assert.Nil(t, rawSlice[1].value, "popped element value should be nil in backing array to allow GC")
}

func TestPopCleansUpEmptyMapEntry(t *testing.T) {
	s := contextStack(make(map[any][]stackEntry))

	s.Push(stackTestKey{}, "value", nil)
	s.Pop(stackTestKey{})

	_, exists := s[stackTestKey{}]
	assert.False(t, exists, "empty stack entry should be removed from the map")
}

func TestPeekSkipsDoneEntries(t *testing.T) {
	s := contextStack(make(map[any][]stackEntry))
	k := stackTestKey{}

	// Live parent, then a child pushed on top, both live.
	parentCell := new(atomic.Bool)
	s.Push(k, "parent", parentCell)
	childCell := new(atomic.Bool)
	s.Push(k, "child", childCell)
	require.Equal(t, "child", s.Peek(k), "live top entry is the active value")

	// The child finishes (e.g. cross-goroutine, so it was not popped here). Its
	// done cell is true. Peek must skip it and surface the live parent — not the
	// finished child, which under pooling may already be a recycled, unrelated span.
	childCell.Store(true)
	assert.Equal(t, "parent", s.Peek(k), "finished top entry must be skipped; live parent surfaces")

	// Parent finishes too: no live scope remains, Peek reports none.
	parentCell.Store(true)
	assert.Nil(t, s.Peek(k), "all entries done: no active value")

	// Depth is unchanged by Peek (it is read-only; Push does the draining).
	assert.Equal(t, 2, s.Depth(), "Peek must not mutate the stack")
}

func TestPeekReturnsNilDoneEntries(t *testing.T) {
	s := contextStack(make(map[any][]stackEntry))
	k := stackTestKey{}

	// Values with a nil done cell (e.g. the bool under executionTracedKey) are
	// never skipped, regardless of how many pile up.
	s.Push(k, true, nil)
	assert.Equal(t, true, s.Peek(k), "nil-done entries are always live for Peek")
}

// newCell allocates a fresh liveness cell (not yet done). Convenience for tests.
func newCell() *atomic.Bool { return new(atomic.Bool) }

func TestPushReclaimsFinishedTopEntry(t *testing.T) {
	s := contextStack(make(map[any][]stackEntry))

	cell1 := newCell()
	s.Push(stackTestKey{}, "first", cell1)
	require.Equal(t, 1, s.Depth(), "first push lands")

	// Mark the top entry done, as a finished span would be. The next
	// push must drop it instead of stacking on top, keeping depth at 1.
	cell1.Store(true)
	s.Push(stackTestKey{}, "second", newCell())

	assert.Equal(t, 1, s.Depth(), "done top entry should be dropped on push")
	assert.Equal(t, "second", s.Peek(stackTestKey{}), "new value should be on top")
}

func TestPushDrainsMultipleReclaimableEntries(t *testing.T) {
	s := contextStack(make(map[any][]stackEntry))

	// Several entries pile up while live (pushed on this goroutine, e.g. via
	// ContextWithSpan), building real depth.
	cells := make([]*atomic.Bool, 5)
	for i := range cells {
		cells[i] = newCell()
		s.Push(stackTestKey{}, i, cells[i])
	}
	require.Equal(t, 5, s.Depth(), "five live entries pushed")

	// They all get finished elsewhere (no matching pop ran on this goroutine).
	for _, c := range cells {
		c.Store(true)
	}

	// The next push must drain ALL trailing done entries, not just the top.
	s.Push(stackTestKey{}, "live", newCell())

	assert.Equal(t, 1, s.Depth(), "all trailing done entries should be drained")
	assert.Equal(t, "live", s.Peek(stackTestKey{}))
}

func TestPushKeepsLiveEntries(t *testing.T) {
	s := contextStack(make(map[any][]stackEntry))

	// A live (not-done) entry on top must never be dropped — this is
	// the legitimate same-goroutine nesting case (parent still active).
	s.Push(stackTestKey{}, "parent", newCell())
	s.Push(stackTestKey{}, "child", newCell())

	assert.Equal(t, 2, s.Depth(), "live entries must be preserved (nesting)")
	assert.Equal(t, "child", s.Peek(stackTestKey{}))
}

func TestPushDoesNotReclaimBuriedEntryUnderLiveTop(t *testing.T) {
	s := contextStack(make(map[any][]stackEntry))

	// Build [buried, liveTop] with both live, so neither is dropped at push.
	buriedCell := newCell()
	s.Push(stackTestKey{}, "buried", buriedCell)
	s.Push(stackTestKey{}, "liveTop", newCell())
	require.Equal(t, 2, s.Depth())

	// buried becomes done, but liveTop (still live) sits above it.
	buriedCell.Store(true)

	s.Push(stackTestKey{}, "next", newCell())

	// The drain stops at liveTop (not done), so buried is preserved.
	// This is the invariant that protects legitimate nesting: a done entry
	// beneath a live scope is never dropped.
	assert.Equal(t, 3, s.Depth(), "drain stops at the first live entry from the top; buried stays")
}

func TestPushDoesNotDrainNilDoneEntries(t *testing.T) {
	s := contextStack(make(map[any][]stackEntry))

	// Values pushed with done=nil (e.g. the bool stored under executionTracedKey)
	// must never be drained, even if they pile up.
	s.Push(stackTestKey{}, true, nil)
	s.Push(stackTestKey{}, false, nil)
	s.Push(stackTestKey{}, true, nil)

	assert.Equal(t, 3, s.Depth(), "nil-done values are never dropped")
}

// BenchmarkPushDrainReclaimed measures the cost of Push's trailing-reclaim drain
// under the cross-goroutine-finish pattern: a stack accumulates done entries
// (finished spans whose pop never ran on this goroutine), then a single Push
// drains them all before appending the new value.
func BenchmarkPushDrainReclaimed(b *testing.B) {
	for _, stale := range []int{0, 1, 10, 100} {
		b.Run(fmt.Sprintf("%d_reclaimed", stale), func(b *testing.B) {
			k := stackTestKey{}
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				s := contextStack(make(map[any][]stackEntry))
				cells := make([]*atomic.Bool, stale)
				for i := range cells {
					cells[i] = newCell()
					s.Push(k, i, cells[i]) // pushed live so the stack reaches depth N
				}
				for _, c := range cells {
					c.Store(true) // now finished cross-goroutine (pop never ran here)
				}
				b.StartTimer()

				// This single Push must drain all `stale` done entries.
				s.Push(k, stale, newCell())
			}
		})
	}
}
