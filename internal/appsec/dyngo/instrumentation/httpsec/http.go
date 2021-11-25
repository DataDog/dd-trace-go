// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package httpsec defines the HTTP operation that can be listened to using
// dyngo's operation instrumentation. It serves as an abstract representation
// of HTTP handler calls.
package httpsec

import (
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
)

// Abstract HTTP handler operation definition.
type (
	// HandlerOperationArgs is the HTTP handler operation arguments.
	HandlerOperationArgs struct {
		Method     string
		Host       string
		RemoteAddr string
		Path       string
		IsTLS      bool
		Span       ddtrace.Span

		// RequestURI corresponds to the address `server.request.uri.raw`
		RequestURI string
		// Headers corresponds to the address `server.request.headers.no_cookies`
		Headers map[string][]string
		// Cookies corresponds to the address `server.request.cookies`
		Cookies map[string][]string
		// Query corresponds to the address `server.request.query`
		Query url.Values
	}

	// HandlerOperationRes is the HTTP handler operation results.
	HandlerOperationRes struct {
		// Status corresponds to the address `server.response.status`
		Status int
	}
)

var enabled bool

func init() {
	enabled, _ = strconv.ParseBool(os.Getenv("DD_APPSEC_ENABLED"))
}

// WrapHandler wraps the given HTTP handler with the abstract HTTP operation defined by HandlerOperationArgs and
// HandlerOperationRes.
func WrapHandler(handler http.Handler, span ddtrace.Span) http.Handler {
	// TODO(Julio-Guerra): move these to service entry tags
	if !enabled {
		span.SetTag("_dd.appsec.enabled", 0)
		return handler
	}
	span.SetTag("_dd.appsec.enabled", 1)
	span.SetTag("_dd.runtime_family", "go")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op := StartOperation(
			makeHandlerOperationArgs(r, span),
			nil,
		)
		defer func() {
			var status int
			if mw, ok := w.(interface{ Status() int }); ok {
				status = mw.Status()
			}
			op.Finish(HandlerOperationRes{Status: status})
		}()
		handler.ServeHTTP(w, r)
	})
}

// MakeHandlerOperationArgs creates the HandlerOperationArgs out of a standard
// http.Request along with the given current span. It returns an empty structure
// when appsec is disabled.
func MakeHandlerOperationArgs(r *http.Request, span ddtrace.Span) HandlerOperationArgs {
	return makeHandlerOperationArgs(r, span)
}

// makeHandlerOperationArgs implements MakeHandlerOperationArgs regardless of appsec being disabled.
func makeHandlerOperationArgs(r *http.Request, span ddtrace.Span) HandlerOperationArgs {
	headers := make(http.Header, len(r.Header))
	for k, v := range r.Header {
		k := strings.ToLower(k)
		if k == "cookie" {
			// Do not include cookies in the request headers
			continue
		}
		headers[k] = v
	}
	var cookies map[string][]string
	if reqCookies := r.Cookies(); len(reqCookies) > 0 {
		cookies = make(map[string][]string, len(reqCookies))
		for _, cookie := range reqCookies {
			if cookie == nil {
				continue
			}
			cookies[cookie.Name] = append(cookies[cookie.Name], cookie.Value)
		}
	}
	headers["host"] = []string{r.Host}
	return HandlerOperationArgs{
		Span:       span,
		IsTLS:      r.TLS != nil,
		Method:     r.Method,
		Host:       r.Host,
		Path:       r.URL.Path,
		RequestURI: r.RequestURI,
		RemoteAddr: r.RemoteAddr,
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
	*dyngo.OperationImpl
}

// StartOperation starts an HTTP handler operation, along with the given
// arguments and parent operation, and emits a start event up in the
// operation stack. When parent is nil, the operation is linked to the global
// root operation.
func StartOperation(args HandlerOperationArgs, parent dyngo.Operation) Operation {
	return Operation{OperationImpl: dyngo.StartOperation(args, parent)}
}

// Finish the HTTP handler operation, along with the given results, and emits a
// finish event up in the operation stack.
func (op Operation) Finish(res HandlerOperationRes) {
	op.OperationImpl.Finish(res)
}

// HTTP handler operation's start and finish event callback function types.
type (
	// OnHandlerOperationStart function type, called when an HTTP handler
	// operation starts.
	OnHandlerOperationStart func(dyngo.Operation, HandlerOperationArgs)
	// OnHandlerOperationFinish function type, called when an HTTP handler
	// operation finishes.
	OnHandlerOperationFinish func(dyngo.Operation, HandlerOperationRes)
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
	f(op, v.(HandlerOperationArgs))
}

// ListenedType returns the type a OnHandlerOperationFinish event listener
// listens to, which is the HandlerOperationRes type.
func (OnHandlerOperationFinish) ListenedType() reflect.Type { return handlerOperationResType }

// Call the underlying event listener function by performing the type-assertion
// on v whose type is the one returned by ListenedType().
func (f OnHandlerOperationFinish) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(HandlerOperationRes))
}
