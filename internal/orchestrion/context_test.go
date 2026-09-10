// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

type key string

func TestFromGLS(t *testing.T) {
	t.Cleanup(MockGLS())

	t.Run("Enabled() is false, ctx is nil", func(t *testing.T) {
		enabled = false
		require.Equal(t, nil, WrapContext(nil))
	})

	t.Run("Enabled() is false, ctx is not nil", func(t *testing.T) {
		enabled = false
		require.Equal(t, context.Background(), WrapContext(context.Background()))

	})

	t.Run("Enabled() is true, ctx is nil", func(t *testing.T) {
		enabled = true
		require.Equal(t, &glsContext{context.Background()}, WrapContext(nil))
	})

	t.Run("Enabled() is true, ctx is not nil", func(t *testing.T) {
		enabled = true
		ctx := context.WithValue(context.Background(), key("key"), "value")
		require.Equal(t, &glsContext{ctx}, WrapContext(ctx))
	})
}

func TestGLSPopFunc(t *testing.T) {
	t.Run("same goroutine pops value", func(t *testing.T) {
		t.Cleanup(MockGLS())

		token := getDDContextStack().Push(key("k"), "v", nil, nil)
		popFn := GLSPopFunc(key("k"), token)

		require.Equal(t, "v", getDDContextStack().Peek(key("k")))

		popFn()

		require.Nil(t, getDDContextStack().Peek(key("k")))
	})

	t.Run("different goroutine is no-op", func(t *testing.T) {
		t.Cleanup(MockGLS())

		token := getDDContextStack().Push(key("k"), "v", nil, nil)
		popFn := GLSPopFunc(key("k"), token)

		// Simulate a different goroutine by swapping the GLS to a new stack.
		// In production, each goroutine has its own contextStack pointer in
		// runtime.g, so getDDContextStack() returns different pointers.
		originalStack := getDDGLS()
		var differentStack contextStack
		setDDGLS(&differentStack)
		t.Cleanup(func() { setDDGLS(originalStack) })

		popFn()

		// Restore the original stack and verify the value was NOT popped.
		setDDGLS(originalStack)
		require.Equal(t, "v", getDDContextStack().Peek(key("k")),
			"value should not be popped when called from different goroutine")
	})

	t.Run("disabled orchestrion returns no-op", func(t *testing.T) {
		t.Cleanup(MockGLS())
		enabled = false // Override MockGLS's enabled=true to test disabled path

		popFn := GLSPopFunc(key("k"), 0)
		popFn() // must not panic
	})
}

