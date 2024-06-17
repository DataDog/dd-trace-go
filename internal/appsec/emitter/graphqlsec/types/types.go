// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package types

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

type (
	RequestOperation struct {
		dyngo.Operation
		trace.TagSetter
		trace.SecurityEventsHolder
	}

	// RequestOperationArgs describes arguments passed to a GraphQL request.
	RequestOperationArgs struct {
		RawQuery      string         // The raw, not-yet-parsed GraphQL query
		OperationName string         // The user-provided operation name for the query
		Variables     map[string]any // The user-provided variables object for this request
	}

	RequestOperationRes struct {
		// Data is the data returned from processing the GraphQL operation.
		Data any
		// Error is the error returned by processing the GraphQL Operation, if any.
		Error error
	}
)

// Finish the GraphQL query operation, along with the given results, and emit a finish event up in
// the operation stack.
func (q *RequestOperation) Finish(res RequestOperationRes) {
	dyngo.FinishOperation(q, res)
}

func (RequestOperationArgs) IsArgOf(*RequestOperation)   {}
func (RequestOperationRes) IsResultOf(*RequestOperation) {}

type (
	ExecutionOperation struct {
		dyngo.Operation
		trace.TagSetter
		trace.SecurityEventsHolder
	}

	// ExecutionOperationArgs describes arguments passed to a GraphQL query operation.
	ExecutionOperationArgs struct {
		// Variables is the user-provided variables object for the query.
		Variables map[string]any
		// Query is the query that is being executed.
		Query string
		// OperationName is the user-provided operation name for the query.
		OperationName string
	}

	ExecutionOperationRes struct {
		// Data is the data returned from processing the GraphQL operation.
		Data any
		// Error is the error returned by processing the GraphQL Operation, if any.
		Error error
	}
)

// Finish the GraphQL query operation, along with the given results, and emit a finish event up in
// the operation stack.
func (q *ExecutionOperation) Finish(res ExecutionOperationRes) {
	dyngo.FinishOperation(q, res)
}

func (ExecutionOperationArgs) IsArgOf(*ExecutionOperation)   {}
func (ExecutionOperationRes) IsResultOf(*ExecutionOperation) {}

type (
	ResolveOperation struct {
		dyngo.Operation
		trace.TagSetter
		trace.SecurityEventsHolder
	}

	// ResolveOperationArgs describes arguments passed to a GraphQL field operation.
	ResolveOperationArgs struct {
		// TypeName is the name of the field's type
		TypeName string
		// FieldName is the name of the field
		FieldName string
		// Arguments is the arguments provided to the field resolver
		Arguments map[string]any
		// Trivial determines whether the resolution is trivial or not. Leave as false if undetermined.
		Trivial bool
	}

	ResolveOperationRes struct {
		// Data is the data returned from processing the GraphQL operation.
		Data any
		// Error is the error returned by processing the GraphQL Operation, if any.
		Error error
	}
)

// Finish the GraphQL Field operation, along with the given results, and emit a finish event up in
// the operation stack.
func (q *ResolveOperation) Finish(res ResolveOperationRes) {
	dyngo.FinishOperation(q, res)
}

func (ResolveOperationArgs) IsArgOf(*ResolveOperation)   {}
func (ResolveOperationRes) IsResultOf(*ResolveOperation) {}
