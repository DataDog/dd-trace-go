// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo"
)

type (
	// HandlerOperationArgs is the HTTP handler operation arguments.
	HandlerOperationArgs struct {
		Method     string
		Host       string
		RequestURI string
		RemoteAddr string
		Headers    http.Header
		IsTLS      bool
		Span       ddtrace.Span
	}

	// HandlerOperationRes is the HTTP handler operation results.
	HandlerOperationRes struct {
		Status int
	}
)

// TODO(julio): once these helpers validated, we should rely on a go-generate tool to generate those types and methods

func init() {
	dyngo.RegisterOperation((*HandlerOperationArgs)(nil), (*HandlerOperationRes)(nil))
}

// HandlerOperation is a helper type returned by StartHandlerOperation allowing start, finish and use the HTTP handler
// operation in a simpler and type-safe way than what bare dyngo operations provide.
type HandlerOperation struct {
	*dyngo.Operation
}

// StartHandlerOperation starts an HTTP handler operation with the given arguments.
func StartHandlerOperation(args HandlerOperationArgs, opts ...dyngo.Option) HandlerOperation {
	return HandlerOperation{dyngo.StartOperation(args, opts...)}
}

// Finish finishes the HTTP handler operation with the given results.
func (o HandlerOperation) Finish(res HandlerOperationRes) {
	o.Operation.Finish(res)
}

// OnHandlerOperationStartListener returns an operation start event listener for HTTP operations. This event listener
// is suitable for calls to dyngo.(*Operation).Register.
func OnHandlerOperationStartListener(l func(*dyngo.Operation, HandlerOperationArgs)) dyngo.EventListener {
	return dyngo.OnStartEventListener((*HandlerOperationArgs)(nil), func(op *dyngo.Operation, v interface{}) {
		l(op, v.(HandlerOperationArgs))
	})
}

// OnHandlerOperationStart registers the given HTTP operation start listener to operation.
func OnHandlerOperationStart(op *dyngo.Operation, l func(*dyngo.Operation, HandlerOperationArgs)) {
	op.OnStart((*HandlerOperationArgs)(nil), func(op *dyngo.Operation, v interface{}) {
		l(op, v.(HandlerOperationArgs))
	})
}

// OnHandlerOperationFinish registers the given HTTP operation start listener to operation.
func OnHandlerOperationFinish(op *dyngo.Operation, l func(*dyngo.Operation, HandlerOperationRes)) {
	op.OnFinish((*HandlerOperationRes)(nil), func(op *dyngo.Operation, v interface{}) {
		l(op, v.(HandlerOperationRes))
	})
}

// MakeHTTPOperationContext creates an HTTP operation context from HTTP operation arguments and results.
// This context can be added to a security event.
func MakeHTTPOperationContext(args HandlerOperationArgs, res HandlerOperationRes) types.HTTPOperationContext {
	return types.HTTPOperationContext{
		Request: types.HTTPRequestContext{
			Method:     args.Method,
			Host:       args.Host,
			IsTLS:      args.IsTLS,
			RequestURI: args.RequestURI,
			RemoteAddr: args.RemoteAddr,
		},
		Response: types.HTTPResponseContext{
			Status: res.Status,
		},
	}
}