func TestGLSActivate(t *testing.T) {
	t.Run("pushes and captures a working popper", func(t *testing.T) {
		t.Cleanup(MockGLS())

		var pop GLSPopperCell
		var done GLSDoneCell
		GLSActivate(nil, key("k"), "v", &pop, &done)
		require.Equal(t, "v", getDDContextStack().Peek(key("k")), "value should be on the GLS stack")
		fn := pop.ptr.Load()
		require.NotNil(t, fn, "popper should be captured")
		cell := done.ptr.Load()
		require.NotNil(t, cell, "liveness cell should be allocated")
		require.False(t, cell.Load(), "a fresh activation is live")

		fn.pop()
		require.Nil(t, getDDContextStack().Peek(key("k")), "popper should remove the value")
	})

	t.Run("first activation wins: popper is not overwritten", func(t *testing.T) {
		t.Cleanup(MockGLS())

		var pop GLSPopperCell
		var done GLSDoneCell
		GLSActivate(nil, key("k"), "v1", &pop, &done)
		first := pop.ptr.Load()
		GLSActivate(nil, key("k"), "v2", &pop, &done) // re-activate same field
		require.Equal(t, 2, getDDContextStack().Depth(), "every activation pushes")
		require.Same(t, first, pop.ptr.Load(),
			"the first popper must be retained across re-activation")
	})

	// A live span re-activated on a second goroutine must keep the cell it already
	// has, and that cell must stay false. Allocating a fresh one and marking the
	// old one done would be the tempting way to keep depth at 1, and it is wrong:
	// the previous entry is still LIVE, so marking it makes the first goroutine's
	// next Push drop it, and once a child scope closes the GLS no longer restores
	// the unfinished parent. That is what moved kafka.consume off its produce
	// parent in the twmb_franz_go integration test.
	t.Run("re-activation of a live span shares its cell and marks nothing", func(t *testing.T) {
		t.Cleanup(MockGLS())

		var pop GLSPopperCell
		var done GLSDoneCell
		GLSActivate(nil, key("k"), "v1", &pop, &done)
		first := pop.ptr.Load()
		cell := done.ptr.Load()

		GLSActivate(nil, key("k"), "v2", &pop, &done)

		require.Same(t, cell, done.ptr.Load(), "the cell is reused, not replaced")
		require.False(t, cell.Load(), "a live scope must not be marked done by a re-activation")
		require.Equal(t, 2, getDDContextStack().Depth(), "both live entries stay on the stack")
		require.Same(t, first, pop.ptr.Load(),
			"the first popper must be retained across re-activation")

		// One cell for the whole lifecycle means one finish marks every entry the
		// span pushed, so the drain reclaims them together.
		GLSDeactivate(&done, nil)
		require.Nil(t, getDDContextStack().Peek(key("k")),
			"finishing once must retire every entry the span pushed")
	})

	// The cross-goroutine order: the owner finished the span before the worker put
	// it into a context. GLSDeactivate left a pre-marked cell, and reusing it is
	// what makes the entry drain-eligible the moment it lands. A fresh false cell
	// would never be marked — one leaked entry per record, which is the shape of
	// the original report.
	t.Run("activation after finish reuses the marked cell", func(t *testing.T) {
		t.Cleanup(MockGLS())

		var pop GLSPopperCell
		var done GLSDoneCell
		GLSDeactivate(&done, &pop) // finish first, before any activation
		marked := done.ptr.Load()
		require.NotNil(t, marked)

		GLSActivate(nil, key("k"), "v1", &pop, &done)
		require.Same(t, marked, done.ptr.Load(), "the marked cell must be reused")
		require.Equal(t, 1, getDDContextStack().Depth(), "the entry is pushed")
		require.Nil(t, getDDContextStack().Peek(key("k")),
			"an already-finished span must not be readable as the active scope")

		// The next push on this key drains it rather than stacking on top.
		GLSActivate(nil, key("k"), "v2", &pop, nil)
		require.Equal(t, 1, getDDContextStack().Depth(), "the eligible entry is drained, not buried")
		require.Equal(t, "v2", getDDContextStack().Peek(key("k")))
	})

	// After the pool recycles a span, GLSReset has left done nil, so the next
	// activation allocates a fresh cell. Without that, the span would inherit the
	// previous lifecycle's marked cell and its new entry would be drained
	// immediately — a live scope treated as finished.
	t.Run("activation after reset allocates a fresh live cell", func(t *testing.T) {
		t.Cleanup(MockGLS())

		var pop GLSPopperCell
		var done GLSDoneCell
		GLSActivate(nil, key("k"), "v1", &pop, &done)
		GLSDeactivate(&done, &pop)
		retired := done.ptr.Load()
		require.True(t, retired.Load())

		GLSReset(&done, &pop) // clear(): the span goes back to the pool
		GLSActivate(nil, key("k"), "v2", &pop, &done)

		fresh := done.ptr.Load()
		require.NotSame(t, retired, fresh, "a recycled span must not inherit the retired cell")
		require.False(t, fresh.Load(), "the new lifecycle starts live")
		require.Equal(t, "v2", getDDContextStack().Peek(key("k")),
			"the reused span's new scope must be readable")
	})

	t.Run("ctxp non-nil wraps the parent so the result is GLS-aware", func(t *testing.T) {
		t.Cleanup(MockGLS())

		ctx := context.Background()
		var pop GLSPopperCell
		var done GLSDoneCell
		GLSActivate(&ctx, key("k"), "v", &pop, &done)
		_, ok := ctx.(*glsContext)
		require.True(t, ok, "ctxp should be wrapped in a glsContext")
	})

	t.Run("done=nil pushes an entry with no cell (dyngo path)", func(t *testing.T) {
		t.Cleanup(MockGLS())

		var pop GLSPopperCell
		GLSActivate(nil, key("k"), "v", &pop, nil) // must not panic
		require.Equal(t, "v", getDDContextStack().Peek(key("k")), "value pushed")
		require.NotNil(t, pop.ptr.Load(), "popper still captured")
	})

	t.Run("disabled orchestrion is a no-op", func(t *testing.T) {
		t.Cleanup(MockGLS())
		enabled = false // exercise the !Enabled() early return

		ctx := context.Background()
		var pop GLSPopperCell
		var done GLSDoneCell
		GLSActivate(&ctx, key("k"), "v", &pop, &done) // must not panic
		require.Nil(t, pop.ptr.Load(), "no popper captured when disabled")
		require.Nil(t, done.ptr.Load(), "no cell allocated when disabled")
		require.Equal(t, context.Background(), ctx, "ctx unchanged when disabled")
	})
}

