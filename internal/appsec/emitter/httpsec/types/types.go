// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package types

import (
	"net/netip"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

// Operation type representing an HTTP operation. It must be created with
// StartOperation() and finished with its Finish().
type (
	Operation struct {
		dyngo.Operation
		trace.TagsHolder
		trace.SecurityEventsHolder
		mu sync.RWMutex
	}

	// SDKBodyOperation type representing an SDK body
	SDKBodyOperation struct {
		dyngo.Operation
	}
)

// Finish the HTTP handler operation, along with the given results and emits a
// finish event up in the operation stack.
func (op *Operation) Finish(res HandlerOperationRes) []any {
	dyngo.FinishOperation(op, res)
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
		Body interface{}
	}

	// SDKBodyOperationRes is the SDK body operation results.
	SDKBodyOperationRes struct{}

	// MonitoringError is used to vehicle an HTTP error, usually resurfaced through Appsec SDKs.
	MonitoringError struct {
		msg string
	}
)

// Finish finishes the SDKBody operation and emits a finish event
func (op *SDKBodyOperation) Finish() {
	dyngo.FinishOperation(op, SDKBodyOperationRes{})
}

// Error implements the Error interface
func (e *MonitoringError) Error() string {
	return e.msg
}

// NewMonitoringError creates and returns a new HTTP monitoring error, wrapped under
// sharedesec.MonitoringError
func NewMonitoringError(msg string) error {
	return &MonitoringError{
		msg: msg,
	}
}

func (SDKBodyOperationArgs) IsArgOf(*SDKBodyOperation)   {}
func (SDKBodyOperationRes) IsResultOf(*SDKBodyOperation) {}

func (HandlerOperationArgs) IsArgOf(*Operation)   {}
func (HandlerOperationRes) IsResultOf(*Operation) {}
