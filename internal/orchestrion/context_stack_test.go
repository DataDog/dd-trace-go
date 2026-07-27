// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"fmt"
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

// The nil case mirrors the woven *tracer.Span implementation
// (ddtrace/tracer/orchestrion.yml, "Span GLS fields") and the contract on
// [reclaimable]: a nil pointer is never a live scope.
func (f *fakeReclaimable) GLSReclaimable() bool { return f == nil || f.reclaimed }

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
	assert.Same(t, second, s.Peek(stackTestKey{}), "new value should be on top")
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
	assert.Same(t, live, s.Peek(stackTestKey{}))
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
	assert.Same(t, child, s.Peek(stackTestKey{}))
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

// TestPeekSkipsReclaimableEntries verifies that Peek never surfaces a finished
// (reclaimable) entry as the active value, and still returns the nearest live
// one. This is the read-side guard for the trace-merge in APMS-20132:
//
//	StartSpanFromContext(r.Context(), "http.request")
//	  1. SpanFromContext → Peek → stale FINISHED span   ← parent chosen here
//	  2. ChildOf(staleSpan)
//	  3. StartSpan(...)
//	  4. ContextWithSpan → Push → reclaim drain          ← one step too late
//
// A finished entry ends up on top because the pop is not identity-matched (see
// GLSPopFunc): when a span finished on another goroutine sits above the running
// request's own span, the request's finish pops that stray and leaves its own
// span behind. Push's drain then bounds the stack, so the memory leak is gone,
// but the read above still happens first — and returning that survivor makes the
// next request its child, carrying one trace_id forward indefinitely.
//
// Entries are pushed LIVE and only then marked reclaimable: pushing an
// already-reclaimable value lets Push drain it immediately, so the stack would
// never reach the intended depth (same technique as
// TestPushDrainsMultipleReclaimableEntries).
func TestPeekSkipsReclaimableEntries(t *testing.T) {
	tests := []struct {
		name string
		// finished describes the stack bottom→top: true = finished (reclaimable).
		finished []bool
		// wantIdx indexes finished for the entry Peek must return; -1 means nil.
		wantIdx int
	}{
		{
			name:     "finished top entry is not an active parent",
			finished: []bool{true},
			wantIdx:  -1,
		},
		{
			name:     "every entry finished leaves no active parent",
			finished: []bool{true, true, true},
			wantIdx:  -1,
		},
		{
			name:     "skips a finished top down to the live entry beneath it",
			finished: []bool{false, true},
			wantIdx:  0,
		},
		{
			name:     "skips a run of finished entries down to the live one",
			finished: []bool{false, true, true, true},
			wantIdx:  0,
		},
		{
			name:     "returns a live top and ignores finished entries beneath",
			finished: []bool{true, false},
			wantIdx:  1,
		},
		{
			name:     "returns the nearest live entry, not the deepest",
			finished: []bool{false, false, true},
			wantIdx:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := contextStack(make(map[any][]any))

			// Push every entry live so the stack actually reaches len(finished).
			entries := make([]*fakeReclaimable, len(tt.finished))
			for i := range entries {
				entries[i] = &fakeReclaimable{id: i}
				s.Push(stackTestKey{}, entries[i])
			}
			require.Equal(t, len(tt.finished), s.Depth(), "stack must reach the intended depth")

			// Now finish the ones the case marks — as a cross-goroutine finish
			// would, where the matching pop never ran on this goroutine.
			for i, fin := range tt.finished {
				entries[i].reclaimed = fin
			}

			got := s.Peek(stackTestKey{})
			if tt.wantIdx < 0 {
				assert.Nil(t, got,
					"Peek must return nil rather than a finished entry; returning one lets "+
						"StartSpanFromContext adopt it as a parent (APMS-20132 trace-merge)")
				return
			}
			assert.Same(t, entries[tt.wantIdx], got, "Peek must return the nearest live entry")
		})
	}
}

// TestPeekBreaksStaleParentChain exercises Push's drain, the position-matched Pop
// and Peek together over repeated cycles, which no single-operation test covers.
// Each iteration is one request on a reused goroutine: a span finished elsewhere
// lands above the request's own span, so the request's finish pops that stray and
// leaves its own span behind. Peek must refuse the survivor every time; returning
// it would make each request a child of the last.
func TestPeekBreaksStaleParentChain(t *testing.T) {
	s := contextStack(make(map[any][]any))
	k := stackTestKey{}

	for i := range 10 {
		req := &fakeReclaimable{id: i}
		s.Push(k, req)

		stray := &fakeReclaimable{id: 1000 + i}
		s.Push(k, stray)
		stray.reclaimed = true

		req.reclaimed = true
		require.Same(t, stray, s.Pop(k), "the position-matched pop takes the stray, not the request span")
		require.Equal(t, 1, s.Depth(), "only the request's own entry should remain")

		assert.Nilf(t, s.Peek(k),
			"request %d's finished span must not be offered as a parent to request %d", i, i+1)
	}
}

