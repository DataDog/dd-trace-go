// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package httpevent contains the definitions for dyngo's HTTP events.
package httpevent

import (
	"context"
	"net/netip"

	"github.com/datadog/dd-trace-go/dyngo/internal/opcontext"
	"github.com/datadog/dd-trace-go/dyngo/internal/operation"
)

type (
	// HandlerOperation represents a HTTP handler operation.
	HandlerOperation struct {
		operation.Operation
		context.Context
	}

	// HandlerOperationArgs is the HTTP handler operation arguments.
	HandlerOperationArgs struct {
		ClientIP   netip.Addr          // The client IP address
		Headers    map[string][]string // Request headers
		Cookies    map[string][]string // Request cookies
		Query      map[string][]string // Request query parameters
		PathParams map[string]string   // Request path parameters
		Method     string              // Request method (GET, PUT, POST, ...)
		RequestURI string              // Request URI
	}

	// HandlerOperationRes is the HTTP handler operation results.
	HandlerOperationRes struct {
		Headers map[string][]string // Response headers
		Status  int                 // Response status code (200, 404, ...)
	}
)

// StartHandlerOperation creates and startsa new HTTP handler operation with the
// provided arguments.
func StartHandlerOperation(
	ctx context.Context,
	args HandlerOperationArgs,
	setup ...func(*HandlerOperation),
) (context.Context, *HandlerOperation) {
	op := &HandlerOperation{Operation: operation.New(nil), Context: ctx}
	for _, fn := range setup {
		fn(op)
	}
	operation.Start(op, args)
	return opcontext.WithOperation(ctx, op), op
}

// Finish finishes the receiving HTTP handler operation.
func (op *HandlerOperation) Finish(res HandlerOperationRes) {
	operation.Finish(op, res)
}

func (HandlerOperationArgs) IsArgOf(*HandlerOperation)   {}
func (HandlerOperationRes) IsResultOf(*HandlerOperation) {}
