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
	// RequestOperation represents the execution of a single GraphQL resolver.
	ResolveOperation struct {
		operation.Operation
		context.Context
	}

	// ResolveOperationArgs describes arguments passed to a GraphQL resolver operation.
	ResolveOperationArgs struct {
		Arguments map[string]any // The arguments passed to the resolver
		TypeName  string         // The name of the resolved field's type
		FieldName string         // The name of the resolved field
		Trivial   bool           // Whether the resolver is trivial
	}

	// ResolveOperationRes describes the results of a GraphQL resolver operation.
	ResolveOperationRes struct {
		Data  any   // The data returned from processing the resolver
		Error error // The error returned by processing the resolver, if any
	}
)

// StartResolveOperation creates and starts a new GraphQL resolve operation
// using  the provided parent and arguments. If the parent is nil, a value will
// be retrieved from the provided context if available; otherwise the current
// root operation is used instead.
func StartResolveOperation(
	ctx context.Context,
	parent *ExecutionOperation,
	args ResolveOperationArgs,
) (context.Context, *ResolveOperation) {
	if parent == nil {
		parent = opcontext.OperationOfType[*ExecutionOperation](ctx)
	}

	op := &ResolveOperation{Operation: operation.New(parent), Context: ctx}

	operation.Start(op, args)
	return opcontext.WithOperation(ctx, op), op
}

// Finish finishes the GraphQL resolve operation with the provided results.
func (o *ResolveOperation) Finish(res ResolveOperationRes) {
	operation.Finish(o, res)
}

func (ResolveOperationArgs) IsArgOf(*ResolveOperation)   {}
func (ResolveOperationRes) IsResultOf(*ResolveOperation) {}
