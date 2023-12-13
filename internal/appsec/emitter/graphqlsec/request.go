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

type RequestOperation struct {
	dyngo.Operation
	trace.TagSetter
	trace.SecurityEventsHolder
}

// RequestOperationArgs describes arguments passed to a GraphQL request.
type RequestOperationArgs struct {
	RawQuery      string         // The raw, not-yet-parsed GraphQL query
	OperationName string         // The user-provided operation name for the query
	Variables     map[string]any // The user-provided variables object for this request
}

// StartRequestOperation starts a new GraphQL request operation, along with the given arguments, and
// emits a start event up in the operation stack. The operation is usually linked to tge global root
// operation. The operation is tracked on the returned context, and can be extracted later on using
// FromContext.
func StartRequestOperation(ctx context.Context, parent dyngo.Operation, span trace.TagSetter, args RequestOperationArgs) (context.Context, *RequestOperation) {
	if span == nil {
		// The span may be nil (e.g: in case of GraphQL subscriptions with certian contribs)
		span = trace.NoopTagSetter{}
	}

	op := &RequestOperation{
		Operation: dyngo.NewOperation(parent),
		TagSetter: span,
	}
	newCtx := contextWithValue(ctx, op)
	dyngo.StartOperation(op, args)

	return newCtx, op
}

// Finish the GraphQL query operation, along with the given results, and emit a finish event up in
// the operation stack.
func (q *RequestOperation) Finish(res RequestOperationRes) {
	dyngo.FinishOperation(q, res)
}

type (
	OnRequestOperationStart  func(*RequestOperation, RequestOperationArgs)
	OnRequestOperationFinish func(*RequestOperation, RequestOperationRes)

	RequestOperationRes struct {
		// Data is the data returned from processing the GraphQL operation.
		Data any
		// Error is the error returned by processing the GraphQL Operation, if any.
		Error error
	}
)

var (
	requestOperationStartArgsType = reflect.TypeOf((*RequestOperationArgs)(nil)).Elem()
	requestOperationFinishResType = reflect.TypeOf((*RequestOperationRes)(nil)).Elem()
)

func (OnRequestOperationStart) ListenedType() reflect.Type { return requestOperationStartArgsType }
func (f OnRequestOperationStart) Call(op dyngo.Operation, v interface{}) {
	f(op.(*RequestOperation), v.(RequestOperationArgs))
}

func (OnRequestOperationFinish) ListenedType() reflect.Type { return requestOperationFinishResType }
func (f OnRequestOperationFinish) Call(op dyngo.Operation, v interface{}) {
	f(op.(*RequestOperation), v.(RequestOperationRes))
}