// TestPeekSkipsNilReclaimableEntry guards the nil half of the [reclaimable]
// contract. A typed-nil pointer satisfies the interface, so if it reported itself
// live it would be handed out as the active scope and would hide every entry
// beneath it — and would also stop Push's drain, since the drain breaks at the
// first live entry. Nothing pushes a nil span today (the woven ContextWithSpan
// advice is guarded by `if s != nil`), which is exactly why this needs a test:
// the guard lives in the woven method, out of reach of the rest of this suite.
func TestPeekSkipsNilReclaimableEntry(t *testing.T) {
	s := contextStack(make(map[any][]any))

	live := &fakeReclaimable{id: 1}
	s.Push(stackTestKey{}, live)
	s.Push(stackTestKey{}, (*fakeReclaimable)(nil))
	require.Equal(t, 2, s.Depth(), "the nil entry is pushed on top of the live one")

	assert.Same(t, live, s.Peek(stackTestKey{}),
		"a nil entry must not shadow the live entry beneath it")
}

// TestPeekReturnsNonReclaimableValues guards the values that do not implement
// [reclaimable] — e.g. the bool stored under executionTracedKey. They carry no
// finished state, so Peek must return them unchanged.
func TestPeekReturnsNonReclaimableValues(t *testing.T) {
	s := contextStack(make(map[any][]any))

	s.Push(stackTestKey{}, false)
	s.Push(stackTestKey{}, true)

	assert.Equal(t, true, s.Peek(stackTestKey{}), "non-reclaimable top value must be returned as-is")
}

// BenchmarkPeekSkipReclaimed measures Peek's cost as finished entries pile above
// the nearest live one. Peek runs on every SpanFromContext, so 0_reclaimed is the
// case that matters; the rest bound the walk, which Push's drain keeps short in
// practice (TestSpanGLSNoLeakCrossGoroutine holds depth at ~1 over 5000 cycles).
func BenchmarkPeekSkipReclaimed(b *testing.B) {
	for _, stale := range []int{0, 1, 10, 100} {
		b.Run(fmt.Sprintf("%d_reclaimed", stale), func(b *testing.B) {
			k := stackTestKey{}
			s := contextStack(make(map[any][]any))

			// A live entry at the bottom is what Peek must find.
			s.Push(k, &fakeReclaimable{id: -1})
			// Pushed live so the stack reaches depth N, then marked finished —
			// pushing them reclaimable would let Push drain each one immediately.
			entries := make([]*fakeReclaimable, stale)
			for i := range entries {
				entries[i] = &fakeReclaimable{id: i}
				s.Push(k, entries[i])
			}
			for _, e := range entries {
				e.reclaimed = true
			}

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				s.Peek(k)
			}
		})
	}
}

// BenchmarkPushDrainReclaimed measures the cost of Push's trailing-reclaim drain
// under the cross-goroutine-finish pattern: a stack accumulates reclaimable
// entries (finished spans whose pop never ran on this goroutine), then a single
// Push drains them all before appending the new value.
//
// The 0_reclaimed sub-benchmark is the common case (nothing to drain) and
// establishes the baseline cost of the drain loop. The N_reclaimed cases show
// the amortised cost per entry: each entry is pushed once and drained once, so
// total work is O(N) regardless of how many pushes trigger the drain.
//
// The entries are built LIVE and only then marked reclaimable: pushing them
// reclaimable would let Push drain each one as the stack is built, so the stack
// would never reach depth N (this mirrors TestPushDrainsMultipleReclaimableEntries).
func BenchmarkPushDrainReclaimed(b *testing.B) {
	for _, stale := range []int{0, 1, 10, 100} {
		b.Run(fmt.Sprintf("%d_reclaimed", stale), func(b *testing.B) {
			k := stackTestKey{}
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				s := contextStack(make(map[any][]any))
				entries := make([]*fakeReclaimable, stale)
				for i := range entries {
					entries[i] = &fakeReclaimable{id: i}
					s.Push(k, entries[i]) // pushed live so the stack reaches depth N
				}
				for _, e := range entries {
					e.reclaimed = true // now finished cross-goroutine (pop never ran here)
				}
				b.StartTimer()

				// This single Push must drain all `stale` reclaimable entries.
				s.Push(k, &fakeReclaimable{id: stale})
			}
		})
	}
}
