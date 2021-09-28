// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"net/http"
	"net/url"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo"
)

type (
	// HandlerOperationArgs is the HTTP handler operation arguments.
	HandlerOperationArgs struct {
		Method      Method
		Host        Host
		RequestURI  RequestURI
		RemoteAddr  RemoteAddr
		Headers     Headers
		QueryValues QueryValues
		UserAgent   UserAgent
		IsTLS       bool
	}

	// HandlerOperationRes is the HTTP handler operation results.
	HandlerOperationRes struct {
		Status int
	}

	// Method of the HTTP request.
	Method string
	// Host of the HTTP request.
	Host string
	// RequestURI of the HTTP request.
	RequestURI string
	// RemoteAddr of the HTTP request's TCP connection.
	RemoteAddr string
	// UserAgent of the HTTP request.
	UserAgent string
	// Headers of the HTTP request.
	Headers http.Header
	// QueryValues of the HTTP request.
	QueryValues url.Values
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

// OnSecurityEventData registers the given security event listener to the HTTP handler operation.
func (o HandlerOperation) OnSecurityEventData(l func(*dyngo.Operation, *types.SecurityEvent)) {
	types.OnSecurityEventData(o.Operation, l)
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
			Method:     string(args.Method),
			Host:       string(args.Host),
			IsTLS:      args.IsTLS,
			RequestURI: string(args.RequestURI),
			RemoteAddr: string(args.RemoteAddr),
		},
		Response: types.HTTPResponseContext{
			Status: res.Status,
		},
	}
}
