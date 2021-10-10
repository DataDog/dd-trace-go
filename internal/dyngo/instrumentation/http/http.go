// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	appsectypes "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo/instrumentation"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo/internal"
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
		Cookies []string
		// Query corresponds to the address `server.request.query`
		Query url.Values
	}

	// HandlerOperationRes is the HTTP handler operation results.
	HandlerOperationRes struct {
		// Status corresponds to the address `server.response.status`
		Status int
	}
)

// MakeHTTPContext returns the HTTP context from the HTTP handler operation arguments and results.
func MakeHTTPContext(args HandlerOperationArgs, res HandlerOperationRes) appsectypes.HTTPContext {
	return appsectypes.HTTPContext{
		Request: appsectypes.HTTPRequestContext{
			Method:     args.Method,
			Host:       args.Host,
			IsTLS:      args.IsTLS,
			RequestURI: args.RequestURI,
			Path:       args.Path,
			RemoteAddr: args.RemoteAddr,
			Headers:    args.Headers,
			Query:      args.Query,
		},
		Response: appsectypes.HTTPResponseContext{
			Status: res.Status,
		},
	}
}

// WrapHandler wraps the given HTTP handler with the abstract HTTP operation defined by HandlerOperationArgs and
// HandlerOperationRes.
func WrapHandler(handler http.Handler, span ddtrace.Span) http.Handler {
	if os.Getenv("DD_APPSEC_ENABLED") == "" {
		return handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var (
			headers = make(http.Header, len(r.Header))
			cookies []string
		)
		for k, v := range r.Header {
			k := strings.ToLower(k)
			if k == "cookie" {
				// Save the cookies value and do not include them in the request headers
				cookies = v
				continue
			}
			headers[k] = v
		}
		host := r.Host
		headers["host"] = []string{host}
		op := StartOperation(
			HandlerOperationArgs{
				Span:       span,
				IsTLS:      r.TLS != nil,
				Method:     r.Method,
				Host:       r.Host,
				Path:       r.URL.Path,
				RequestURI: r.RequestURI,
				RemoteAddr: r.RemoteAddr,
				Headers:    headers,
				Cookies:    cookies,
				// TODO(julio): avoid actively parsing the query string and move to a lazy monitoring of this value with
				//   the dynamic instrumentation of the Query() method.
				Query: r.URL.Query(),
			},
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