// TestGLSActivateConcurrentFirstActivation pins the CompareAndSwap in
// GLSActivate. Two goroutines activating the same span for the first time must
// push entries carrying the SAME cell. If each installed its own, the span's
// single Finish would mark only one of them and the other goroutine's entry would
// never be drained — one leaked entry per concurrent first activation.
//
// What is asserted is the cell each activation actually handed to Push, read back
// from that goroutine's own stack. Re-reading done.ptr afterwards would not do:
// with a last-write-wins Store both goroutines end up loading whichever cell
// landed second, so the assertion would hold while the entries still disagreed.
func TestGLSActivateConcurrentFirstActivation(t *testing.T) {
	k := key("k")

	for range 200 {
		cleanup := MockGLSPerGoroutine()

		var pop GLSPopperCell
		var done GLSDoneCell
		pushed := make([]*atomic.Bool, 2)

		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range pushed {
			wg.Go(func() {
				<-start
				GLSActivate(nil, k, "v", &pop, &done)
				// Each goroutine has its own stack here, so this is the entry this
				// activation just pushed.
				stack := getDDContextStack().stacks[k]
				if assert.Len(t, stack, 1, "this goroutine pushed exactly one entry") {
					pushed[i] = stack[0].done
				}
			})
		}
		close(start)
		wg.Wait()

		require.NotNil(t, pushed[0], "first activation captured a cell")
		require.NotNil(t, pushed[1], "second activation captured a cell")
		require.Same(t, pushed[0], pushed[1],
			"concurrent first activations must push entries sharing one cell, so the span's "+
				"single Finish retires both")

		cleanup()
	}
}

func TestGLSReset(t *testing.T) {
	// The cell must survive the reset. This is the whole reason the span holds a
	// pointer to it rather than the bit itself: clear() runs while stack entries
	// from the finished lifecycle are still around, and they need to keep seeing
	// the finish. Storing false here — which is what the flag-on-the-span design
	// did — is the ABA that made the span pool unsafe under orchestrion.
	t.Run("drops the span's reference without disturbing the cell", func(t *testing.T) {
		var done GLSDoneCell
		cell := new(atomic.Bool)
		cell.Store(true)
		done.ptr.Store(cell)
		ran := 0
		var pop GLSPopperCell
		fn := GLSPopper(func() { ran++ })
		pop.ptr.Store(&glsExit{pop: fn})

		GLSReset(&done, &pop)
		require.Nil(t, done.ptr.Load(), "the span's reference to the cell must be cleared")
		require.True(t, cell.Load(),
			"the cell itself must stay marked — stack entries still hold it")
		require.Nil(t, pop.ptr.Load(), "popper must be cleared without being run")
		require.Equal(t, 0, ran, "GLSReset must not run the popper")
	})

	t.Run("tolerates nil done (dyngo operations)", func(t *testing.T) {
		var pop GLSPopperCell
		fn := GLSPopper(func() {})
		pop.ptr.Store(&glsExit{pop: fn})
		GLSReset(nil, &pop) // must not panic
		require.Nil(t, pop.ptr.Load())
	})
}

