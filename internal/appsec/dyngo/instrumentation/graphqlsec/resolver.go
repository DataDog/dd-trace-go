// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphqlsec

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation"
)

type ResolveOperation struct {
	dyngo.Operation
	instrumentation.TagsHolder
	instrumentation.SecurityEventsHolder
}

func StartResolverOperation(ctx context.Context, listeners ...dyngo.DataListener) (context.Context, *ResolveOperation) {
	// The parent will typically be the Field operation that previously fired...
	parent, _ := ctx.Value(instrumentation.ContextKey{}).(dyngo.Operation)

	op := &ResolveOperation{
		Operation:  dyngo.NewOperation(parent),
		TagsHolder: instrumentation.NewTagsHolder(),
	}

	for _, l := range listeners {
		op.OnData(l)
	}

	newCtx := context.WithValue(ctx, instrumentation.ContextKey{}, op)
	dyngo.StartOperation(op, nil)

	return newCtx, op
}

func (o *ResolveOperation) Finish(res Result) []any {
	dyngo.FinishOperation(o, ResolveResult(res))
	return o.Events()
}

type ResolveResult Result
