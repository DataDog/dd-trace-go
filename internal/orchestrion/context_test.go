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

		CtxWithValue(context.Background(), key("k"), "v")
		popFn := GLSPopFunc(key("k"))

		require.Equal(t, "v", getDDContextStack().Peek(key("k")))

		popFn()

		require.Nil(t, getDDContextStack().Peek(key("k")))
	})

	t.Run("different goroutine is no-op", func(t *testing.T) {
		t.Cleanup(MockGLS())

		CtxWithValue(context.Background(), key("k"), "v")
		popFn := GLSPopFunc(key("k"))

		// Simulate a different goroutine by swapping the GLS to a new stack.
		// In production, each goroutine has its own contextStack pointer in
		// runtime.g, so getDDContextStack() returns different pointers.
		originalStack := getDDGLS()
		differentStack := contextStack(make(map[any][]stackEntry))
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

		popFn := GLSPopFunc(key("k"))
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
		require.NotNil(t, done.ptr.Load(), "done cell should be allocated")

		(*fn)()
		require.Nil(t, getDDContextStack().Peek(key("k")), "popper should remove the value")
	})

	t.Run("re-activation of live span shares the same cell (no supersede)", func(t *testing.T) {
		t.Cleanup(MockGLS())

		var pop GLSPopperCell
		var done GLSDoneCell
		GLSActivate(nil, key("k"), "v1", &pop, &done)
		first := pop.ptr.Load()
		cell1 := done.ptr.Load()
		// Second activation of the same still-live span (e.g. propagated to
		// another goroutine before Finish). The cell must be REUSED, not replaced,
		// and the previous entry must NOT be marked done — marking a live entry
		// would let the next Push drop it, breaking cross-goroutine live
		// propagation (orchestrion#782 review). So both entries stay live and the
		// stack grows to 2.
		GLSActivate(nil, key("k"), "v2", &pop, &done)
		require.Equal(t, 2, getDDContextStack().Depth(), "re-activation keeps both live entries")
		require.Same(t, cell1, done.ptr.Load(), "the cell is reused, not replaced")
		require.False(t, cell1.Load(), "the live cell must not be marked done on re-activation")
		require.Same(t, first, pop.ptr.Load(),
			"the first popper must be retained across re-activation")
	})

	t.Run("re-activation of already-finished span reuses existing cell", func(t *testing.T) {
		t.Cleanup(MockGLS())

		var pop GLSPopperCell
		var done GLSDoneCell
		// Simulate: span was pushed and finished by a different goroutine
		// (GLSActivate + GLSDeactivate). The cell is already true.
		cell := new(atomic.Bool)
		cell.Store(true)
		done.ptr.Store(cell)

		GLSActivate(nil, key("k"), "v1", &pop, &done)
		// The existing true cell must be reused so the stack entry is immediately
		// drain-eligible on the next Push for the same key.
		require.Same(t, cell, done.ptr.Load(), "existing true cell must be reused")
		require.Equal(t, 1, getDDContextStack().Depth(), "entry pushed")

		// Second activation on the same key: drain removes the just-pushed entry.
		GLSActivate(nil, key("k"), "v2", &pop, nil)
		require.Equal(t, 1, getDDContextStack().Depth(), "drain must remove the immediately-eligible entry")
		require.Equal(t, "v2", getDDContextStack().Peek(key("k")), "v2 is the live entry")
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

	t.Run("done=nil is a no-op for the cell (dyngo path)", func(t *testing.T) {
		t.Cleanup(MockGLS())

		var pop GLSPopperCell
		GLSActivate(nil, key("k"), "v", &pop, nil) // must not panic, no cell needed
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

func TestGLSReset(t *testing.T) {
	t.Run("clears done cell and popper without running the popper", func(t *testing.T) {
		var done GLSDoneCell
		cell := new(atomic.Bool)
		cell.Store(true)
		done.ptr.Store(cell)
		ran := 0
		var pop GLSPopperCell
		fn := GLSPopper(func() { ran++ })
		pop.ptr.Store(&fn)

		GLSReset(&done, &pop)
		require.Nil(t, done.ptr.Load(), "done cell pointer must be cleared (span's reference dropped)")
		// The underlying cell is unchanged — stack entries holding it still see true.
		require.True(t, cell.Load(), "original cell must still be true (stack entry keeps its ref)")
		require.Nil(t, pop.ptr.Load(), "popper must be cleared without being run")
		require.Equal(t, 0, ran, "GLSReset must not run the popper")
	})

	t.Run("tolerates nil done (dyngo operations)", func(t *testing.T) {
		var pop GLSPopperCell
		fn := GLSPopper(func() {})
		pop.ptr.Store(&fn)
		GLSReset(nil, &pop) // must not panic
		require.Nil(t, pop.ptr.Load())
	})
}

func TestGLSDeactivate(t *testing.T) {
	t.Run("marks done cell true and runs the popper once", func(t *testing.T) {
		var done GLSDoneCell
		cell := new(atomic.Bool)
		done.ptr.Store(cell)
		popped := 0
		var pop GLSPopperCell
		fn := GLSPopper(func() { popped++ })
		pop.ptr.Store(&fn)

		GLSDeactivate(&done, &pop)
		require.True(t, cell.Load(), "done cell should be set to true on finish")
		require.Equal(t, 1, popped, "popper should run once")
		require.Nil(t, pop.ptr.Load(), "popper should be cleared after running")

		GLSDeactivate(&done, &pop) // second finish: popper already nil
		require.Equal(t, 1, popped, "popper must not run again on a repeated finish")
	})

	t.Run("creates and pre-marks a cell when Finish runs before ContextWithSpan", func(t *testing.T) {
		// In the korECM cross-goroutine pattern (orchestrion#782), Finish is called
		// on the span BEFORE ContextWithSpan. done.ptr is nil at the time of Finish.
		// GLSDeactivate must create a pre-marked cell so GLSActivate can find and
		// reuse it, making the resulting stack entry immediately drain-eligible.
		var done GLSDoneCell // ptr is nil: no prior GLSActivate
		var pop GLSPopperCell

		GLSDeactivate(&done, &pop)
		cell := done.ptr.Load()
		require.NotNil(t, cell, "GLSDeactivate must create a cell when none exists")
		require.True(t, cell.Load(), "pre-created cell must already be true")
	})

	t.Run("tolerates nil done and nil pointers", func(t *testing.T) {
		var pop GLSPopperCell // empty: nil inner pointer

		GLSDeactivate(nil, &pop) // no done, no popper -> no invoke, no panic

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
	CtxWithValue(context.Background(), key("k"), "main-val")
	popFn := GLSPopFunc(key("k"))

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
		getDDContextStack().Push(k, true, nil)
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
		getDDContextStack().Push(k, true, nil)
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
		CtxWithValue(context.Background(), k, true)
		popFn := GLSPopFunc(k)
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
		CtxWithValue(context.Background(), k, true)
		popFn := GLSPopFunc(k)
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
				getDDContextStack().Push(k, true, nil)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				getDDContextStack().Peek(k)
			}
		})
	}
}

// BenchmarkGLSActivate measures the per-span GLS lifecycle cost as woven into
// the tracer: GLSActivate (ContextWithSpan) + GLSDeactivate (Finish) + GLSReset
// (clear). Running reset every iteration mirrors how the span pool reuses a span,
// which is the scenario this decouple design unlocks. The reported allocs/op are
// the GLS-helper overhead per pooled-span reuse: one closure for the popper
// (re-captured because reset cleared it) and one *atomic.Bool liveness cell.
// The cell is the cost the cell-based reclaim adds over reading a mutable flag
// off the span; it is paid only under orchestrion.
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
