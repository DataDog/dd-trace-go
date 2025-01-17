// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package wrap

import (
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http/internal/config"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// ServeMux is an HTTP request multiplexer that traces all the incoming requests.
type ServeMux struct {
	*http.ServeMux
	cfg *config.Config
}

// NewServeMux allocates and returns an http.ServeMux augmented with the
// global tracer.
func NewServeMux(opts ...config.Option) *ServeMux {
	cfg := config.With(opts...)

	cfg.SpanOpts = append(cfg.SpanOpts, tracer.Tag(ext.SpanKind, ext.SpanKindServer))
	cfg.SpanOpts = append(cfg.SpanOpts, tracer.Tag(ext.Component, config.ComponentName))
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
	if mux.cfg.IgnoreRequest(r) {
		mux.ServeMux.ServeHTTP(w, r)
		return
	}
	// get the resource associated to this request
	pattern := getPattern(mux.ServeMux, r)
	route := patternRoute(pattern)
	resource := mux.cfg.ResourceNamer(r)
	if resource == "" {
		resource = r.Method + " " + route
	}
	so := make([]ddtrace.StartSpanOption, len(mux.cfg.SpanOpts), len(mux.cfg.SpanOpts)+1)
	copy(so, mux.cfg.SpanOpts)
	so = append(so, httptrace.HeaderTagsFromRequest(r, mux.cfg.HeaderTags))
	TraceAndServe(mux.ServeMux, w, r, &httptrace.ServeConfig{
		Service:       mux.cfg.ServiceName,
		Resource:      resource,
		SpanOpts:      so,
		Route:         route,
		IsStatusError: mux.cfg.IsStatusError,
		RouteParams:   patternValues(pattern, r),
	})
}
