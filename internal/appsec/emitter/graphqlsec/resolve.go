// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphqlsec

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/graphqlsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

// StartResolveOperation starts a new GraphQL Resolve operation, along with the given arguments, and
// emits a start event up in the operation stack. The operation is tracked on the returned context,
// and can be extracted later on using FromContext.
func StartResolveOperation(ctx context.Context, parent *types.ExecutionOperation, span trace.TagSetter, args types.ResolveOperationArgs) (context.Context, *types.ResolveOperation) {
	op := &types.ResolveOperation{
		Operation: dyngo.NewOperation(parent),
		TagSetter: span,
	}
	newCtx := contextWithValue(ctx, op)
	dyngo.StartOperation(op, args)

	return newCtx, op
}