func TestGLSDeactivate(t *testing.T) {
	t.Run("marks the cell and runs the popper once", func(t *testing.T) {
		var done GLSDoneCell
		cell := new(atomic.Bool)
		done.ptr.Store(cell)
		popped := 0
		var pop GLSPopperCell
		fn := GLSPopper(func() { popped++ })
		pop.ptr.Store(&glsExit{pop: fn})

		GLSDeactivate(&done, &pop)
		require.True(t, cell.Load(), "the span's cell should be marked done on finish")
		require.Equal(t, 1, popped, "popper should run once")
		require.Nil(t, pop.ptr.Load(), "popper should be cleared after running")

		GLSDeactivate(&done, &pop) // second finish: popper already nil
		require.Equal(t, 1, popped, "popper must not run again on a repeated finish")
		require.True(t, cell.Load(), "a repeated finish leaves the cell marked")
	})

	// Finish before the span ever reaches a context: there is no cell yet, so one
	// is created already marked. The activation that follows reuses it (see
	// TestGLSActivate) and its entry is drain-eligible on arrival, which is what
	// bounds the stack in the cross-goroutine pattern from orchestrion#782.
	t.Run("creates a marked cell when finish precedes activation", func(t *testing.T) {
		var done GLSDoneCell // nil: no prior GLSActivate
		var pop GLSPopperCell

		GLSDeactivate(&done, &pop)
		cell := done.ptr.Load()
		require.NotNil(t, cell, "GLSDeactivate must create a cell when none exists")
		require.True(t, cell.Load(), "the created cell must already be marked")
	})

	t.Run("tolerates nil done and nil pointers", func(t *testing.T) {
		var pop GLSPopperCell // empty: nil inner pointer

		GLSDeactivate(nil, &pop) // no cell, no popper -> no invoke, no panic

		GLSDeactivate(nil, nil) // must not panic
	})
}

func TestCtxWithValue(t *testing.T) {
	t.Cleanup(MockGLS())

	t.Run("orchestrion disabled", func(t *testing.T) {
		enabled = false
		require.Equal(t, context.WithValue(context.Background(), key("key"), "value"), CtxWithValue(context.Background(), key("key"), "value"))
	})

	t.Run("orchestrion enabled", func(t *testing.T) {
		enabled = true
		ctx := CtxWithValue(context.Background(), key("key"), "value")
		require.Equal(t, context.WithValue(&glsContext{context.Background()}, key("key"), "value"), ctx)
		require.Equal(t, "value", ctx.Value(key("key")))
		require.Equal(t, "value", getDDContextStack().Peek(key("key")))
		require.Equal(t, "value", GLSPopValue(key("key")))
		require.Nil(t, getDDContextStack().Peek(key("key")))
	})

	t.Run("cross-goroutine switch", func(t *testing.T) {
		enabled = true
		ctx := CtxWithValue(context.Background(), key("key"), "value")
		var wg sync.WaitGroup
		wg.Go(func() {
			// Use assert (not require) from a non-test goroutine to avoid
			// calling t.FailNow which panics outside the test goroutine.
			assert.Equal(t, "value", ctx.Value(key("key")))
		})
		wg.Wait()
	})
}

