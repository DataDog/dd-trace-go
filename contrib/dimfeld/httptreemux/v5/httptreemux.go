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
	resource := r.config.resourceNamer(r.TreeMux, w, req)
	// pass r.TreeMux to avoid a circular reference panic on calling r.ServeHTTP
	httptrace.TraceAndServe(r.TreeMux, w, req, &httptrace.ServeConfig{
		Service:  r.config.serviceName,
		Resource: resource,
		SpanOpts: r.config.spanOpts,
	})
}

// Router is a traced version of httptreemux.ContextMux.
type ContextRouter struct {
	*httptreemux.ContextMux
	config *routerConfig
}

// NewWithContext returns a new router augmented with tracing and preconfigured
// to work with context objects. The matched route and parameters are added to
// the context.
func NewWithContext(opts ...RouterOption) *ContextRouter {
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
	return &ContextRouter{httptreemux.NewContextMux(), cfg}
}

// ServeHTTP implements http.Handler.
func (r *ContextRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	resource := r.config.resourceNamer(r.TreeMux, w, req)
	// pass r.TreeMux to avoid a circular reference panic on calling r.ServeHTTP
	httptrace.TraceAndServe(r.TreeMux, w, req, &httptrace.ServeConfig{
		Service:  r.config.serviceName,
		Resource: resource,
		SpanOpts: r.config.spanOpts,
	})
}

// defaultResourceNamer attempts to determine the resource name for an HTTP
// request by performing a lookup using the path template associated with the
// route from the request. If the lookup fails to find a match the route is set
// to "unknown".
func defaultResourceNamer(router *httptreemux.TreeMux, w http.ResponseWriter, req *http.Request) string {
	route := req.URL.Path
	lr, found := router.Lookup(w, req)
	if !found {
		return req.Method + " unknown"
	}
	for k, v := range lr.Params {
		// replace parameter surrounded by a set of "/", i.e. ".../:param/..."
		old := "/" + v + "/"
		new := "/" + ":" + k + "/"
		if strings.Contains(route, old) {
			route = strings.Replace(route, old, new, 1)
			continue
		}
		// replace parameter at end of the path, i.e. "../:param"
		old = "/" + v
		new = "/" + ":" + k
		route = strings.Replace(route, old, new, 1)
	}
	return req.Method + " " + route
}
