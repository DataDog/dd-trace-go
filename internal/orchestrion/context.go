// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"context"
)

// FromCtxOrGLS returns a context that that will check if
func FromCtxOrGLS(ctx context.Context) context.Context {
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
func CtxWithValue(parent context.Context, key, val any) context.Context {
	if !Enabled() {
		return context.WithValue(parent, key, val)
	}

	getDDContextStack().Push(key, val)
	return FromCtxOrGLS(parent)
}

func GLSPopValue(key any) any {
	return getDDContextStack().Pop(key)
}

var _ context.Context = (*glsContext)(nil)

type glsContext struct {
	context.Context
}

func (g *glsContext) Value(key any) any {
	if val := getDDContextStack().Peek(key); val != nil {
		return val
	}

	return g.Context.Value(key)
}
