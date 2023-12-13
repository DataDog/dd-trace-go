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

type Request struct {
	dyngo.Operation
	trace.TagSetter
	trace.SecurityEventsHolder
}

// RequestArguments describes arguments passed to a GraphQL request.
type RequestArguments struct {
	RawQuery      string         // The raw, not-yet-parsed GraphQL query
	OperationName string         // The user-provided operation name for the query
	Variables     map[string]any // The user-provided variables object for this request
}

// StartRequest starts a new GraphQL request operation, along with the given arguments, and emits a
// start event up in the operation stack. The operation is usually linked to tge global root
// operation.
func StartRequest(ctx context.Context, span trace.TagSetter, args RequestArguments) (context.Context, *Request) {
	// The parent will typically be nil (the root operation will be used)
	parent, _ := ctx.Value(contextKey{}).(dyngo.Operation)

	if span == nil {
		// The span may be nil (e.g: in case of GraphQL subscriptions with certian contribs)
		span = trace.NoopTagSetter{}
	}

	op := &Request{
		Operation: dyngo.NewOperation(parent),
		TagSetter: span,
	}
	newCtx := context.WithValue(ctx, contextKey{}, op)
	dyngo.StartOperation(op, args)

	return newCtx, op
}

// Finish the GraphQL query operation, along with the given results, and emit a finish event up in
// the operation stack.
func (q *Request) Finish(res Result) {
	dyngo.FinishOperation(q, RequestResult(res))
}

type (
	OnRequestStart  func(*Request, RequestArguments)
	OnRequestFinish func(*Request, RequestResult)

	RequestResult Result
)

var (
	requestStartArgsType = reflect.TypeOf((*RequestArguments)(nil)).Elem()
	requestFinishResType = reflect.TypeOf((*RequestResult)(nil)).Elem()
)

func (OnRequestStart) ListenedType() reflect.Type { return requestStartArgsType }
func (f OnRequestStart) Call(op dyngo.Operation, v interface{}) {
	f(op.(*Request), v.(RequestArguments))
}

func (OnRequestFinish) ListenedType() reflect.Type { return requestFinishResType }
func (f OnRequestFinish) Call(op dyngo.Operation, v interface{}) {
	f(op.(*Request), v.(RequestResult))
}
