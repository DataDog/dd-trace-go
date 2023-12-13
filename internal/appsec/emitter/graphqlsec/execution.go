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

type Execution struct {
	dyngo.Operation
	trace.TagSetter
	trace.SecurityEventsHolder
}

// ExecutionArguments describes arguments passed to a GraphQL query operation.
type ExecutionArguments struct {
	// Variables is the user-provided variables object for the query.
	Variables map[string]any
	// Query is the query that is being executed.
	Query string
	// OperationName is the user-provided operation name for the query.
	OperationName string
}

// StartExecution starts a new GraphQL query operation, along with the given arguments, and emits a
// start event up in the operation stack.
func StartExecution(ctx context.Context, span trace.TagSetter, args ExecutionArguments, listeners ...dyngo.DataListener) (context.Context, *Execution) {
	// The parent will typically be the Request operation that previously fired...
	parent, _ := ctx.Value(contextKey{}).(dyngo.Operation)

	if span == nil {
		// The span may be nil (e.g: in case of GraphQL subscriptions with certian contribs)
		span = trace.NoopTagSetter{}
	}

	op := &Execution{
		Operation: dyngo.NewOperation(parent),
		TagSetter: span,
	}
	for _, l := range listeners {
		op.OnData(l)
	}
	newCtx := context.WithValue(ctx, contextKey{}, op)
	dyngo.StartOperation(op, args)

	return newCtx, op
}

// Finish the GraphQL query operation, along with the given results, and emit a finish event up in
// the operation stack.
func (q *Execution) Finish(res Result) {
	dyngo.FinishOperation(q, ExecutionResult(res))
}

type (
	OnExecutionStart  func(*Execution, ExecutionArguments)
	OnExecutionFinish func(*Execution, ExecutionResult)

	ExecutionResult Result
)

var (
	executionStartArgsType = reflect.TypeOf((*ExecutionArguments)(nil)).Elem()
	executionFinishResType = reflect.TypeOf((*ExecutionResult)(nil)).Elem()
)

func (OnExecutionStart) ListenedType() reflect.Type { return executionStartArgsType }
func (f OnExecutionStart) Call(op dyngo.Operation, v interface{}) {
	f(op.(*Execution), v.(ExecutionArguments))
}

func (OnExecutionFinish) ListenedType() reflect.Type { return executionFinishResType }
func (f OnExecutionFinish) Call(op dyngo.Operation, v interface{}) {
	f(op.(*Execution), v.(ExecutionResult))
}