func TestGLSPopFuncCrossGoroutine(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	t.Cleanup(MockGLSPerGoroutine())

	// Push a value and capture the pop function on the main goroutine.
	_, popFn := CtxWithScopedValue(context.Background(), key("k"), "main-val")

	require.Equal(t, "main-val", getDDContextStack().Peek(key("k")),
		"main goroutine should see its pushed value")

	// Call popFn from a spawned goroutine — it should be a no-op because
	// the spawned goroutine has a different contextStack pointer.
	var wg sync.WaitGroup
	wg.Go(func() {
		popFn()
		// The spawned goroutine should have an empty (nil) stack.
		assert.Equal(t, 0, GLSStackDepth(),
			"spawned goroutine should have empty GLS stack")
	})
	wg.Wait()

	// Back on the main goroutine, the value should NOT have been popped.
	require.Equal(t, "main-val", getDDContextStack().Peek(key("k")),
		"main goroutine value must survive cross-goroutine pop attempt")
	require.Equal(t, 1, GLSStackDepth(),
		"main goroutine GLS depth should still be 1")

	// Clean up: pop on the correct goroutine.
	GLSPopValue(key("k"))
}

func TestGLSStackDepth(t *testing.T) {
	t.Cleanup(MockGLS())

	require.Equal(t, 0, GLSStackDepth(), "empty stack should have depth 0")

	CtxWithValue(context.Background(), key("a"), "v1")
	require.Equal(t, 1, GLSStackDepth())

	CtxWithValue(context.Background(), key("b"), "v2")
	require.Equal(t, 2, GLSStackDepth())

	// Push another value for the same key.
	CtxWithValue(context.Background(), key("a"), "v3")
	require.Equal(t, 3, GLSStackDepth())

	GLSPopValue(key("a"))
	require.Equal(t, 2, GLSStackDepth())

	GLSPopValue(key("a"))
	GLSPopValue(key("b"))
	require.Equal(t, 0, GLSStackDepth(), "stack should be empty after popping all values")
}

// BenchmarkContextStackPushPop measures the cost of balanced push/pop cycles.
// At steady state the backing slice is reused, so allocations should be near zero.
func BenchmarkContextStackPushPop(b *testing.B) {
	b.Cleanup(MockGLS())
	k := key("bench")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		getDDContextStack().Push(k, true, nil, nil)
		getDDContextStack().Pop(k)
	}
	if depth := GLSStackDepth(); depth != 0 {
		b.Fatalf("depth = %d after balanced push/pop, want 0", depth)
	}
}

// BenchmarkContextStackPushOnly measures the cost of unbalanced pushes (no pop).
// This simulates the leak pattern: memory grows linearly with b.N.
func BenchmarkContextStackPushOnly(b *testing.B) {
	b.Cleanup(MockGLS())
	k := key("bench")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		getDDContextStack().Push(k, true, nil, nil)
	}
	b.StopTimer()
	depth := GLSStackDepth()
	b.Logf("depth after %d unbalanced pushes: %d", b.N, depth)
	if depth != b.N {
		b.Fatalf("depth = %d, want %d", depth, b.N)
	}
}

// BenchmarkGLSPopFuncSameGoroutine measures GLSPopFunc cost when called from
// the same goroutine (the pop actually executes).
func BenchmarkGLSPopFuncSameGoroutine(b *testing.B) {
	b.Cleanup(MockGLS())
	k := key("bench")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, popFn := CtxWithScopedValue(context.Background(), k, true)
		popFn()
	}
	if depth := GLSStackDepth(); depth != 0 {
		b.Fatalf("depth = %d, want 0", depth)
	}
}

