// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httpsec

import (
	"context"
	"net/http"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf/actions"
)

// Operation type representing an HTTP operation. It must be created with
// StartOperation() and finished with its Finish().
type (
	Operation struct {
		dyngo.Operation
		*waf.ContextOperation
		mu sync.RWMutex
	}

	RoundTripOperation struct {
		dyngo.Operation
	}
)

func StartOperation(ctx context.Context, args HandlerOperationArgs) (*Operation, *actions.BlockHTTP, context.Context) {
	var action actions.BlockHTTP
	wafOp, ctx := waf.StartContextOperation(ctx)
	op := &Operation{
		Operation:        dyngo.NewOperation(wafOp),
		ContextOperation: wafOp,
	}

	dyngo.OnData(op, func(a *actions.BlockHTTP) {
		action = *a
	})

	return op, &action, dyngo.StartAndRegisterOperation(ctx, op, args)
}

// Finish the HTTP handler operation and its children operations and write everything to the service entry span.
func (op *Operation) Finish(res HandlerOperationRes, span ddtrace.Span) {
	dyngo.FinishOperation(op, res)
	op.ServiceEntrySpanOperation.Finish(span)
}

// Abstract HTTP handler operation definition.
type (
	// HandlerOperationArgs is the HTTP handler operation arguments.
	HandlerOperationArgs struct {
		*http.Request
		PathParams map[string]string
	}

	// HandlerOperationRes is the HTTP handler operation results.
	HandlerOperationRes struct {
		http.ResponseWriter
		ResponseHeaderCopier func(http.ResponseWriter) http.Header
	}

	// RoundTripOperationArgs is the round trip operation arguments.
	RoundTripOperationArgs struct {
		// URL corresponds to the address `server.io.net.url`.
		URL string
	}

	// RoundTripOperationRes is the round trip operation results.
	RoundTripOperationRes struct{}
)

func (HandlerOperationArgs) IsArgOf(*Operation)   {}
func (HandlerOperationRes) IsResultOf(*Operation) {}

func (RoundTripOperationArgs) IsArgOf(*RoundTripOperation)   {}
func (RoundTripOperationRes) IsResultOf(*RoundTripOperation) {}
