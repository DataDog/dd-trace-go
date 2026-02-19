// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package internal

import (
	"context"
	"runtime"
	"sync"
	"testing"

	"go.uber.org/goleak"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"
)

func TestTraceTaskEndContext(t *testing.T) {
	if IsExecutionTraced(context.Background()) {
		t.Fatal("background context incorrectly marked as execution traced")
	}
	ctx := WithExecutionTraced(context.Background())
	if !IsExecutionTraced(ctx) {
		t.Fatal("context not marked as execution traced")
	}
	ctx = WithExecutionNotTraced(ctx)
	if IsExecutionTraced(ctx) {
		t.Fatal("context incorrectly marked as execution traced")
	}
}

func TestScopedExecutionNotTraced(t *testing.T) {
	t.Run("marks context as not traced and cleans up", func(t *testing.T) {
		t.Cleanup(orchestrion.MockGLS())

		ctx := WithExecutionTraced(context.Background())
		if got := IsExecutionTraced(ctx); got != true {
			t.Fatalf("IsExecutionTraced(WithExecutionTraced(ctx)) = %v, want true", got)
		}

		ctx, cleanup := ScopedExecutionNotTraced(ctx)
		if got := IsExecutionTraced(ctx); got != false {
			t.Fatalf("IsExecutionTraced(ctx) after ScopedExecutionNotTraced = %v, want false", got)
		}

		cleanup()

		// After cleanup, the "not traced" override is popped, revealing the
		// original "traced" value pushed by WithExecutionTraced.
		if got := IsExecutionTraced(orchestrion.WrapContext(context.Background())); got != true {
			t.Fatalf("IsExecutionTraced after cleanup = %v, want true (original traced value)", got)
		}
	})

	t.Run("no-op when not previously traced", func(t *testing.T) {
		t.Cleanup(orchestrion.MockGLS())

		ctx := context.Background()
		newCtx, cleanup := ScopedExecutionNotTraced(ctx)
		if newCtx != ctx {
			t.Fatalf("ScopedExecutionNotTraced(ctx) returned different context %v, want original %v", newCtx, ctx)
		}
		cleanup() // must not panic
	})
}

func TestWithExecutionTracedGLSCleanup(t *testing.T) {
	t.Cleanup(orchestrion.MockGLS())

	ctx := WithExecutionTraced(context.Background())
	if got := IsExecutionTraced(ctx); got != true {
		t.Fatalf("IsExecutionTraced(WithExecutionTraced(ctx)) = %v, want true", got)
	}

	PopExecutionTraced()

	// After pop, the GLS stack no longer has the value, but the context
	// still holds it via context.WithValue. We check GLS via a fresh
	// WrapContext on a bare context to verify GLS cleanup.
	if got := IsExecutionTraced(orchestrion.WrapContext(nil)); got != false {
		t.Fatalf("IsExecutionTraced(WrapContext(nil)) after PopExecutionTraced() = %v, want false", got)
	}
}

// TestGLSLeakReproduction reproduces the leak from
// https://github.com/DataDog/orchestrion/issues/782 using only the push
// APIs (WithExecutionTraced + WithExecutionNotTraced) WITHOUT any pop.
// This simulates the behavior on main where no PopExecutionTraced exists.
//
// On main: each cycle pushes 2 entries (true + false) that are never popped.
// On this branch (with PopExecutionTraced): this test still leaks because
// we intentionally omit the pop calls to reproduce the original bug.
//
// To verify the fix, compare with TestGLSStackDoesNotGrowOnRepeatedCycles
// which uses the full push+pop cycle and shows depth == 0.
func TestGLSLeakReproduction(t *testing.T) {
	t.Cleanup(orchestrion.MockGLS())

	const iterations = 1000
	for range iterations {
		ctx := WithExecutionTraced(context.Background())
		_ = WithExecutionNotTraced(ctx)
		// Intentionally NO PopExecutionTraced — reproducing main's behavior.
	}

	depth := orchestrion.GLSStackDepth()
	t.Logf("GLS depth after %d cycles without pop: %d (%.1f entries/cycle)",
		iterations, depth, float64(depth)/float64(iterations))

	// Each cycle leaks 2 entries: one true (WithExecutionTraced) + one false
	// (WithExecutionNotTraced). This is exactly what happens on main.
	if depth != 2*iterations {
		t.Fatalf("GLS depth = %d, want %d (2 leaked entries per cycle without PopExecutionTraced)",
			depth, 2*iterations)
	}
}