// BenchmarkGLSPopFuncCrossGoroutine measures GLSPopFunc cost when called from
// a different goroutine (the pop is a no-op, so entries leak).
func BenchmarkGLSPopFuncCrossGoroutine(b *testing.B) {
	b.Cleanup(MockGLSPerGoroutine())
	k := key("bench")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, popFn := CtxWithScopedValue(context.Background(), k, true)
		done := make(chan struct{})
		go func() { defer close(done); popFn() }()
		<-done
	}
	b.StopTimer()
	depth := GLSStackDepth()
	b.Logf("depth after %d cross-goroutine pops: %d (%.2f leaked/iter)",
		b.N, depth, float64(depth)/float64(b.N))
	if depth != b.N {
		b.Fatalf("depth = %d, want %d (one leak per iteration)", depth, b.N)
	}
}

// BenchmarkContextStackDepthScaling measures Peek/Push performance as the
// stack grows, showing the impact of a leaked stack on hot-path operations.
func BenchmarkContextStackDepthScaling(b *testing.B) {
	for _, depth := range []int{0, 100, 1000, 10000} {
		b.Run(fmt.Sprintf("depth=%d", depth), func(b *testing.B) {
			b.Cleanup(MockGLS())
			k := key("bench")
			// Pre-fill the stack to simulate leaked entries.
			for range depth {
				getDDContextStack().Push(k, true, nil, nil)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				getDDContextStack().Peek(k)
			}
		})
	}
}

// BenchmarkGLSActivate measures the per-span GLS lifecycle as the tracer weaves
// it: GLSActivate (ContextWithSpan) + GLSDeactivate (Finish) + GLSReset (clear).
// Resetting every iteration mirrors the span pool recycling a span, which is the
// case this design unlocks, and is what makes the next iteration allocate again:
// reset clears both the popper and the cell, so the reported allocs/op are one
// popper closure plus one *atomic.Bool.
//
// The cell is the price of decoupling the liveness signal from the span, paid
// once per pooled reuse and only under orchestrion. A build without orchestrion
// takes the !Enabled() branch and allocates nothing.
func BenchmarkGLSActivate(b *testing.B) {
	b.Cleanup(MockGLS())
	k := key("bench")
	var pop GLSPopperCell
	var done GLSDoneCell
	b.ReportAllocs()
	for b.Loop() {
		GLSActivate(nil, k, "v", &pop, &done)
		GLSDeactivate(&done, &pop)
		GLSReset(&done, &pop)
	}
	if d := GLSStackDepth(); d != 0 {
		b.Fatalf("GLS depth = %d after balanced activate/deactivate, want 0", d)
	}
}

// TestGLSDeactivateBeforeActivationSharesOneDoneCell pins the shared sentinel.
// Under orchestrion every span that finishes without ever reaching
// ContextWithSpan lands on this path, so allocating a cell here would cost one
// heap object per StartSpan/Finish pair on exactly the high-throughput workloads
// the span pool is meant to serve. The state needed is immutable — "already
// done" — so one cell serves all of them.
func TestGLSDeactivateBeforeActivationSharesOneDoneCell(t *testing.T) {
	t.Cleanup(MockGLS())

	var first, second GLSDoneCell
	GLSDeactivate(&first, nil)
	GLSDeactivate(&second, nil)

	c1, c2 := first.ptr.Load(), second.ptr.Load()
	require.NotNil(t, c1, "a finish with no prior activation must still leave a marked cell")
	assert.Same(t, c1, c2, "two spans finished before activation must share one cell, not allocate two")
	assert.True(t, c1.Load(), "the shared cell must read done, so the activation that follows is drain-eligible")
}

// TestGLSActivateAfterResetGetsItsOwnLiveCell is the other half of sharing the
// sentinel: it must not leak across a recycle. GLSReset clears the pointer when
// the pool takes the span back, and the next lifecycle has to start live —
// adopting a cell that already reads done would make its first entry
// drain-eligible on arrival and drop a scope that never ended.
func TestGLSActivateAfterResetGetsItsOwnLiveCell(t *testing.T) {
	t.Cleanup(MockGLS())

	var done GLSDoneCell
	GLSDeactivate(&done, nil) // finished before ever being activated
	require.Same(t, doneSentinel, done.ptr.Load(), "expected the shared already-done cell")

	GLSReset(&done, nil) // the pool recycles the span
	GLSActivate(nil, key("recycled"), "v", nil, &done)

	cell := done.ptr.Load()
	require.NotNil(t, cell, "activation must always leave a cell: a nil one is permanently live")
	assert.NotSame(t, doneSentinel, cell, "a recycled span must not inherit the previous lifecycle's done state")
	assert.False(t, cell.Load(), "the new lifecycle's scope is live until its own finish")
}

