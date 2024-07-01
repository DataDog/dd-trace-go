// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package types

import (
	"net/netip"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

// Abstract gRPC server handler operation definitions. It is based on two
// operations allowing to describe every type of RPC: the HandlerOperation type
// which represents the RPC handler, and the ReceiveOperation type which
// represents the messages the RPC handler receives during its lifetime.
// This means that the ReceiveOperation(s) will happen within the
// HandlerOperation.
// Every type of RPC, unary, client streaming, server streaming, and
// bidirectional streaming RPCs, can be all represented with a HandlerOperation
// having one or several ReceiveOperation.
// The send operation is not required for now and therefore not defined, which
// means that server and bidirectional streaming RPCs currently have the same
// run-time representation as unary and client streaming RPCs.
type (
	// HandlerOperation represents a gRPC server handler operation.
	// It must be created with StartHandlerOperation() and finished with its
	// Finish() method.
	// Security events observed during the operation lifetime should be added
	// to the operation using its AddSecurityEvent() method.
	HandlerOperation struct {
		dyngo.Operation
		Error error
		trace.TagsHolder
		trace.SecurityEventsHolder
	}

	// HandlerOperationArgs is the grpc handler arguments.
	HandlerOperationArgs struct {
		// Method is the gRPC method name.
		// Corresponds to the address `grpc.server.method`.
		Method string

		// RPC metadata received by the gRPC handler.
		// Corresponds to the address `grpc.server.request.metadata`.
		Metadata map[string][]string

		// ClientIP is the IP address of the client that initiated the gRPC request.
		// Corresponds to the address `http.client_ip`.
		ClientIP netip.Addr
	}

	// HandlerOperationRes is the grpc handler results. Empty as of today.
	HandlerOperationRes struct{}

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

// Finish the gRPC handler operation, along with the given results, and emit a
// finish event up in the operation stack.
func (op *HandlerOperation) Finish(res HandlerOperationRes) []any {
	dyngo.FinishOperation(op, res)
	return op.Events()
}

// Finish the gRPC handler operation, along with the given results, and emits a
// finish event up in the operation stack.
func (op ReceiveOperation) Finish(res ReceiveOperationRes) {
	dyngo.FinishOperation(op, res)
}

func (HandlerOperationArgs) IsArgOf(*HandlerOperation)   {}
func (HandlerOperationRes) IsResultOf(*HandlerOperation) {}

func (ReceiveOperationArgs) IsArgOf(ReceiveOperation)   {}
func (ReceiveOperationRes) IsResultOf(ReceiveOperation) {}
