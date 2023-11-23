// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package http provides functions to trace the net/http package (https://golang.org/pkg/net/http).
package http // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"

import (
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
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
	cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.SpanKind, ext.SpanKindServer))
	cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.Component, componentName))
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
	if mux.cfg.ignoreRequest(r) {
		mux.ServeMux.ServeHTTP(w, r)
		return
	}
	// get the resource associated to this request
	_, route := mux.Handler(r)
	resource := mux.cfg.resourceNamer(r)
	if resource == "" {
		resource = r.Method + " " + route
	}
	so := make([]ddtrace.StartSpanOption, len(mux.cfg.spanOpts), len(mux.cfg.spanOpts)+1)
	copy(so, mux.cfg.spanOpts)
	so = append(so, httptrace.HeaderTagsFromRequest(r, mux.cfg.headerTags))
	TraceAndServe(mux.ServeMux, w, r, &ServeConfig{
		Service:  mux.cfg.serviceName,
		Resource: resource,
		SpanOpts: so,
		Route:    route,
	})
}

// WrapHandler wraps an http.Handler with tracing using the given service and resource.
// If the WithResourceNamer option is provided as part of opts, it will take precedence over the resource argument.
func WrapHandler(h http.Handler, service, resource string, opts ...Option) http.Handler {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.SpanKind, ext.SpanKindServer))
	cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.Component, componentName))
	log.Debug("contrib/net/http: Wrapping Handler: Service: %s, Resource: %s, %#v", service, resource, cfg)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if cfg.ignoreRequest(req) {
			h.ServeHTTP(w, req)
			return
		}
		resc := resource
		if r := cfg.resourceNamer(req); r != "" {
			resc = r
		}
		so := make([]ddtrace.StartSpanOption, len(cfg.spanOpts), len(cfg.spanOpts)+1)
		copy(so, cfg.spanOpts)
		so = append(so, httptrace.HeaderTagsFromRequest(req, cfg.headerTags))
		TraceAndServe(h, w, req, &ServeConfig{
			Service:    service,
			Resource:   resc,
			FinishOpts: cfg.finishOpts,
			SpanOpts:   so,
		})
	})
}
