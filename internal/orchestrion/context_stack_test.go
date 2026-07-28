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
	var s contextStack

	// Push two values so popping one keeps the map entry alive (len > 0).
	// This lets us inspect the backing array for the cleared slot.
	s.Push(stackTestKey{}, "filler")
	large := make([]byte, 1<<20) // 1 MiB
	s.Push(stackTestKey{}, large)

	popped := s.Pop(stackTestKey{})
	require.NotNil(t, popped)

	// The map entry still exists (one element remains). Check that the
	// backing array slot at index 1 was cleared so GC can collect it.
	stack := s.stacks[stackTestKey{}]
	require.Len(t, stack, 1, "one element should remain")
	rawSlice := stack[:cap(stack)]
	assert.Zero(t, rawSlice[1], "popped element should be zeroed in backing array to allow GC")
}

func TestPopCleansUpEmptyMapEntry(t *testing.T) {
	var s contextStack

	s.Push(stackTestKey{}, "value")
	s.Pop(stackTestKey{})

	_, exists := s.stacks[stackTestKey{}]
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
	var s contextStack

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
	var s contextStack

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
	var s contextStack

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
	var s contextStack

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
	var s contextStack

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
			var s contextStack

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
	var s contextStack
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
	var s contextStack

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
	var s contextStack

	s.Push(stackTestKey{}, false)
	s.Push(stackTestKey{}, true)

	assert.Equal(t, true, s.Peek(stackTestKey{}), "non-reclaimable top value must be returned as-is")
}

// TestPopScopeRemovesTargetAndEverythingAbove is the half of APMS-20132 the Peek
// guard cannot reach. Pop takes the top rather than the scope that ended, so a
// non-LIFO exit strands an entry:
//
//	push A, push B, push C
//	A exits  →  Pop takes C, leaving [A(finished), B(live)]
//
// Peek walks down from the top and stops at B, because B is LIVE. It is not a
// finished span the read-side guard can skip; it is a sibling scope that simply
// outlived the one that ended. Handing it out makes the next request its child.
//
// PopScope closes A by token and takes B and C with it: both were opened inside
// A's scope, so neither can outlive it.
func TestPopScopeRemovesTargetAndEverythingAbove(t *testing.T) {
	var s contextStack
	k := stackTestKey{}

	a := &fakeReclaimable{id: 1}
	tokenA := s.Push(k, a)
	b := &fakeReclaimable{id: 2}
	s.Push(k, b)
	c := &fakeReclaimable{id: 3}
	s.Push(k, c)
	require.Equal(t, 3, s.Depth())

	// A finishes while it is not the top: the non-LIFO exit.
	a.reclaimed = true
	require.True(t, s.PopScope(k, tokenA), "PopScope must find the scope its token opened")

	assert.Equal(t, 0, s.Depth(), "A, B and C must all be gone")

	got := s.Peek(k)
	assert.Nil(t, got, "nothing survives A's exit, so there is no active scope")
	assert.NotEqual(t, b, got,
		"B outlived the scope that opened it and became the active one — the top-pop "+
			"stranding that produced the APMS-20132 trace merge")
}

// TestPopScopeKeepsEntriesBelow pins the other side of scope exit: an enclosing
// scope is still live and must become active again. This is ordinary nesting —
// a child span finishing inside its parent — and a PopScope that cleared the
// whole slice would break it while still passing every test above.
func TestPopScopeKeepsEntriesBelow(t *testing.T) {
	var s contextStack
	k := stackTestKey{}

	outer := &fakeReclaimable{id: 1}
	s.Push(k, outer)
	inner := &fakeReclaimable{id: 2}
	tokenInner := s.Push(k, inner)
	leaf := &fakeReclaimable{id: 3}
	s.Push(k, leaf)

	inner.reclaimed = true
	require.True(t, s.PopScope(k, tokenInner))

	assert.Equal(t, 1, s.Depth(), "only inner and what inner opened are removed")
	assert.Same(t, outer, s.Peek(k), "the enclosing scope becomes active again")

	// The vacated slots are zeroed so the GC can collect the values they held,
	// matching Pop and Push's drain.
	raw := s.stacks[k]
	require.GreaterOrEqual(t, cap(raw), 3, "the backing array must still hold the removed slots")
	raw = raw[:cap(raw)]
	assert.Zero(t, raw[1], "inner's slot must be cleared")
	assert.Zero(t, raw[2], "leaf's slot must be cleared")
}

// TestPopScopeOnRemovedTokenDoesNothing covers the repeated or late exit: a
// popper that already ran, or one whose scope was swept away by an enclosing
// PopScope. It must report that there was nothing to close and leave the stack
// alone, because by then the position it once held may belong to someone else.
func TestPopScopeOnRemovedTokenDoesNothing(t *testing.T) {
	var s contextStack
	k := stackTestKey{}

	gone := &fakeReclaimable{id: 1}
	token := s.Push(k, gone)
	require.True(t, s.PopScope(k, token), "the first exit closes the scope")

	assert.False(t, s.PopScope(k, token), "a second exit finds nothing to close")
	assert.Equal(t, 0, s.Depth())

	// The same holds with an unrelated scope open: the stale exit must not take it.
	live := &fakeReclaimable{id: 2}
	s.Push(k, live)

	assert.False(t, s.PopScope(k, token), "the stale token still matches nothing")
	assert.Equal(t, 1, s.Depth(), "the unrelated scope must survive the stale exit")
	assert.Same(t, live, s.Peek(k))
}

// TestPopScopeIgnoresStaleTokenAfterIndexReuse is the ABA case, and the reason
// an entry carries a token instead of the popper capturing an index. The slice
// hands index 0 to the second scope after the first is gone, so an index-based
// exit would close a scope it never opened.
//
// The counter lives on the contextStack rather than alongside each key's slice
// precisely so this holds: PopScope deletes the key once it empties, and a
// per-key counter would restart from zero and re-issue the stale token.
func TestPopScopeIgnoresStaleTokenAfterIndexReuse(t *testing.T) {
	var s contextStack
	k := stackTestKey{}

	first := &fakeReclaimable{id: 1}
	stale := s.Push(k, first)
	require.True(t, s.PopScope(k, stale))
	require.Equal(t, 0, s.Depth(), "the key is now empty, so the next push starts at index 0 again")

	second := &fakeReclaimable{id: 2}
	fresh := s.Push(k, second)
	require.Same(t, second, s.stacks[k][0].val, "the second scope reuses the first scope's index")
	require.NotEqual(t, stale, fresh, "a reused index must not come with a reused token")

	assert.False(t, s.PopScope(k, stale), "the stale exit matches nothing")
	assert.Equal(t, 1, s.Depth(), "the unrelated scope at the same index must survive")
	assert.Same(t, second, s.Peek(k), "the stale exit must not disturb the active scope")
}

// BenchmarkPeekSkipReclaimed measures Peek's cost as finished entries pile above
// the nearest live one. Peek runs on every SpanFromContext, so 0_reclaimed is the
// case that matters; the rest bound the walk, which Push's drain keeps short in
// practice (TestSpanGLSNoLeakCrossGoroutine holds depth at ~1 over 5000 cycles).
func BenchmarkPeekSkipReclaimed(b *testing.B) {
	for _, stale := range []int{0, 1, 10, 100} {
		b.Run(fmt.Sprintf("%d_reclaimed", stale), func(b *testing.B) {
			k := stackTestKey{}
			var s contextStack

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
				var s contextStack
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
