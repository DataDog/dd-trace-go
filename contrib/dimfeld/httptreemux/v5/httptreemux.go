// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package httptreemux provides functions to trace the dimfeld/httptreemux/v5 package (https://github.com/dimfeld/httptreemux).
package httptreemux // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/dimfeld/httptreemux/v5"

import (
	"math"
	"net/http"
	"strings"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/dimfeld/httptreemux/v5"
)

// Router is a traced version of httptreemux.TreeMux.
type Router struct {
	*httptreemux.TreeMux
	config *routerConfig
}

// New returns a new router augmented with tracing.
func New(opts ...RouterOption) *Router {
	cfg := new(routerConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	if !math.IsNaN(cfg.analyticsRate) {
		cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
	cfg.spanOpts = append(cfg.spanOpts, tracer.Measured())
	log.Debug("contrib/dimfeld/httptreemux/v5: Configuring Router: %#v", cfg)
	return &Router{httptreemux.New(), cfg}
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// get the resource associated to this request
	route := req.URL.Path
	lr, _ := r.Lookup(w, req)
	for k, v := range lr.Params {
		// replace parameter values in URL path with their names
		route = strings.Replace(route, v, ":"+k, 1)
	}
	resource := req.Method + " " + route
	// pass r.TreeMux to avoid a circular reference panic on calling r.ServeHTTP
	httptrace.TraceAndServe(r.TreeMux, w, req, &httptrace.ServeConfig{
		Service:  r.config.serviceName,
		Resource: resource,
		SpanOpts: r.config.spanOpts,
	})
}
