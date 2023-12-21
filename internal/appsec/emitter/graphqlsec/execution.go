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
	"reflect"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

type ExecutionOperation struct {
	dyngo.Operation
	trace.TagSetter
	trace.SecurityEventsHolder
}

// ExecutionOperationArgs describes arguments passed to a GraphQL query operation.
type ExecutionOperationArgs struct {
	// Variables is the user-provided variables object for the query.
	Variables map[string]any
	// Query is the query that is being executed.
	Query string
	// OperationName is the user-provided operation name for the query.
	OperationName string
}

// StartExecutionOperation starts a new GraphQL query operation, along with the given arguments, and
// emits a start event up in the operation stack. The operation is tracked on the returned context,
// and can be extracted later on using FromContext.
func StartExecutionOperation(ctx context.Context, parent *RequestOperation, span trace.TagSetter, args ExecutionOperationArgs, listeners ...dyngo.DataListener) (context.Context, *ExecutionOperation) {
	if span == nil {
		// The span may be nil (e.g: in case of GraphQL subscriptions with certian contribs). Child
		// operations might have spans however... and these should be used then.
		span = trace.NoopTagSetter{}
	}

	op := &ExecutionOperation{
		Operation: dyngo.NewOperation(parent),
		TagSetter: span,
	}
	for _, l := range listeners {
		op.OnData(l)
	}
	newCtx := contextWithValue(ctx, op)
	dyngo.StartOperation(op, args)

	return newCtx, op
}

// Finish the GraphQL query operation, along with the given results, and emit a finish event up in
// the operation stack.
func (q *ExecutionOperation) Finish(res ExecutionOperationRes) {
	dyngo.FinishOperation(q, res)
}

type (
	OnExecutionOperationStart  func(*ExecutionOperation, ExecutionOperationArgs)
	OnExecutionOperationFinish func(*ExecutionOperation, ExecutionOperationRes)

	ExecutionOperationRes struct {
		// Data is the data returned from processing the GraphQL operation.
		Data any
		// Error is the error returned by processing the GraphQL Operation, if any.
		Error error
	}
)

var (
	executionOperationStartArgsType = reflect.TypeOf((*ExecutionOperationArgs)(nil)).Elem()
	executionOperationFinishResType = reflect.TypeOf((*ExecutionOperationRes)(nil)).Elem()
)

func (OnExecutionOperationStart) ListenedType() reflect.Type { return executionOperationStartArgsType }
func (f OnExecutionOperationStart) Call(op dyngo.Operation, v interface{}) {
	f(op.(*ExecutionOperation), v.(ExecutionOperationArgs))
}

func (OnExecutionOperationFinish) ListenedType() reflect.Type { return executionOperationFinishResType }
func (f OnExecutionOperationFinish) Call(op dyngo.Operation, v interface{}) {
	f(op.(*ExecutionOperation), v.(ExecutionOperationRes))
}
