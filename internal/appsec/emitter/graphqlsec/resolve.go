// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphqlsec

import (
	"context"
	"reflect"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

type ResolveOperation struct {
	dyngo.Operation
	trace.TagSetter
	trace.SecurityEventsHolder
}

// ResolveOperationArgs describes arguments passed to a GraphQL field operation.
type ResolveOperationArgs struct {
	// TypeName is the name of the field's type
	TypeName string
	// FieldName is the name of the field
	FieldName string
	// Arguments is the arguments provided to the field resolver
	Arguments map[string]any
	// Trivial determines whether the resolution is trivial or not. Leave as false if undetermined.
	Trivial bool
}

// StartResolveOperation starts a new GraphQL Resolve operation, along with the given arguments, and
// emits a start event up in the operation stack. The operation is tracked on the returned context,
// and can be extracted later on using FromContext.
func StartResolveOperation(ctx context.Context, parent *ExecutionOperation, span trace.TagSetter, args ResolveOperationArgs) (context.Context, *ResolveOperation) {
	op := &ResolveOperation{
		Operation: dyngo.NewOperation(parent),
		TagSetter: span,
	}
	newCtx := contextWithValue(ctx, op)
	dyngo.StartOperation(op, args)

	return newCtx, op
}

// Finish the GraphQL Field operation, along with the given results, and emit a finish event up in
// the operation stack.
func (q *ResolveOperation) Finish(res ResolveOperationRes) {
	dyngo.FinishOperation(q, res)
}

type (
	OnResolveOperationStart  func(*ResolveOperation, ResolveOperationArgs)
	OnResolveOperationFinish func(*ResolveOperation, ResolveOperationRes)

	ResolveOperationRes struct {
		// Data is the data returned from processing the GraphQL operation.
		Data any
		// Error is the error returned by processing the GraphQL Operation, if any.
		Error error
	}
)

var (
	resolveOperationStartArgsType = reflect.TypeOf((*ResolveOperationArgs)(nil)).Elem()
	resolveOperationFinishResType = reflect.TypeOf((*ResolveOperationRes)(nil)).Elem()
)

func (OnResolveOperationStart) ListenedType() reflect.Type { return resolveOperationStartArgsType }
func (f OnResolveOperationStart) Call(op dyngo.Operation, v interface{}) {
	f(op.(*ResolveOperation), v.(ResolveOperationArgs))
}

func (OnResolveOperationFinish) ListenedType() reflect.Type { return resolveOperationFinishResType }
func (f OnResolveOperationFinish) Call(op dyngo.Operation, v interface{}) {
	f(op.(*ResolveOperation), v.(ResolveOperationRes))
}