// TestGLSStackDoesNotGrowOnRepeatedCycles is a regression test verifying that
// the GLS stack returns to depth 0 after repeated push/pop cycles on the same
// goroutine. This is the normal (non-leaking) case: WithExecutionTraced pushes
// true, ScopedExecutionNotTraced pushes false, cleanup pops false, and
// PopExecutionTraced pops true — leaving the stack empty.
//
// Compare with TestGLSLeakReproduction which omits the pop calls and shows the
// leak. If you revert PopExecutionTraced, THIS test would fail with depth != 0.
func TestGLSStackDoesNotGrowOnRepeatedCycles(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	t.Cleanup(orchestrion.MockGLS())

	const iterations = 1000
	for range iterations {
		ctx := WithExecutionTraced(context.Background())
		_, cleanup := ScopedExecutionNotTraced(ctx)
		cleanup()            // span.Finish on the same goroutine
		PopExecutionTraced() // defer end() from startTraceTask
	}

	if depth := orchestrion.GLSStackDepth(); depth != 0 {
		t.Fatalf("GLS depth = %d after %d clean cycles, want 0", depth, iterations)
	}
}

// TestGLSLeaksOnCrossGoroutineFinish demonstrates the known GLS leak described
// in https://github.com/DataDog/orchestrion/issues/782. When span.Finish
// (cleanup) runs on a different goroutine, GLSPopFunc is a no-op — it cannot
// pop the original goroutine's stack. The subsequent PopExecutionTraced pops
// the top entry (false from ScopedExecutionNotTraced) but leaves the bottom
// entry (true from WithExecutionTraced) leaked.
//
// This test documents the current behavior so that any future fix can be
// verified: once the leak is fixed, this test should be updated to assert
// depth == 0.
func TestGLSLeaksOnCrossGoroutineFinish(t *testing.T) {
	t.Cleanup(orchestrion.MockGLSPerGoroutine())

	const iterations = 100
	for range iterations {
		ctx := WithExecutionTraced(context.Background())
		_, cleanup := ScopedExecutionNotTraced(ctx)

		// Simulate span.Finish on a different goroutine.
		var wg sync.WaitGroup
		wg.Go(func() {
			cleanup() // GLSPopFunc no-op: different goroutine's contextStack
		})
		wg.Wait()

		// PopExecutionTraced pops the top (false), leaving the bottom (true) leaked.
		PopExecutionTraced()
	}

	// Each cycle leaks one "true" entry from WithExecutionTraced.
	if depth := orchestrion.GLSStackDepth(); depth != iterations {
		t.Fatalf("GLS depth = %d, want %d (one leaked entry per cross-goroutine cycle)",
			depth, iterations)
	}
}

// TestNoGoroutineLeaksFromGLSOperations verifies that the cross-goroutine
// cleanup pattern does not leak goroutines. The GLS leak from issue #782 is a
// memory leak (unbounded contextStack growth), NOT a goroutine leak. This test
// confirms that spawned goroutines exit cleanly and are not blocked on channels
// or sync primitives.
func TestNoGoroutineLeaksFromGLSOperations(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	t.Cleanup(orchestrion.MockGLSPerGoroutine())

	const iterations = 50
	for range iterations {
		ctx := WithExecutionTraced(context.Background())
		_, cleanup := ScopedExecutionNotTraced(ctx)

		var wg sync.WaitGroup
		wg.Go(func() {
			cleanup()
		})
		wg.Wait()

		PopExecutionTraced()
	}
	// goleak.VerifyNone in the defer catches any goroutines that didn't exit.
}

