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
	var s contextStack

	// Push two values so popping one keeps the map entry alive (len > 0).
	// This lets us inspect the backing array for the cleared slot.
	s.Push(stackTestKey{}, "filler", nil, nil)
	large := make([]byte, 1<<20) // 1 MiB
	s.Push(stackTestKey{}, large, nil, nil)

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

	s.Push(stackTestKey{}, "value", nil, nil)
	s.Pop(stackTestKey{})

	_, exists := s.stacks[stackTestKey{}]
	assert.False(t, exists, "empty stack entry should be removed from the map")
}

// scope stands in for a span and the __dd_glsDone field orchestrion weaves onto
// it: a value to push, plus the liveness cell its entry captures.
//
// The two are bundled here only so a test can hold both. Every Push below passes
// them separately, which is the whole point of the design: the stack reads the
// cell it was handed at push time and never anything off the value, so an entry
// keeps describing the scope it opened even after the value is recycled into an
// unrelated one (see TestPushReuseNoBuriedRecycledEntry).
type scope struct {
	id   int
	done *atomic.Bool
}

func newScope(id int) *scope { return &scope{id: id, done: new(atomic.Bool)} }

// finish marks the scope done, as GLSDeactivate does when a span finishes.
func (sc *scope) finish() { sc.done.Store(true) }

func TestPushReclaimsFinishedTopEntry(t *testing.T) {
	var s contextStack

	first := newScope(1)
	s.Push(stackTestKey{}, first, first.done, nil)
	require.Equal(t, 1, s.Depth(), "first push lands")

	// Finish the top entry, as a cross-goroutine span finish would. The next push
	// must drop it instead of stacking on top, keeping depth at 1.
	first.finish()
	second := newScope(2)
	s.Push(stackTestKey{}, second, second.done, nil)

	assert.Equal(t, 1, s.Depth(), "finished top entry should be dropped on push")
	assert.Same(t, second, s.Peek(stackTestKey{}), "new value should be on top")
}

func TestPushDrainsMultipleFinishedEntries(t *testing.T) {
	var s contextStack

	// Several entries pile up while live (pushed on this goroutine, e.g. via
	// ContextWithSpan), building real depth.
	entries := make([]*scope, 5)
	for i := range entries {
		entries[i] = newScope(i)
		s.Push(stackTestKey{}, entries[i], entries[i].done, nil)
	}
	require.Equal(t, 5, s.Depth(), "five live entries pushed")

	// They all get finished elsewhere (no matching pop ran on this goroutine).
	for _, e := range entries {
		e.finish()
	}

	// The next push must drain ALL trailing finished entries, not just the top.
	live := newScope(99)
	s.Push(stackTestKey{}, live, live.done, nil)

	assert.Equal(t, 1, s.Depth(), "all trailing finished entries should be drained")
	assert.Same(t, live, s.Peek(stackTestKey{}))
}

func TestPushKeepsLiveEntries(t *testing.T) {
	var s contextStack

	// A live entry on top must never be dropped — this is the legitimate
	// same-goroutine nesting case (parent still active).
	parent := newScope(1)
	s.Push(stackTestKey{}, parent, parent.done, nil)
	child := newScope(2)
	s.Push(stackTestKey{}, child, child.done, nil)

	assert.Equal(t, 2, s.Depth(), "live entries must be preserved (nesting)")
	assert.Same(t, child, s.Peek(stackTestKey{}))
}

func TestPushDoesNotReclaimBuriedEntryUnderLiveTop(t *testing.T) {
	var s contextStack

	// Build [buried, liveTop] with both live, so neither is dropped at push.
	buried := newScope(1)
	s.Push(stackTestKey{}, buried, buried.done, nil)
	liveTop := newScope(2)
	s.Push(stackTestKey{}, liveTop, liveTop.done, nil)
	require.Equal(t, 2, s.Depth())

	// buried finishes, but liveTop (still live) sits above it.
	buried.finish()

	next := newScope(3)
	s.Push(stackTestKey{}, next, next.done, nil)

	// The drain stops at liveTop (still live), so buried is preserved.
	// This is the invariant that protects legitimate nesting: a finished
	// entry beneath a live scope is never dropped.
	assert.Equal(t, 3, s.Depth(), "drain stops at the first live entry from the top; buried stays")
}

