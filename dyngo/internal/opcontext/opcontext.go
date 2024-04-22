package opcontext

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/dyngo/internal/operation"
)

type opContextKey[O operation.Operation] struct{}

var anyOperationKey = opContextKey[operation.Operation]{}

// WithOperation returns a new context.Context tracking the provided operation
// value.
func WithOperation[O operation.Operation](ctx context.Context, op O) context.Context {
	ctx = context.WithValue(ctx, opContextKey[O]{}, op)
	ctx = context.WithValue(ctx, anyOperationKey, op)
	return ctx
}

// Operation returns the operation.Operation value tracked by the provided
// context. If no operation is tracked, the nil is returned.
func Operation(ctx context.Context) operation.Operation {
	op, _ := ctx.Value(anyOperationKey).(operation.Operation)
	return op
}

// OperationOfType returns the value of the specified operation type tracked by
// the provided context. If no such operation is tracked, nil is returned.
func OperationOfType[O operation.Operation](ctx context.Context) O {
	op, _ := ctx.Value(opContextKey[O]{}).(O)
	return op
}
