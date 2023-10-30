// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphqlsec

import (
	"context"
	"encoding/json"
	"reflect"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation"
)

type Field struct {
	dyngo.Operation
	instrumentation.TagsHolder
	instrumentation.SecurityEventsHolder
}

// FieldArguments describes arguments passed to a GraphQL field operation.
type FieldArguments struct {
	// Label is the user-specified label for the field
	Label string
	// TypeName is the name of the field's type
	TypeName string
	// FieldName is the name of the field
	FieldName string
	// Trivial determines whether the resolution is trivial or not
	Trivial bool
	// Arguments is the arguments provided to the field resolver
	Arguments map[string]any
}

// FieldResult describes the result of a GraphQL field execution.
type FieldResult struct {
	// Data is the data returned from processing the field.
	Data any
	// Error is the error returned by processing the field, if any.
	Error error
}

// StartField starts a new GraphQL Field operation, along with the given arguments, and emits a
// start event up in the operation stack.
func StartField(ctx context.Context, args FieldArguments, listeners ...dyngo.DataListener) (context.Context, *Field) {
	// The parent will typically be the Query operation that previously fired...
	parent, _ := ctx.Value(instrumentation.ContextKey{}).(dyngo.Operation)

	op := &Field{
		Operation:  dyngo.NewOperation(parent),
		TagsHolder: instrumentation.NewTagsHolder(),
	}
	for _, l := range listeners {
		op.OnData(l)
	}
	newCtx := context.WithValue(ctx, instrumentation.ContextKey{}, op)
	dyngo.StartOperation(op, args)

	return newCtx, op
}

// Finish the GraphQL Field operation, along with the given results, and emit a finish event up in
// the operation stack.
func (q *Field) Finish(res FieldResult) []json.RawMessage {
	dyngo.FinishOperation(q, res)
	return q.Events()
}

type (
	OnFieldStart  func(*Field, FieldArguments)
	OnFieldFinish func(*Field, FieldResult)
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
