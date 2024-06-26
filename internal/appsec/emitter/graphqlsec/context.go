// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphqlsec

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/orchestrion"
)

type typed[T dyngo.Operation] struct{}

// FromContext returns the operation of the given type from the context. Returns
// the zero-value of T if no such operation is found.
func FromContext[T dyngo.Operation](ctx context.Context) T {
	val := ctx.Value(typed[T]{})
	if val == nil {
		var zero T
		log.Debug("appsec/graphqlsec: no operation of type %T found in context", zero)
		return zero
	}

	return val.(T)
}

// contextWithValue creates a new context with the specified operation stored in it.
func contextWithValue[T dyngo.Operation](ctx context.Context, value T) context.Context {
	return orchestrion.CtxWithValue(ctx, typed[T]{}, value)
}