// TestPushDoesNotDrainEntriesWithoutCell pins the nil-cell half of
// [entry.isDone]. Values that carry no liveness cell — the bool stored under
// executionTracedKey, and every dyngo operation, which is registered and finished
// on one goroutine — never report themselves finished, so the drain must leave
// them alone however many pile up. Draining them would silently discard state
// whose only exit is an explicit pop.
func TestPushDoesNotDrainEntriesWithoutCell(t *testing.T) {
	var s contextStack

	s.Push(stackTestKey{}, true, nil, nil)
	s.Push(stackTestKey{}, false, nil, nil)
	s.Push(stackTestKey{}, true, nil, nil)

	assert.Equal(t, 3, s.Depth(), "entries without a liveness cell are never dropped")
}

// TestPeekSkipsFinishedEntries verifies that Peek never surfaces a finished entry
// as the active value, and still returns the nearest live one. This is the
// read-side guard for the trace-merge in APMS-20132:
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
// Entries are pushed LIVE and only then finished: pushing an already-done cell
// lets Push drain it immediately, so the stack would never reach the intended
// depth (same technique as TestPushDrainsMultipleFinishedEntries).
func TestPeekSkipsFinishedEntries(t *testing.T) {
	tests := []struct {
		name string
		// finished describes the stack bottom→top: true = finished.
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
			entries := make([]*scope, len(tt.finished))
			for i := range entries {
				entries[i] = newScope(i)
				s.Push(stackTestKey{}, entries[i], entries[i].done, nil)
			}
			require.Equal(t, len(tt.finished), s.Depth(), "stack must reach the intended depth")

			// Now finish the ones the case marks — as a cross-goroutine finish
			// would, where the matching pop never ran on this goroutine.
			for i, fin := range tt.finished {
				if fin {
					entries[i].finish()
				}
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
		req := newScope(i)
		s.Push(k, req, req.done, nil)

		stray := newScope(1000 + i)
		s.Push(k, stray, stray.done, nil)
		stray.finish()

		req.finish()
		require.Same(t, stray, s.Pop(k), "the position-matched pop takes the stray, not the request span")
		require.Equal(t, 1, s.Depth(), "only the request's own entry should remain")

		assert.Nilf(t, s.Peek(k),
			"request %d's finished span must not be offered as a parent to request %d", i, i+1)
	}
}

// TestPeekReturnsEntriesWithoutCell is the read-side companion to
// TestPushDoesNotDrainEntriesWithoutCell: an entry with no liveness cell carries
// no finished state, so Peek must hand it back unchanged rather than walk past it.
// Skipping it would make the GLS lose the bool under executionTracedKey and the
// active dyngo operation.
func TestPeekReturnsEntriesWithoutCell(t *testing.T) {
	var s contextStack

	s.Push(stackTestKey{}, false, nil, nil)
	s.Push(stackTestKey{}, true, nil, nil)

	assert.Equal(t, true, s.Peek(stackTestKey{}), "a top value with no cell must be returned as-is")
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

	a := newScope(1)
	tokenA := s.Push(k, a, a.done, nil)
	b := newScope(2)
	s.Push(k, b, b.done, nil)
	c := newScope(3)
	s.Push(k, c, c.done, nil)
	require.Equal(t, 3, s.Depth())

	// A finishes while it is not the top: the non-LIFO exit.
	a.finish()
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

	outer := newScope(1)
	s.Push(k, outer, outer.done, nil)
	inner := newScope(2)
	tokenInner := s.Push(k, inner, inner.done, nil)
	leaf := newScope(3)
	s.Push(k, leaf, leaf.done, nil)

	inner.finish()
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

	gone := newScope(1)
	token := s.Push(k, gone, gone.done, nil)
	require.True(t, s.PopScope(k, token), "the first exit closes the scope")

	assert.False(t, s.PopScope(k, token), "a second exit finds nothing to close")
	assert.Equal(t, 0, s.Depth())

	// The same holds with an unrelated scope open: the stale exit must not take it.
	live := newScope(2)
	s.Push(k, live, live.done, nil)

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

	first := newScope(1)
	stale := s.Push(k, first, first.done, nil)
	require.True(t, s.PopScope(k, stale))
	require.Equal(t, 0, s.Depth(), "the key is now empty, so the next push starts at index 0 again")

	second := newScope(2)
	fresh := s.Push(k, second, second.done, nil)
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
			bottom := newScope(-1)
			s.Push(k, bottom, bottom.done, nil)
			// Pushed live so the stack reaches depth N, then finished —
			// pushing them done would let Push drain each one immediately.
			entries := make([]*scope, stale)
			for i := range entries {
				entries[i] = newScope(i)
				s.Push(k, entries[i], entries[i].done, nil)
			}
			for _, e := range entries {
				e.finish()
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
// under the cross-goroutine-finish pattern: a stack accumulates finished entries
// (spans whose pop never ran on this goroutine), then a single Push drains them
// all before appending the new value.
//
// The 0_reclaimed sub-benchmark is the common case (nothing to drain) and
// establishes the baseline cost of the drain loop. The N_reclaimed cases show
// the amortised cost per entry: each entry is pushed once and drained once, so
// total work is O(N) regardless of how many pushes trigger the drain.
//
// The entries are built LIVE and only then finished: pushing them done would let
// Push drain each one as the stack is built, so the stack would never reach depth
// N (this mirrors TestPushDrainsMultipleFinishedEntries).
func BenchmarkPushDrainReclaimed(b *testing.B) {
	for _, stale := range []int{0, 1, 10, 100} {
		b.Run(fmt.Sprintf("%d_reclaimed", stale), func(b *testing.B) {
			k := stackTestKey{}
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				var s contextStack
				entries := make([]*scope, stale)
				for i := range entries {
					entries[i] = newScope(i)
					s.Push(k, entries[i], entries[i].done, nil) // pushed live so the stack reaches depth N
				}
				for _, e := range entries {
					e.finish() // now finished cross-goroutine (pop never ran here)
				}
				b.StartTimer()

				// This single Push must drain all `stale` finished entries.
				last := newScope(stale)
				s.Push(k, last, last.done, nil)
			}
		})
	}
}

// TestPopEntryRemovesOnlyItsOwnEntry pins the difference from PopScope. Sweeping
// entries above the target is right for nested scopes, and wrong for a key whose
// entries are independent scopes holding their own exits: removing one does not
// cancel its exit, so that exit runs anyway against a stack it no longer owns.
func TestPopEntryRemovesOnlyItsOwnEntry(t *testing.T) {
	var s contextStack
	k := stackTestKey{}

	a := newScope(1)
	s.Push(k, a, a.done, nil)
	b := newScope(2)
	tokenB := s.Push(k, b, b.done, nil)
	c := newScope(3)
	s.Push(k, c, c.done, nil)

	require.True(t, s.PopEntry(k, tokenB), "PopEntry must find the entry its token opened")

	assert.Equal(t, 2, s.Depth(), "only B is gone; A below and C above both stay")
	assert.Same(t, c, s.Peek(k), "C was never inside B's scope, so it is still the active one")
}

// TestPopEntryLeavesAPositionalPopIntact is the reason PopEntry exists.
// internal.executionTracedKey carries bools whose exit is PopExecutionTraced, a
// positional pop. If closing the scope-exact override also swept the marker above
// it, that marker's own pop would still run and take an unrelated entry further
// down — silently flipping IsExecutionTraced for everything after it.
func TestPopEntryLeavesAPositionalPopIntact(t *testing.T) {
	var s contextStack
	k := stackTestKey{}

	a := newScope(1) // an unrelated marker nobody here will pop
	s.Push(k, a, a.done, nil)
	b := newScope(2) // the scope-exact override
	tokenB := s.Push(k, b, b.done, nil)
	c := newScope(3) // owns a positional pop
	s.Push(k, c, c.done, nil)

	require.True(t, s.PopEntry(k, tokenB))
	s.Pop(k) // C's positional exit, running after its neighbour closed

	assert.Equal(t, 1, s.Depth(), "C's pop must take C, not reach past it")
	assert.Same(t, a, s.Peek(k), "the unrelated marker below must survive")
}

// TestPopEntryIgnoresAStaleToken covers a repeated or late exit. Tokens only ever
// ascend, so a token already removed matches nothing and the walk stops early
// rather than destroying whatever now occupies that position.
func TestPopEntryIgnoresAStaleToken(t *testing.T) {
	var s contextStack
	k := stackTestKey{}

	a := newScope(1)
	tokenA := s.Push(k, a, a.done, nil)
	require.True(t, s.PopEntry(k, tokenA))
	require.Equal(t, 0, s.Depth())

	later := newScope(2)
	s.Push(k, later, later.done, nil)

	assert.False(t, s.PopEntry(k, tokenA), "a token that is already gone must match nothing")
	assert.Equal(t, 1, s.Depth(), "the unrelated scope that reused the position must survive")
	assert.Same(t, later, s.Peek(k))
	assert.False(t, s.PopEntry(k, 0), "a zero token means no scope and must never match")
}
