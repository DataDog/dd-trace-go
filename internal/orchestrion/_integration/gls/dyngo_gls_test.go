// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package gls

import (
	"context"
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"

	"github.com/stretchr/testify/require"
)

// AppSec's dyngo is GLS-agnostic in source; orchestrion weaves its GLS push/
// read/pop (internal/orchestrion/gls.orchestrion.yml). These tests run under
// `orchestrion go test` and fail if that injection regresses — the dyngo
// equivalent of the span GLS regression tests.

type dyngoOp struct{ dyngo.Operation }

type (
	dyngoArgs struct{}
	dyngoRes  struct{}
)

func (dyngoArgs) IsArgOf(dyngoOp)   {}
func (dyngoRes) IsResultOf(dyngoOp) {}

func newDyngoOp(parent dyngo.Operation) dyngoOp {
	return dyngoOp{dyngo.NewOperation(parent)}
}

// TestDyngoGLSPopOnFinish verifies the woven push populates the GLS, finish
// pops, and a double FinishOperation does not over-pop the parent (the
// orchestrion#782 shape for AppSec operations).
func TestDyngoGLSPopOnFinish(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("GLS only exists in orchestrion builds")
	}

	outer := newDyngoOp(nil)
	ctx := dyngo.RegisterOperation(context.Background(), outer)
	require.Equal(t, 1, orchestrion.GLSStackDepth(),
		"RegisterOperation must push onto the GLS (injection missing?)")

	inner := newDyngoOp(outer)
	_ = dyngo.RegisterOperation(ctx, inner)
	require.Equal(t, 2, orchestrion.GLSStackDepth())

	dyngo.FinishOperation(inner, dyngoRes{})
	require.Equal(t, 1, orchestrion.GLSStackDepth(), "FinishOperation must pop the operation")

	dyngo.FinishOperation(inner, dyngoRes{}) // double finish must not over-pop
	require.Equal(t, 1, orchestrion.GLSStackDepth(),
		"a second FinishOperation must not over-pop the parent operation")

	dyngo.FinishOperation(outer, dyngoRes{})
	require.Equal(t, 0, orchestrion.GLSStackDepth())
}

// TestDyngoFromContextNilUsesGLS pins the exact pattern AppSec relies on:
// instrumented call sites with no context.Context in scope call
// dyngo.FromContext(nil) and expect the active operation to be found purely via
// the GLS fallback (e.g. contrib/os: `__dd_parent_op, _ := dyngo.FromContext(nil)`
// woven into os.Open). The woven FromContext must therefore WrapContext(nil)
// *unconditionally* — a nil-guard around the wrap lets the source's
// `if ctx == nil { return nil, false }` short-circuit before the GLS is read,
// which silently disables AppSec on every un-instrumented call site (the WAF
// never sees the operation, nothing is blocked). See orchestrion#782.
func TestDyngoFromContextNilUsesGLS(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("GLS only exists in orchestrion builds")
	}

	op := newDyngoOp(nil)
	dyngo.RegisterOperation(context.Background(), op) // push onto the GLS
	t.Cleanup(func() { dyngo.FinishOperation(op, dyngoRes{}) })

	// AppSec's pattern: no context available, rely entirely on the GLS.
	got, ok := dyngo.FromContext(nil)
	require.True(t, ok,
		"FromContext(nil) must find the registered operation via the GLS fallback "+
			"(AppSec calls this from un-instrumented sites); a nil-guarded wrap regresses this")
	require.Equal(t, dyngo.Operation(op), got)
}

// TestDyngoGLSCrossGoroutineNoCorruption verifies that finishing an operation
// on a goroutine other than the one that registered it does not pop the
// finishing goroutine's GLS stack.
func TestDyngoGLSCrossGoroutineNoCorruption(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("GLS only exists in orchestrion builds")
	}

	opA := newDyngoOp(nil)
	dyngo.RegisterOperation(context.Background(), opA) // pushes on this goroutine
	require.Equal(t, 1, orchestrion.GLSStackDepth())

	var depthInB int
	var wg sync.WaitGroup
	wg.Go(func() {
		opB := newDyngoOp(nil)
		dyngo.RegisterOperation(context.Background(), opB)
		dyngo.FinishOperation(opA, dyngoRes{}) // finishing opA on G2 must not pop opB
		depthInB = orchestrion.GLSStackDepth()
		dyngo.FinishOperation(opB, dyngoRes{})
	})
	wg.Wait()

	require.Equal(t, 1, depthInB,
		"FinishOperation(opA) on another goroutine must not pop opB's GLS entry")

	dyngo.FinishOperation(opA, dyngoRes{}) // clean up opA on its own goroutine
}
