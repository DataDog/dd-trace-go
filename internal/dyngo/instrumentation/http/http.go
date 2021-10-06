// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"reflect"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo/instrumentation"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo/internal"
)

// Abstract HTTP handler operation definition.
type (
	// HandlerOperationArgs is the HTTP handler operation arguments.
	HandlerOperationArgs struct {
		Method     string
		Host       string
		RequestURI string
		RemoteAddr string
		// Headers without cookies
		Headers map[string][]string
		Cookies []string
		IsTLS   bool
		Span    ddtrace.Span
	}

	// HandlerOperationRes is the HTTP handler operation results.
	HandlerOperationRes struct {
		Status int
	}
)

// TODO(julio): create a go-generate tool to generate the types, vars and methods below

type Operation struct {
	*internal.OperationImpl
}

func StartOperation(args HandlerOperationArgs, parent internal.Operation) Operation {
	return Operation{OperationImpl: internal.StartOperation(args, parent)}
}
func (op Operation) Finish(res HandlerOperationRes) {
	op.OperationImpl.Finish(res)
}

type (
	OnHandlerOperationStart  func(instrumentation.Operation, HandlerOperationArgs)
	OnHandlerOperationFinish func(instrumentation.Operation, HandlerOperationRes)
)

var (
	handlerOperationArgsType = reflect.TypeOf((*HandlerOperationArgs)(nil)).Elem()
	handlerOperationResType  = reflect.TypeOf((*HandlerOperationRes)(nil)).Elem()
)

func (OnHandlerOperationStart) ListenedType() reflect.Type { return handlerOperationArgsType }
func (f OnHandlerOperationStart) Call(op instrumentation.Operation, v interface{}) {
	f(op, v.(HandlerOperationArgs))
}

func (OnHandlerOperationFinish) ListenedType() reflect.Type { return handlerOperationResType }
func (f OnHandlerOperationFinish) Call(op instrumentation.Operation, v interface{}) {
	f(op, v.(HandlerOperationRes))
}
