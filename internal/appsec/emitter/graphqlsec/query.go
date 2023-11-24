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
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

type Query struct {
	dyngo.Operation
	trace.TagsHolder
	trace.SecurityEventsHolder
}

// QueryArguments describes arguments passed to a GraphQL query operation.
type QueryArguments struct {
	// Variables is the user-provided variables object for the query.
	Variables map[string]any
	// Query is the query that is being executed.
	Query string
	// OperationName is the user-provided operation name for the query.
	OperationName string
}

// StartQuery starts a new GraphQL query operation, along with the given arguments, and emits a
// start event up in the operation stack. The operation is linked to tge global root operation.
func StartQuery(ctx context.Context, args QueryArguments, listeners ...dyngo.DataListener) (context.Context, *Query) {
	op := &Query{
		Operation:  dyngo.NewOperation(nil),
		TagsHolder: trace.NewTagsHolder(),
	}
	for _, l := range listeners {
		op.OnData(l)
	}
	newCtx := context.WithValue(ctx, listener.ContextKey{}, op)
	dyngo.StartOperation(op, args)

	return newCtx, op
}

// Finish the GraphQL query operation, along with the given results, and emit a finish event up in
// the operation stack.
func (q *Query) Finish(res Result) []any {
	dyngo.FinishOperation(q, QueryResult(res))
	return q.Events()
}

type (
	OnQueryStart  func(*Query, QueryArguments)
	OnQueryFinish func(*Query, QueryResult)

	QueryResult Result
)

var (
	queryStartArgsType = reflect.TypeOf((*QueryArguments)(nil)).Elem()
	queryFinishResType = reflect.TypeOf((*QueryResult)(nil)).Elem()
)

func (OnQueryStart) ListenedType() reflect.Type { return queryStartArgsType }
func (f OnQueryStart) Call(op dyngo.Operation, v interface{}) {
	f(op.(*Query), v.(QueryArguments))
}

func (OnQueryFinish) ListenedType() reflect.Type { return queryFinishResType }
func (f OnQueryFinish) Call(op dyngo.Operation, v interface{}) {
	f(op.(*Query), v.(QueryResult))
}
