// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package grpcsec

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
)

type (
	// ReceiveOperation type representing an gRPC server handler operation. It must
	// be created with StartReceiveOperation() and finished with its Finish().
	ReceiveOperation struct {
		dyngo.Operation
	}

	// ReceiveOperationArgs is the gRPC handler receive operation arguments
	// Empty as of today.
	ReceiveOperationArgs struct{}

	// ReceiveOperationRes is the gRPC handler receive operation results which
	// contains the message the gRPC handler received.
	ReceiveOperationRes struct {
		// Message received by the gRPC handler.
		// Corresponds to the address `grpc.server.request.message`.
		Message interface{}
	}
)

// StartReceiveOperation starts a receive operation of a gRPC handler, along
// with the given arguments and parent operation, and emits a start event up in
// the operation stack. When parent is nil, the operation is linked to the
// global root operation.
func StartReceiveOperation(args ReceiveOperationArgs, parent dyngo.Operation) ReceiveOperation {
	op := ReceiveOperation{Operation: dyngo.NewOperation(parent)}
	dyngo.StartOperation(op, args)
	return op
}

// Finish the gRPC handler operation, along with the given results, and emits a
// finish event up in the operation stack.
func (op ReceiveOperation) Finish(res ReceiveOperationRes) {
	dyngo.FinishOperation(op, res)
}

func (ReceiveOperationArgs) IsArgOf(ReceiveOperation)   {}
func (ReceiveOperationRes) IsResultOf(ReceiveOperation) {}
