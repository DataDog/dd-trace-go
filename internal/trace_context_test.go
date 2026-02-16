// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package internal

import (
	"context"
	"testing"

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
