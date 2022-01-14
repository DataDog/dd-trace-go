// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package httpsec defines is the HTTP instrumentation API and contract for
// AppSec. It defines an abstract representation of HTTP handlers, along with
// helper functions to wrap (aka. instrument) standard net/http handlers.
// HTTP integrations must use this package to enable AppSec features for HTTP,
// which listens to this package's operation events.
package httpsec

import (
	"encoding/json"
	"net"
	"net/http"
	"reflect"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
)

// Abstract HTTP handler operation definition.
type (
	// HandlerOperationArgs is the HTTP handler operation arguments.
	HandlerOperationArgs struct {
		// RequestURI corresponds to the address `server.request.uri.raw`
		RequestURI string
		// Headers corresponds to the address `server.request.headers.no_cookies`
		Headers map[string][]string
		// Cookies corresponds to the address `server.request.cookies`
		Cookies []string
		// Query corresponds to the address `server.request.query`
		Query map[string][]string
	}

	// HandlerOperationRes is the HTTP handler operation results.
	HandlerOperationRes struct {
		// Status corresponds to the address `server.response.status`.
		Status int
	}
)

// WrapHandler wraps the given HTTP handler with the abstract HTTP operation defined by HandlerOperationArgs and
// HandlerOperationRes.
func WrapHandler(handler http.Handler, span ddtrace.Span) http.Handler {
	SetAppSecTags(span)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		args := MakeHandlerOperationArgs(r)
		op := StartOperation(
			args,
			nil,
		)
		defer func() {
			var status int
			if mw, ok := w.(interface{ Status() int }); ok {
				status = mw.Status()
			}
			events := op.Finish(HandlerOperationRes{Status: status})
			if len(events) == 0 {
				return
			}

			remoteIP, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				remoteIP = r.RemoteAddr
			}
			SetSecurityEventTags(span, events, remoteIP, args.Headers, w.Header())
		}()
		handler.ServeHTTP(w, r)
	})
}

// MakeHandlerOperationArgs creates the HandlerOperationArgs out of a standard
// http.Request along with the given current span. It returns an empty structure
// when appsec is disabled.
func MakeHandlerOperationArgs(r *http.Request) HandlerOperationArgs {
	headers := make(http.Header, len(r.Header))
	var cookies []string
	for k, v := range r.Header {
		k := strings.ToLower(k)
		if k == "cookie" {
			// Do not include cookies in the request headers
			cookies = v
			continue
		}
		headers[k] = v
	}
	headers["host"] = []string{r.Host}
	return HandlerOperationArgs{
		RequestURI: r.RequestURI,
		Headers:    headers,
		Cookies:    cookies,
		// TODO(Julio-Guerra): avoid actively parsing the query string and move to a lazy monitoring of this value with
		//   the dynamic instrumentation of the Query() method.
		Query: r.URL.Query(),
	}
}

// TODO(Julio-Guerra): create a go-generate tool to generate the types, vars and methods below

// Operation type representing an HTTP operation. It must be created with
// StartOperation() and finished with its Finish().
type Operation struct {
	dyngo.Operation
	events json.RawMessage
}

// StartOperation starts an HTTP handler operation, along with the given
// arguments and parent operation, and emits a start event up in the
// operation stack. When parent is nil, the operation is linked to the global
// root operation.
func StartOperation(args HandlerOperationArgs, parent dyngo.Operation) *Operation {
	op := &Operation{Operation: dyngo.NewOperation(parent)}
	dyngo.StartOperation(op, args)
	return op
}

// Finish the HTTP handler operation, along with the given results, and emits a
// finish event up in the operation stack.
func (op *Operation) Finish(res HandlerOperationRes) json.RawMessage {
	dyngo.FinishOperation(op, res)
	return op.events
}

// AddSecurityEvent adds the security event to the list of events observed
// during the operation lifetime.
func (op *Operation) AddSecurityEvent(event json.RawMessage) {
	// TODO(Julio-Guerra): the current situation involves only one event per
	//   operation. In the future, multiple events per operation will become
	//   possible and the append operation should be made thread-safe.
	op.events = event
}

// HTTP handler operation's start and finish event callback function types.
type (
	// OnHandlerOperationStart function type, called when an HTTP handler
	// operation starts.
	OnHandlerOperationStart func(*Operation, HandlerOperationArgs)
	// OnHandlerOperationFinish function type, called when an HTTP handler
	// operation finishes.
	OnHandlerOperationFinish func(*Operation, HandlerOperationRes)
)

var (
	handlerOperationArgsType = reflect.TypeOf((*HandlerOperationArgs)(nil)).Elem()
	handlerOperationResType  = reflect.TypeOf((*HandlerOperationRes)(nil)).Elem()
)

// ListenedType returns the type a OnHandlerOperationStart event listener
// listens to, which is the HandlerOperationArgs type.
func (OnHandlerOperationStart) ListenedType() reflect.Type { return handlerOperationArgsType }

// Call the underlying event listener function by performing the type-assertion
// on v whose type is the one returned by ListenedType().
func (f OnHandlerOperationStart) Call(op dyngo.Operation, v interface{}) {
	f(op.(*Operation), v.(HandlerOperationArgs))
}

// ListenedType returns the type a OnHandlerOperationFinish event listener
// listens to, which is the HandlerOperationRes type.
func (OnHandlerOperationFinish) ListenedType() reflect.Type { return handlerOperationResType }

// Call the underlying event listener function by performing the type-assertion
// on v whose type is the one returned by ListenedType().
func (f OnHandlerOperationFinish) Call(op dyngo.Operation, v interface{}) {
	f(op.(*Operation), v.(HandlerOperationRes))
}