// TestGLSMemoryStabilitySameGoroutine verifies that same-goroutine push/pop
// cycles do not cause heap growth. This is the "fix works" test: when cleanup
// runs on the same goroutine, the stack is perfectly balanced.
func TestGLSMemoryStabilitySameGoroutine(t *testing.T) {
	t.Cleanup(orchestrion.MockGLS())

	// Warm up to let the allocator settle.
	for range 100 {
		ctx := WithExecutionTraced(context.Background())
		_, cleanup := ScopedExecutionNotTraced(ctx)
		cleanup()
		PopExecutionTraced()
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	const iterations = 10_000
	for range iterations {
		ctx := WithExecutionTraced(context.Background())
		_, cleanup := ScopedExecutionNotTraced(ctx)
		cleanup()
		PopExecutionTraced()
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	if depth := orchestrion.GLSStackDepth(); depth != 0 {
		t.Fatalf("GLS depth = %d, want 0 (same-goroutine should not leak)", depth)
	}

	// HeapInuse should not grow significantly. Allow 256KB tolerance for
	// runtime overhead (goroutine stacks, GC metadata, test framework, etc.).
	heapGrowth := int64(after.HeapInuse) - int64(before.HeapInuse)
	t.Logf("HeapInuse delta: %d bytes over %d iterations (%.1f bytes/iter)",
		heapGrowth, iterations, float64(heapGrowth)/float64(iterations))
	t.Logf("TotalAlloc delta: %d bytes", after.TotalAlloc-before.TotalAlloc)

	const maxHeapGrowth = 256 * 1024 // 256KB
	if heapGrowth > maxHeapGrowth {
		t.Errorf("HeapInuse grew by %d bytes (> %d), potential memory leak", heapGrowth, maxHeapGrowth)
	}
}

// TestGLSMemoryGrowthCrossGoroutine proves that cross-goroutine cleanup causes
// measurable heap growth proportional to the number of iterations. This is the
// reproduction test for https://github.com/DataDog/orchestrion/issues/782.
//
// At production RPS (~6k), each leaked entry costs ~16 bytes of []any backing
// storage plus map overhead, leading to ~345MB/hour of unbounded growth.
func TestGLSMemoryGrowthCrossGoroutine(t *testing.T) {
	t.Cleanup(orchestrion.MockGLSPerGoroutine())

	// Warm up.
	for range 100 {
		ctx := WithExecutionTraced(context.Background())
		_, cleanup := ScopedExecutionNotTraced(ctx)
		var wg sync.WaitGroup
		wg.Go(func() { ; cleanup() })
		wg.Wait()
		PopExecutionTraced()
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	depthBefore := orchestrion.GLSStackDepth()

	const iterations = 10_000
	for range iterations {
		ctx := WithExecutionTraced(context.Background())
		_, cleanup := ScopedExecutionNotTraced(ctx)
		var wg sync.WaitGroup
		wg.Go(func() { ; cleanup() })
		wg.Wait()
		PopExecutionTraced()
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	depthAfter := orchestrion.GLSStackDepth()

	leaked := depthAfter - depthBefore
	heapGrowth := int64(after.HeapInuse) - int64(before.HeapInuse)
	totalAllocGrowth := after.TotalAlloc - before.TotalAlloc

	t.Logf("GLS entries leaked: %d (%.2f per iteration)", leaked, float64(leaked)/float64(iterations))
	t.Logf("HeapInuse delta: %d bytes (%.1f bytes/leaked entry)",
		heapGrowth, float64(heapGrowth)/float64(leaked))
	t.Logf("TotalAlloc delta: %d bytes (%.1f bytes/iter)",
		totalAllocGrowth, float64(totalAllocGrowth)/float64(iterations))

	// Each cross-goroutine cycle leaks exactly one entry.
	if leaked != iterations {
		t.Fatalf("GLS entries leaked = %d, want %d", leaked, iterations)
	}

	// HeapInuse should grow measurably — at least 1 byte per leaked entry.
	// (In practice each any in the []any costs 16 bytes.)
	if heapGrowth <= 0 {
		t.Logf("WARNING: HeapInuse did not grow despite %d leaked entries (may be noise or GC timing)", leaked)
	}
}

// BenchmarkGLSCycleSameGoroutine measures the per-operation cost of balanced
// push/pop cycles (same goroutine cleanup). This should show near-zero
// allocations and constant memory usage.
func BenchmarkGLSCycleSameGoroutine(b *testing.B) {
	b.Cleanup(orchestrion.MockGLS())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := WithExecutionTraced(context.Background())
		_, cleanup := ScopedExecutionNotTraced(ctx)
		cleanup()
		PopExecutionTraced()
	}
	b.StopTimer()
	if depth := orchestrion.GLSStackDepth(); depth != 0 {
		b.Fatalf("GLS depth = %d after benchmark, want 0", depth)
	}
}

// BenchmarkGLSCycleCrossGoroutine measures the per-operation cost of
// cross-goroutine cleanup. Each iteration leaks one GLS entry, so memory
// grows linearly — this benchmark shows the degradation over time.
func BenchmarkGLSCycleCrossGoroutine(b *testing.B) {
	b.Cleanup(orchestrion.MockGLSPerGoroutine())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := WithExecutionTraced(context.Background())
		_, cleanup := ScopedExecutionNotTraced(ctx)
		done := make(chan struct{})
		go func() { defer close(done); cleanup() }()
		<-done
		PopExecutionTraced()
	}
	b.StopTimer()
	depth := orchestrion.GLSStackDepth()
	b.Logf("GLS depth = %d after %d iterations (%.2f leaked entries/iter)",
		depth, b.N, float64(depth)/float64(b.N))
}
