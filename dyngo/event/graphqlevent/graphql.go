// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package graphqlevent contains the definition for dyngo's GraphQL events.
package graphqlevent

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/dyngo/internal/opcontext"
	"gopkg.in/DataDog/dd-trace-go.v1/dyngo/internal/operation"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

type (
	// RequestOperation is the top-level GraphQL operation.
	RequestOperation struct {
		operation.Operation
		trace.TagSetter
		trace.SecurityEventsHolder
	}

	// RequestOperationArgs describes arguments passed to a GraphQL request.
	RequestOperationArgs struct {
		RawQuery      string         // The raw, not-yet-parsed GraphQL query
		OperationName string         // The user-provided operation name for the query
		Variables     map[string]any // The user-provided variables object for this request
	}

	// RequestOperationRes describes the results of a GraphQL request operation.
	RequestOperationRes struct {
		Data  any   // The data returned from processing the GraphQL operation
		Error error // The error returned by processing the GraphQL Operation, if any
	}
)

// StartRequestOperation creates and starts a new GraphQL request operation
// using the provided parent operation adn arguments. If the parent is nil, the
// current root operation is used.
func StartRequestOperation(
	ctx context.Context,
	parent operation.Operation,
	span trace.TagSetter,
	args RequestOperationArgs,
) (context.Context, *RequestOperation) {
	if span == nil {
		// The span may be nil (e.g, GraphQL subscriptions are not traced by some contribs)
		span = trace.NoopTagSetter{}
	}
	op := &RequestOperation{
		Operation: operation.New(parent),
		TagSetter: span,
	}
	ctx = opcontext.WithOperation(ctx, op)
	operation.Start(op, args)
	return ctx, op
}

// Finish finishes the receiving GraphQL request operation with the provided
// results.
func (o *RequestOperation) Finish(res RequestOperationRes) {
	operation.Finish(o, res)
}

func (RequestOperationArgs) IsArgOf(*RequestOperation)   {}
func (RequestOperationRes) IsResultOf(*RequestOperation) {}
