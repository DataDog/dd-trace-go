// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package httptreemux provides functions to trace the dimfeld/httptreemux/v5 package (https://github.com/dimfeld/httptreemux).
package httptreemux // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/dimfeld/httptreemux.v5"

import (
	"net/http"
	"strings"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/dimfeld/httptreemux/v5"
)

const componentName = "dimfeld/httptreemux.v5"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/dimfeld/httptreemux/v5")
}

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
	cfg.spanOpts = append(cfg.spanOpts, tracer.Measured())
	cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.SpanKind, ext.SpanKindServer))
	cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.Component, componentName))
	log.Debug("contrib/dimfeld/httptreemux.v5: Configuring Router: %#v", cfg)
	return &Router{httptreemux.New(), cfg}
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	resource := r.config.resourceNamer(r.TreeMux, w, req)
	route, _ := getRoute(r.TreeMux, w, req)
	// pass r.TreeMux to avoid a circular reference panic on calling r.ServeHTTP
	httptrace.TraceAndServe(r.TreeMux, w, req, &httptrace.ServeConfig{
		Service:  r.config.serviceName,
		Resource: resource,
		SpanOpts: r.config.spanOpts,
		Route:    route,
	})
}

// ContextRouter is a traced version of httptreemux.ContextMux.
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
	cfg.spanOpts = append(cfg.spanOpts, tracer.Measured())
	cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.SpanKind, ext.SpanKindServer))
	cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.Component, componentName))
	log.Debug("contrib/dimfeld/httptreemux.v5: Configuring ContextRouter: %#v", cfg)
	return &ContextRouter{httptreemux.NewContextMux(), cfg}
}

// ServeHTTP implements http.Handler.
func (r *ContextRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	resource := r.config.resourceNamer(r.TreeMux, w, req)
	route, _ := getRoute(r.TreeMux, w, req)
	// pass r.TreeMux to avoid a circular reference panic on calling r.ServeHTTP
	httptrace.TraceAndServe(r.TreeMux, w, req, &httptrace.ServeConfig{
		Service:  r.config.serviceName,
		Resource: resource,
		SpanOpts: r.config.spanOpts,
		Route:    route,
	})
}

// defaultResourceNamer attempts to determine the resource name for an HTTP
// request by performing a lookup using the path template associated with the
// route from the request. If the lookup fails to find a match the route is set
// to "unknown".
func defaultResourceNamer(router *httptreemux.TreeMux, w http.ResponseWriter, req *http.Request) string {
	route, ok := getRoute(router, w, req)
	if !ok {
		route = "unknown"
	}
	return req.Method + " " + route
}

func getRoute(router *httptreemux.TreeMux, w http.ResponseWriter, req *http.Request) (string, bool) {
	route := req.URL.Path
	lr, found := router.Lookup(w, req)
	if !found {
		return "", false
	}
	routeLen := len(route)
	trailingSlash := route[routeLen-1] == '/' && routeLen > 1

	// Retry the population of lookup result parameters.
	// If the initial attempt to populate the parameters fails, clone the request and modify the URI and URL Path.
	// Depending on whether the route has a trailing slash or not, it will either add or remove the trailing slash and retry the lookup.
	if routerRedirectEnabled(router) && isSupportedRedirectStatus(lr.StatusCode) && lr.Params == nil {
		rReq := req.Clone(req.Context())
		if trailingSlash {
			// if the route has a trailing slash, remove it
			rReq.RequestURI = strings.TrimSuffix(rReq.RequestURI, "/")
			rReq.URL.Path = strings.TrimSuffix(rReq.URL.Path, "/")
		} else {
			// if the route does not have a trailing slash, add one
			rReq.RequestURI = rReq.RequestURI + "/"
			rReq.URL.Path = rReq.URL.Path + "/"
		}
		// no need to check found again
		// we already matched a route and we are only trying to populate the lookup result params
		lr, _ = router.Lookup(w, rReq)
	}

	for k, v := range lr.Params {
		// replace parameter surrounded by a set of "/", i.e. ".../:param/..."
		oldP := "/" + v + "/"
		newP := "/:" + k + "/"
		if strings.Contains(route, oldP) {
			route = strings.Replace(route, oldP, newP, 1)
			continue
		}
		// replace parameter at end of the path, i.e. "../:param"
		oldP = "/" + v
		newP = "/:" + k
		route = strings.Replace(route, oldP, newP, 1)
	}
	return route, true
}

// isSupportedRedirectStatus checks if the given HTTP status code is a supported redirect status.
func isSupportedRedirectStatus(status int) bool {
	return status == http.StatusMovedPermanently ||
		status == http.StatusTemporaryRedirect ||
		status == http.StatusPermanentRedirect
}

// routerRedirectEnabled checks if the redirection is enabled on the router.
func routerRedirectEnabled(router *httptreemux.TreeMux) bool {
	return (router.RedirectCleanPath || router.RedirectTrailingSlash) &&
		router.RedirectBehavior != httptreemux.UseHandler
}
