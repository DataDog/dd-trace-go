// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"context"
)

// WrapContext returns the GLS-wrapped context if orchestrion is enabled, otherwise it returns the given parameter.
func WrapContext(ctx context.Context) context.Context {
	if !Enabled() {
		return ctx
	}

	if ctx != nil {
		if _, ok := ctx.(*glsContext); ok { // avoid (some) double wrapping
			return ctx
		}
	}

	if ctx == nil {
		ctx = context.Background()
	}

	return &glsContext{ctx}
}

// CtxWithValue runs context.WithValue, adds the result to the GLS slot of orchestrion, and returns it.
// If orchestrion is not enabled, it will run context.WithValue and return the result.
// Since we don't support cross-goroutine switch of the GLS we still run context.WithValue in the case
// we are switching goroutines.
func CtxWithValue(parent context.Context, key, val any) context.Context {
	if !Enabled() {
		return context.WithValue(parent, key, val)
	}

	getDDContextStack().Push(key, val)
	return context.WithValue(WrapContext(parent), key, val)
}

// GLSPopValue pops the value from the GLS slot of orchestrion and returns it. Using context.Context values usually does
// not require to pop any stack because the copy of each previous context makes the local variable in the scope disappear
// when the current function ends. But the GLS is a semi-global variable that can be accessed from any function in the
// stack, so we need to pop the value when we are done with it.
func GLSPopValue(key any) any {
	if !Enabled() {
		return nil
	}

	return getDDContextStack().Pop(key)
}

// GLSPopFunc returns a function that pops key from the GLS context stack of the
// goroutine that called GLSPopFunc. The returned function is safe to call from
// any goroutine: it compares the current goroutine's GLS contextStack pointer
// with the one captured at creation time and only pops if they match (i.e.,
// same goroutine). On a different goroutine the pop is a no-op, preventing
// accidental corruption of another goroutine's GLS state.
func GLSPopFunc(key any) func() {
	if !Enabled() {
		return glsNoop
	}
	pushStack := getDDContextStack()
	return func() {
		if gls := getDDGLS(); gls != nil && gls.(*contextStack) == pushStack {
			pushStack.Pop(key)
		}
	}
}

var glsNoop = func() {}

// GLSStackDepth returns the total number of entries in the current goroutine's
// GLS context stack. Returns 0 if orchestrion is not enabled. This is intended
// for use in tests to detect GLS leaks.
func GLSStackDepth() int {
	if !Enabled() {
		return 0
	}
	return getDDContextStack().Depth()
}

var _ context.Context = (*glsContext)(nil)

type glsContext struct {
	context.Context
}

func (g *glsContext) Value(key any) any {
	if !Enabled() {
		return g.Context.Value(key)
	}

	if val := getDDContextStack().Peek(key); val != nil {
		return val
	}

	return g.Context.Value(key)
}
