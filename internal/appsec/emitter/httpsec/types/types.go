// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package types

import (
	"context"
	"net/netip"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf"
)

// Operation type representing an HTTP operation. It must be created with
// StartOperation() and finished with its Finish().
type (
	Operation struct {
		dyngo.Operation
		waf.ContextOperation
		mu sync.RWMutex
	}

	// SDKBodyOperation type representing an SDK body
	SDKBodyOperation struct {
		dyngo.Operation
	}

	RoundTripOperation struct {
		dyngo.Operation
	}
)

func (op *Operation) Start(ctx context.Context, args HandlerOperationArgs) context.Context {
	return dyngo.StartAndRegisterOperation(op.ContextOperation.Start(ctx), op, args)
}

// Finish the HTTP handler operation and its children operations and write everything to the service entry span.
func (op *Operation) Finish(res HandlerOperationRes, span ddtrace.Span) []any {
	dyngo.FinishOperation(op, res)
	op.ServiceEntrySpanOperation.Finish(span)
	return op.Events()
}

// Abstract HTTP handler operation definition.
type (
	// HandlerOperationArgs is the HTTP handler operation arguments.
	HandlerOperationArgs struct {
		// ClientIP corresponds to the address `http.client_ip`
		ClientIP netip.Addr
		// Headers corresponds to the address `server.request.headers.no_cookies`
		Headers map[string][]string
		// Cookies corresponds to the address `server.request.cookies`
		Cookies map[string][]string
		// Query corresponds to the address `server.request.query`
		Query map[string][]string
		// PathParams corresponds to the address `server.request.path_params`
		PathParams map[string]string
		// Method is the http method verb of the request, address is `server.request.method`
		Method string
		// RequestURI corresponds to the address `server.request.uri.raw`
		RequestURI string
	}

	// HandlerOperationRes is the HTTP handler operation results.
	HandlerOperationRes struct {
		Headers map[string][]string
		// Status corresponds to the address `server.response.status`.
		Status int
	}

	// SDKBodyOperationArgs is the SDK body operation arguments.
	SDKBodyOperationArgs struct {
		// Body corresponds to the address `server.request.body`.
		Body any
	}

	// SDKBodyOperationRes is the SDK body operation results.
	SDKBodyOperationRes struct{}

	// RoundTripOperationArgs is the round trip operation arguments.
	RoundTripOperationArgs struct {
		// URL corresponds to the address `server.io.net.url`.
		URL string
	}

	// RoundTripOperationRes is the round trip operation results.
	RoundTripOperationRes struct{}
)

// Finish finishes the SDKBody operation and emits a finish event
func (op *SDKBodyOperation) Finish() {
	dyngo.FinishOperation(op, SDKBodyOperationRes{})
}

func (SDKBodyOperationArgs) IsArgOf(*SDKBodyOperation)   {}
func (SDKBodyOperationRes) IsResultOf(*SDKBodyOperation) {}

func (HandlerOperationArgs) IsArgOf(*Operation)   {}
func (HandlerOperationRes) IsResultOf(*Operation) {}

func (RoundTripOperationArgs) IsArgOf(*RoundTripOperation)   {}
func (RoundTripOperationRes) IsResultOf(*RoundTripOperation) {}
