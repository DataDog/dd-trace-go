// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package httpevent contains the definitions for dyngo's HTTP events.
package httpevent

import (
	"context"
	"net/netip"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/dyngo/internal/opcontext"
	"gopkg.in/DataDog/dd-trace-go.v1/dyngo/internal/operation"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

type (
	// HandlerOperation represents a HTTP handler operation.
	HandlerOperation struct {
		operation.Operation
		trace.TagsHolder
		trace.SecurityEventsHolder
		mu sync.Mutex
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
	op := &HandlerOperation{
		Operation:  operation.New(nil),
		TagsHolder: trace.NewTagsHolder(),
	}
	ctx = opcontext.WithOperation(ctx, op)
	for _, fn := range setup {
		fn(op)
	}
	operation.Start(op, args)
	return ctx, op
}

// Finish finishes the receiving HTTP handler operation.
func (op *HandlerOperation) Finish(res HandlerOperationRes) []any {
	operation.Finish(op, res)
	return op.Events()
}

func (HandlerOperationArgs) IsArgOf(*HandlerOperation)   {}
func (HandlerOperationRes) IsResultOf(*HandlerOperation) {}
