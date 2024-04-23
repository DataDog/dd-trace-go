// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphqlevent

import (
	"context"

	"github.com/datadog/dd-trace-go/dyngo/internal/opcontext"
	"github.com/datadog/dd-trace-go/dyngo/internal/operation"
)

type (
	// ExecutionOperation is a GraphQL execution operation, which logically groups
	// together a set of GraphQL resolver operations that are execute in order to
	// fulfill a GraphQL request.
	ExecutionOperation struct {
		operation.Operation
	}

	// ExecutionOperationArgs describes arguments passed to a GraphQL query operation.
	ExecutionOperationArgs struct {
		Variables     map[string]any // The user-provided variables object for the query
		Query         string         // The query that is being executed
		OperationName string         // The user-provided operation name for the query
	}

	// ExecutionOperationRes describes the results of a GraphQL query operation.
	ExecutionOperationRes struct {
		Data  any   // The data returned from processing the GraphQL execution
		Error error // The error returned by processing the GraphQL execution, if any
	}
)

// StartExecutionOperation creates and starts a new GraphQL execution operation
// with the provided parent operation and arguments. If parent is nil, a parent
// will be retrieved from the context if possible; otherwise, the current root
// operation is used.
func StartExecutionOperation(
	ctx context.Context,
	parent *RequestOperation,
	args ExecutionOperationArgs,
) (context.Context, *ExecutionOperation) {
	if parent == nil {
		parent = opcontext.OperationOfType[*RequestOperation](ctx)
	}

	op := &ExecutionOperation{operation.New(parent)}
	operation.Start(op, args)
	return opcontext.WithOperation(ctx, op), op
}

// Finish the GraphQL execution operation with the given results.
func (q *ExecutionOperation) Finish(res ExecutionOperationRes) {
	operation.Finish(q, res)
}

func (ExecutionOperationArgs) IsArgOf(*ExecutionOperation)   {}
func (ExecutionOperationRes) IsResultOf(*ExecutionOperation) {}
