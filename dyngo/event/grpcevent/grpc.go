// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package grpcevent contains the definition for dyngo's gRPC events.
package grpcevent

import (
	"context"
	"net/netip"

	"gopkg.in/DataDog/dd-trace-go.v1/dyngo/internal/opcontext"
	"gopkg.in/DataDog/dd-trace-go.v1/dyngo/internal/operation"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

type (
	// HandlerOperation represents a gRPC handler operation.
	HandlerOperation struct {
		operation.Operation
		trace.TagsHolder
		trace.SecurityEventsHolder
		Error error
	}

	// HandlerOperationArgs is the gRPC handler operation's arguments.
	HandlerOperationArgs struct {
		ClientIP netip.Addr          `address:"http.client_ip"`               // The client IP address
		Metadata map[string][]string `address:"grpc.server.request.metadata"` // Request metadata
		Method   string              `address:"grpc.server.method"`           // Request gRPC method name
	}

	// HandlerOperationRes is the gRPC handler operation's results.
	HandlerOperationRes struct{}
)

// StartHandlerOperation creates and starts a new gRPC server handler operation
// using the provided parent operation and arguments. If the parent operation is
// nil, the current root operation is used.
func StartHandlerOperation(
	ctx context.Context,
	parent operation.Operation,
	args HandlerOperationArgs,
	setup ...func(*HandlerOperation),
) (context.Context, *HandlerOperation) {
	op := &HandlerOperation{
		Operation:  operation.New(parent),
		TagsHolder: trace.NewTagsHolder(),
	}
	ctx = opcontext.WithOperation(ctx, op)
	for _, cb := range setup {
		cb(op)
	}
	operation.Start(op, args)
	return ctx, op
}

// Finish finishes the gRPC handler operation with the provided result, and
// returns any security events that were observed during the operation.
func (o *HandlerOperation) Finish(res HandlerOperationRes) []any {
	operation.Finish(o, res)
	return o.Events()
}

func (HandlerOperationArgs) IsArgOf(*HandlerOperation)   {}
func (HandlerOperationRes) IsResultOf(*HandlerOperation) {}
