// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stackTestKey struct{}

func TestPopNilsBackingArrayElement(t *testing.T) {
	s := contextStack(make(map[any][]any))

	// Push two values so popping one keeps the map entry alive (len > 0).
	// This lets us inspect the backing array for the cleared slot.
	s.Push(stackTestKey{}, "filler")
	large := make([]byte, 1<<20) // 1 MiB
	s.Push(stackTestKey{}, large)

	popped := s.Pop(stackTestKey{})
	require.NotNil(t, popped)

	// The map entry still exists (one element remains). Check that the
	// backing array slot at index 1 was cleared so GC can collect it.
	stack := s[stackTestKey{}]
	require.Len(t, stack, 1, "one element should remain")
	rawSlice := stack[:cap(stack)]
	assert.Nil(t, rawSlice[1], "popped element should be nil in backing array to allow GC")
}

func TestPopCleansUpEmptyMapEntry(t *testing.T) {
	s := contextStack(make(map[any][]any))

	s.Push(stackTestKey{}, "value")
	s.Pop(stackTestKey{})

	_, exists := s[stackTestKey{}]
	assert.False(t, exists, "empty stack entry should be removed from the map")
}

// fakeReclaimable is a test value that implements the reclaimable interface so
// we can drive contextStack.Push's drain logic without depending on the tracer.
type fakeReclaimable struct {
	id        int
	reclaimed bool
}

func (f *fakeReclaimable) GLSReclaimable() bool { return f.reclaimed }

func TestPushReclaimsFinishedTopEntry(t *testing.T) {
	s := contextStack(make(map[any][]any))

	first := &fakeReclaimable{id: 1}
	s.Push(stackTestKey{}, first)
	require.Equal(t, 1, s.Depth(), "first push lands")

	// Mark the top entry reclaimable, as a finished span would be. The next
	// push must drop it instead of stacking on top, keeping depth at 1.
	first.reclaimed = true
	second := &fakeReclaimable{id: 2}
	s.Push(stackTestKey{}, second)

	assert.Equal(t, 1, s.Depth(), "reclaimable top entry should be dropped on push")
	assert.Equal(t, second, s.Peek(stackTestKey{}), "new value should be on top")
}

func TestPushDrainsMultipleReclaimableEntries(t *testing.T) {
	s := contextStack(make(map[any][]any))

	// Several entries pile up while live (pushed on this goroutine, e.g. via
	// ContextWithSpan), building real depth.
	entries := make([]*fakeReclaimable, 5)
	for i := range entries {
		entries[i] = &fakeReclaimable{id: i}
		s.Push(stackTestKey{}, entries[i])
	}
	require.Equal(t, 5, s.Depth(), "five live entries pushed")

	// They all get finished elsewhere (no matching pop ran on this goroutine).
	for _, e := range entries {
		e.reclaimed = true
	}

	// The next push must drain ALL trailing reclaimable entries, not just the top.
	live := &fakeReclaimable{id: 99}
	s.Push(stackTestKey{}, live)

	assert.Equal(t, 1, s.Depth(), "all trailing reclaimable entries should be drained")
	assert.Equal(t, live, s.Peek(stackTestKey{}))
}

func TestPushKeepsLiveEntries(t *testing.T) {
	s := contextStack(make(map[any][]any))

	// A live (non-reclaimable) entry on top must never be dropped — this is
	// the legitimate same-goroutine nesting case (parent still active).
	parent := &fakeReclaimable{id: 1, reclaimed: false}
	s.Push(stackTestKey{}, parent)
	child := &fakeReclaimable{id: 2, reclaimed: false}
	s.Push(stackTestKey{}, child)

	assert.Equal(t, 2, s.Depth(), "live entries must be preserved (nesting)")
	assert.Equal(t, child, s.Peek(stackTestKey{}))
}

func TestPushDoesNotReclaimBuriedEntryUnderLiveTop(t *testing.T) {
	s := contextStack(make(map[any][]any))

	// Build [buried, liveTop] with both live, so neither is dropped at push.
	buried := &fakeReclaimable{id: 1, reclaimed: false}
	s.Push(stackTestKey{}, buried)
	liveTop := &fakeReclaimable{id: 2, reclaimed: false}
	s.Push(stackTestKey{}, liveTop)
	require.Equal(t, 2, s.Depth())

	// buried becomes reclaimable, but liveTop (still live) sits above it.
	buried.reclaimed = true

	next := &fakeReclaimable{id: 3, reclaimed: false}
	s.Push(stackTestKey{}, next)

	// The drain stops at liveTop (not reclaimable), so buried is preserved.
	// This is the invariant that protects legitimate nesting: a reclaimable
	// entry beneath a live scope is never dropped.
	assert.Equal(t, 3, s.Depth(), "drain stops at the first live entry from the top; buried stays")
}

func TestPushDoesNotDrainNonReclaimableValues(t *testing.T) {
	s := contextStack(make(map[any][]any))

	// Values that don't implement reclaimable (e.g. the bool stored under
	// executionTracedKey) must never be drained, even if they pile up.
	s.Push(stackTestKey{}, true)
	s.Push(stackTestKey{}, false)
	s.Push(stackTestKey{}, true)

	assert.Equal(t, 3, s.Depth(), "non-reclaimable values are never dropped")
}
