// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package http provides functions to trace the net/http package (https://golang.org/pkg/net/http).
package http // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"

import (
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httputil"
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/appsec/dyngo"
	httpinstr "gopkg.in/DataDog/dd-trace-go.v1/appsec/instrumentation/http"
	_ "gopkg.in/DataDog/dd-trace-go.v1/appsec/protection/http"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// ServeMux is an HTTP request multiplexer that traces all the incoming requests.
type ServeMux struct {
	*http.ServeMux
	cfg *config
}

// NewServeMux allocates and returns an http.ServeMux augmented with the
// global tracer.
func NewServeMux(opts ...Option) *ServeMux {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	log.Debug("contrib/net/http: Configuring ServeMux: %#v", cfg)
	return &ServeMux{
		ServeMux: http.NewServeMux(),
		cfg:      cfg,
	}
}

// ServeHTTP dispatches the request to the handler
// whose pattern most closely matches the request URL.
// We only need to rewrite this function to be able to trace
// all the incoming requests to the underlying multiplexer
func (mux *ServeMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := dyngo.StartOperation(
		httpinstr.HandlerOperationArgs{
			Headers:     httpinstr.Header(r.Header),
			UserAgent:   httpinstr.UserAgent(r.UserAgent()),
			QueryValues: httpinstr.QueryValues(r.URL.Query()),
		},
	)
	defer op.Finish(httpinstr.HandlerOperationRes{})
	if mux.cfg.ignoreRequest(r) {
		mux.ServeMux.ServeHTTP(w, r)
		return
	}
	// get the resource associated to this request
	_, route := mux.Handler(r)
	resource := r.Method + " " + route
	httputil.TraceAndServe(mux.ServeMux, &httputil.TraceConfig{
		ResponseWriter: w,
		Request:        r,
		Service:        mux.cfg.serviceName,
		Resource:       resource,
		SpanOpts:       mux.cfg.spanOpts,
	})
}

// WrapHandler wraps an http.Handler with tracing using the given service and resource.
func WrapHandler(h http.Handler, service, resource string, opts ...Option) http.Handler {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	log.Debug("contrib/net/http: Wrapping Handler: Service: %s, Resource: %s, %#v", service, resource, cfg)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		op := dyngo.StartOperation(func() httpinstr.HandlerOperationArgs {
			return httpinstr.HandlerOperationArgs{
				Headers:     httpinstr.Header(req.Header),
				UserAgent:   httpinstr.UserAgent(req.UserAgent()),
				QueryValues: httpinstr.QueryValues(req.URL.Query()),
			}
		})
		defer op.Finish(httpinstr.HandlerOperationRes{})
		httputil.TraceAndServe(h, &httputil.TraceConfig{
			ResponseWriter: w,
			Request:        req,
			Service:        service,
			Resource:       resource,
			FinishOpts:     cfg.finishOpts,
			SpanOpts:       cfg.spanOpts,
		})
	})
}
