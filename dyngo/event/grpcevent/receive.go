// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpcevent

import "github.com/datadog/dd-trace-go/dyngo/internal/operation"

type (
	// ReceiveOperation represents a gRPC server handler operation.
	ReceiveOperation struct {
		operation.Operation
	}

	// ReceiveOperationArgs is the gRPC handler operation's arguments.
	ReceiveOperationArgs struct{}

	// ReceiveOperationRes is the gRPC handler operation's results.
	ReceiveOperationRes struct {
		Message any `address:"grpc.server.request.message"` // The received gRPC message
	}
)

// StartReceiveOperation creates and starts a new gRPC receive operation using
// the provided parent operation and arguments. If the parent is nil, the
// current root operation is used.
func StartReceiveOperation(parent operation.Operation, args ReceiveOperationArgs) ReceiveOperation {
	op := ReceiveOperation{Operation: operation.New(parent)}
	operation.Start(op, args)
	return op
}

// Finish finishes the gRPC receive operation with the provided result.
func (op ReceiveOperation) Finish(res ReceiveOperationRes) {
	operation.Finish(op, res)
}

func (ReceiveOperationArgs) IsArgOf(ReceiveOperation)   {}
func (ReceiveOperationRes) IsResultOf(ReceiveOperation) {}
