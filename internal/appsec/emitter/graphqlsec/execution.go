// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package graphql is the GraphQL instrumentation API and contract for AppSec
// defining an abstract run-time representation of AppSec middleware. GraphQL
// integrations must use this package to enable AppSec features for GraphQL,
// which listens to this package's operation events.
package graphqlsec

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/graphqlsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

// StartExecutionOperation starts a new GraphQL query operation, along with the given arguments, and
// emits a start event up in the operation stack. The operation is tracked on the returned context,
// and can be extracted later on using FromContext.
func StartExecutionOperation(ctx context.Context, parent *types.RequestOperation, span trace.TagSetter, args types.ExecutionOperationArgs) (context.Context, *types.ExecutionOperation) {
	if span == nil {
		// The span may be nil (e.g: in case of GraphQL subscriptions with certian contribs). Child
		// operations might have spans however... and these should be used then.
		span = trace.NoopTagSetter{}
	}

	op := &types.ExecutionOperation{
		Operation: dyngo.NewOperation(parent),
		TagSetter: span,
	}
	newCtx := contextWithValue(ctx, op)
	dyngo.StartOperation(op, args)

	return newCtx, op
}
