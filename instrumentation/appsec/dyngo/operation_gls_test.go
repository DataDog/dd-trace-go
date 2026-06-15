// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dyngo_test

import (
	"context"
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFinishOperationIsIdempotentOnGLS is a regression test for the same GLS
// over-pop shape as the span fix in orchestrion#782, on the dyngo contextKey.
//
// FinishOperation used to call orchestrion.GLSPopValue before checking
// o.disabled, so calling it twice on the same operation popped the GLS stack
// twice — the second pop removing an unrelated operation still in flight on
// the same goroutine. The disabled check now runs before the pop, making it
// idempotent.
func TestFinishOperationIsIdempotentOnGLS(t *testing.T) {
	t.Cleanup(orchestrion.MockGLS())

	ctx := context.Background()
	outer := operation{dyngo.NewOperation(nil)}
	ctx = dyngo.RegisterOperation(ctx, outer) // GLS: [outer]
	require.Equal(t, 1, orchestrion.GLSStackDepth())

	inner := operation{dyngo.NewOperation(outer)}
	_ = dyngo.RegisterOperation(ctx, inner) // GLS: [outer, inner]
	require.Equal(t, 2, orchestrion.GLSStackDepth())

	dyngo.FinishOperation(inner, MyOperationRes{}) // pop inner -> [outer]
	require.Equal(t, 1, orchestrion.GLSStackDepth(), "first FinishOperation pops inner")

	dyngo.FinishOperation(inner, MyOperationRes{}) // must be a no-op for the GLS
	assert.Equal(t, 1, orchestrion.GLSStackDepth(),
		"double FinishOperation must not pop the unrelated outer operation")

	dyngo.FinishOperation(outer, MyOperationRes{}) // clean up -> []
	assert.Equal(t, 0, orchestrion.GLSStackDepth())
}

// TestFinishOperationCrossGoroutineDoesNotPopOthersStack verifies that
// finishing an operation on a goroutine other than the one that registered it
// does not pop the finishing goroutine's GLS stack. Before the fix, the raw
// GLSPopValue popped whichever goroutine ran FinishOperation; now the popper
// captured at RegisterOperation time is goroutine-scoped and no-ops elsewhere.
func TestFinishOperationCrossGoroutineDoesNotPopOthersStack(t *testing.T) {
	t.Cleanup(orchestrion.MockGLSPerGoroutine())

	// G1 (this goroutine) registers opA.
	opA := operation{dyngo.NewOperation(nil)}
	dyngo.RegisterOperation(context.Background(), opA)
	require.Equal(t, 1, orchestrion.GLSStackDepth(), "opA on G1's stack")

	var depthInB int
	var wg sync.WaitGroup
	wg.Go(func() {
		// G2 registers its own opB, then finishes opA (registered on G1).
		opB := operation{dyngo.NewOperation(nil)}
		dyngo.RegisterOperation(context.Background(), opB)
		dyngo.FinishOperation(opA, MyOperationRes{}) // must not touch G2's stack
		depthInB = orchestrion.GLSStackDepth()
		dyngo.FinishOperation(opB, MyOperationRes{})
	})
	wg.Wait()

	assert.Equal(t, 1, depthInB,
		"FinishOperation(opA) on G2 must not pop G2's own (opB) GLS entry")
}
