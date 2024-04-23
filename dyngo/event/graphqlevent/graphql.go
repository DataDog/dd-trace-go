// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package graphqlevent contains the definition for dyngo's GraphQL events.
package graphqlevent

import (
	"context"

	"github.com/datadog/dd-trace-go/dyngo/internal/opcontext"
	"github.com/datadog/dd-trace-go/dyngo/internal/operation"
)

type (
	// RequestOperation is the top-level GraphQL operation.
	RequestOperation struct {
		operation.Operation
		context.Context
	}

	// RequestOperationArgs describes arguments passed to a GraphQL request.
	RequestOperationArgs struct {
		Variables     map[string]any // The user-provided variables object for this request
		RawQuery      string         // The raw, not-yet-parsed GraphQL query
		OperationName string         // The user-provided operation name for the query
	}

	// RequestOperationRes describes the results of a GraphQL request operation.
	RequestOperationRes struct {
		Data  any   // The data returned from processing the GraphQL operation
		Error error // The error returned by processing the GraphQL Operation, if any
	}
)

// StartRequestOperation creates and starts a new GraphQL request operation
// using the provided parent operation adn arguments. If the parent is nil, an
// operation will be extracted form the provided context if possible. Otherwise,
// the current root operation will be used.
func StartRequestOperation(
	ctx context.Context,
	parent operation.Operation,
	args RequestOperationArgs,
) (context.Context, *RequestOperation) {
	if parent == nil {
		parent = opcontext.Operation(ctx)
	}

	op := &RequestOperation{Operation: operation.New(parent), Context: ctx}
	operation.Start(op, args)
	return opcontext.WithOperation(ctx, op), op
}

// Finish finishes the receiving GraphQL request operation with the provided
// results.
func (o *RequestOperation) Finish(res RequestOperationRes) {
	operation.Finish(o, res)
}

func (RequestOperationArgs) IsArgOf(*RequestOperation)   {}
func (RequestOperationRes) IsResultOf(*RequestOperation) {}