// TestGLSReactivationAfterSweepStrandsACellessEntry probes the remaining Codex
// finding: PopScope sweeps live entries above its target without touching their
// popper cells, so a value activated again afterwards keeps a closure aimed at a
// token that no longer exists, and its real scope is never closed.
//
// The shape here is dyngo's: a popper but no liveness cell, because AppSec
// operations are registered and finished on one goroutine. That is exactly what
// makes it matter — with no cell, entry.isDone is always false, so a stranded
// entry is never drained by Push and never skipped by Peek. A span in the same
// position self-heals, since GLSDeactivate marks its cell and the stranded entry
// becomes reclaimable.
func TestGLSReactivationAfterSweepStrandsACellessEntry(t *testing.T) {
	t.Cleanup(MockGLS())
	k := key("celless")

	var popA, popB GLSPopperCell
	GLSActivate(nil, k, "A", &popA, nil)
	GLSActivate(nil, k, "B", &popB, nil)
	require.Equal(t, 2, GLSStackDepth())

	// A exits while B is still live: the non-LIFO case PopScope exists for. It
	// removes A and sweeps B along with it.
	GLSDeactivate(nil, &popA)
	require.Equal(t, 0, GLSStackDepth(), "A's scope exit removes A and what was opened inside it")

	// B is activated again on the same goroutine. first-wins keeps the popper
	// captured the first time, which names the token just swept away.
	GLSActivate(nil, k, "B", &popB, nil)
	require.Equal(t, 1, GLSStackDepth())

	// B finishes. Its exit targets the stale token and matches nothing.
	GLSDeactivate(nil, &popB)

	assert.Equal(t, 0, GLSStackDepth(),
		"B's entry outlived B: its popper still named the token PopScope swept, so the exit "+
			"removed nothing. With no liveness cell the entry is permanently live, so Peek keeps "+
			"handing it out and Push never drains it")
}

// TestGLSSweepKeepsASurvivingEntrysExit covers the case where invalidating a
// removed entry's exit would discard one that still has work to do.
//
// Activate B, then A, then B again. Both B entries share one GLSPopperCell, and
// first-wins means the exit it holds names the LOWER B. Closing A sweeps A and the
// upper B, so the upper B's entry is removed — but the cell it points at is still
// the lower B's only way out. Clearing it unconditionally strands that entry, and
// with no liveness cell (dyngo's shape) it stays permanently active.
func TestGLSSweepKeepsASurvivingEntrysExit(t *testing.T) {
	t.Cleanup(MockGLS())
	k := key("celless")

	var popA, popB GLSPopperCell
	GLSActivate(nil, k, "B", &popB, nil) // lower B: popB's exit names this token
	GLSActivate(nil, k, "A", &popA, nil)
	GLSActivate(nil, k, "B", &popB, nil) // upper B: first-wins keeps the lower token
	require.Equal(t, 3, GLSStackDepth())

	GLSDeactivate(nil, &popA) // sweeps A and the upper B; the lower B survives
	require.Equal(t, 1, GLSStackDepth(), "A's scope exit removes A and what was opened inside it")

	GLSDeactivate(nil, &popB) // must still close the surviving lower B
	assert.Equal(t, 0, GLSStackDepth(),
		"the lower B outlived its own exit: sweeping the upper B cleared the cell whose exit "+
			"named the lower one, so B's finish removed nothing")
}
