// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphqlsec

import (
	"context"
	"reflect"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

type Field struct {
	dyngo.Operation
	trace.TagSetter
	trace.SecurityEventsHolder
}

// FieldArguments describes arguments passed to a GraphQL field operation.
type FieldArguments struct {
	// TypeName is the name of the field's type
	TypeName string
	// FieldName is the name of the field
	FieldName string
	// Arguments is the arguments provided to the field resolver
	Arguments map[string]any
	// Trivial determines whether the resolution is trivial or not. Leave as false if undetermined.
	Trivial bool
}

// StartField starts a new GraphQL Field operation, along with the given arguments, and emits a
// start event up in the operation stack.
func StartField(ctx context.Context, span trace.TagSetter, args FieldArguments, listeners ...dyngo.DataListener) (context.Context, *Field) {
	// The parent will typically be the Query operation that previously fired...
	parent, _ := ctx.Value(listener.ContextKey{}).(dyngo.Operation)

	op := &Field{
		Operation: dyngo.NewOperation(parent),
		TagSetter: span,
	}
	for _, l := range listeners {
		op.OnData(l)
	}
	newCtx := context.WithValue(ctx, listener.ContextKey{}, op)
	dyngo.StartOperation(op, args)

	return newCtx, op
}

// Finish the GraphQL Field operation, along with the given results, and emit a finish event up in
// the operation stack.
func (q *Field) Finish(res Result) {
	dyngo.FinishOperation(q, FieldResult(res))
}

type (
	OnFieldStart  func(*Field, FieldArguments)
	OnFieldFinish func(*Field, FieldResult)

	FieldResult Result
)

var (
	FieldStartArgsType = reflect.TypeOf((*FieldArguments)(nil)).Elem()
	FieldFinishResType = reflect.TypeOf((*FieldResult)(nil)).Elem()
)

func (OnFieldStart) ListenedType() reflect.Type { return FieldStartArgsType }
func (f OnFieldStart) Call(op dyngo.Operation, v interface{}) {
	f(op.(*Field), v.(FieldArguments))
}

func (OnFieldFinish) ListenedType() reflect.Type { return FieldFinishResType }
func (f OnFieldFinish) Call(op dyngo.Operation, v interface{}) {
	f(op.(*Field), v.(FieldResult))
}
