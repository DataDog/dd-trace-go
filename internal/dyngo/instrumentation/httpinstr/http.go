// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package httpinstr defines the HTTP operation that can be listened to using
// dyngo's operation instrumentation. It serves as an abstract representation
// of HTTP handler calls.
package httpinstr

import (
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	appsectypes "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo"
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
		span.SetTag("_dd.appsec.enabled", 0)
		return handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span.SetTag("_dd.appsec.enabled", 1)
		span.SetTag("_dd.runtime_family", "go")

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
	*dyngo.OperationImpl
}

func StartOperation(args HandlerOperationArgs, parent dyngo.Operation) Operation {
	return Operation{OperationImpl: dyngo.StartOperation(args, parent)}
}
func (op Operation) Finish(res HandlerOperationRes) {
	op.OperationImpl.Finish(res)
}

type (
	OnHandlerOperationStart  func(dyngo.Operation, HandlerOperationArgs)
	OnHandlerOperationFinish func(dyngo.Operation, HandlerOperationRes)
)

var (
	handlerOperationArgsType = reflect.TypeOf((*HandlerOperationArgs)(nil)).Elem()
	handlerOperationResType  = reflect.TypeOf((*HandlerOperationRes)(nil)).Elem()
)

func (OnHandlerOperationStart) ListenedType() reflect.Type { return handlerOperationArgsType }
func (f OnHandlerOperationStart) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(HandlerOperationArgs))
}

func (OnHandlerOperationFinish) ListenedType() reflect.Type { return handlerOperationResType }
func (f OnHandlerOperationFinish) Call(op dyngo.Operation, v interface{}) {
	f(op, v.(HandlerOperationRes))
}
