// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httpsec

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf/actions"
)

// HandlerOperation type representing an HTTP operation. It must be created with
// StartOperation() and finished with its Finish().
type (
	HandlerOperation struct {
		dyngo.Operation
		*waf.ContextOperation
		mu sync.RWMutex
	}

	RoundTripOperation struct {
		dyngo.Operation
	}
)

func StartOperation(ctx context.Context, args HandlerOperationArgs) (*HandlerOperation, *atomic.Pointer[actions.BlockHTTP], context.Context) {
	wafOp, ctx := waf.StartContextOperation(ctx)
	op := &HandlerOperation{
		Operation:        dyngo.NewOperation(wafOp),
		ContextOperation: wafOp,
	}

	// We need to use an atomic pointer to store the action because the action may be created asynchronously in the future
	var action atomic.Pointer[actions.BlockHTTP]
	dyngo.OnData(op, func(a *actions.BlockHTTP) {
		action.Store(a)
	})

	return op, &action, dyngo.StartAndRegisterOperation(ctx, op, args)
}

// Finish the HTTP handler operation and its children operations and write everything to the service entry span.
func (op *HandlerOperation) Finish(res HandlerOperationRes, span ddtrace.Span) {
	dyngo.FinishOperation(op, res)
	op.ContextOperation.Finish(span)
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

func (HandlerOperationArgs) IsArgOf(*HandlerOperation)   {}
func (HandlerOperationRes) IsResultOf(*HandlerOperation) {}

func (RoundTripOperationArgs) IsArgOf(*RoundTripOperation)   {}
func (RoundTripOperationRes) IsResultOf(*RoundTripOperation) {}
