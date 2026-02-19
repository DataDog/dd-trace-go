// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"context"
	"fmt"
	"sync"
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
		differentStack := contextStack(make(map[any][]any))
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
	for i := 0; i < b.N; i++ {
		getDDContextStack().Push(k, true)
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
	for i := 0; i < b.N; i++ {
		getDDContextStack().Push(k, true)
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
	for i := 0; i < b.N; i++ {
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
	for i := 0; i < b.N; i++ {
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
				getDDContextStack().Push(k, true)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				getDDContextStack().Peek(k)
			}
		})
	}
}
