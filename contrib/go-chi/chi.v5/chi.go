// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package chi provides tracing functions for tracing the go-chi/chi/v5 package (https://github.com/go-chi/chi).
package chi // import "github.com/DataDog/dd-trace-go/contrib/go-chi/chi.v5/v2"

import (
	"math"
	"net/http"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"

	"github.com/go-chi/chi/v5/middleware"
)

const componentName = "go-chi/chi.v5"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageChiV5)
}

// Middleware returns middleware that will trace incoming requests.
//
// # AppSec and the route
//
// With Use before routing, chi selects the route after this middleware starts.
// Before the handler runs, the middleware asks chi which route matches
// ([chi.Routes.Match]) and gives the path parameters of that route to AppSec: a
// rule on path parameters can block before the handler. This early check can use
// other parameters than the handler gets when a middleware changes the path, the
// method or the route path (before or after this middleware), or when a mount
// does not give the request to a chi router that chi can search (for example a
// router behind a handler wrapper, or an ordinary handler). If the early check
// does not extend the route that chi already matched when this middleware runs
// (for example after a middleware changed the route path of a parent mount), it
// is dropped, and AppSec keeps the parameters that chi matched. When the early
// check is wrong, a detection-only rule that matches the wrong parameters can
// also hide a detection-only rule of the same type that matches the correct
// parameters later (also from a r.With(Middleware()) on the endpoint). The
// early check never changes a parameter that chi already matched when this
// middleware runs: a prediction with another value (for example when a name is
// used at several levels of mounts) is dropped, and the "*" of a mount that this
// middleware wraps (r.With(Middleware()).Mount) keeps its value. Before the
// handler, a name that is used at several levels has its first value; after the
// handler, AppSec reports the last value, the one that chi.URLParam returns.
// AppSec cannot clear parameters: if chi finally selects a route without
// parameters, the early parameters stay.
//
// After the handler returns, AppSec gets the route and the path parameters that
// chi selected. A block or a redirect at that time replaces the response only if
// the response is not sent yet: it never stops the handler, which already ran. If
// the response is sent, it does not change; AppSec reports a failed block (a
// redirect is not counted as a block). A flush that returns an error does not
// itself mark the response as sent: if it wrote part of the response, a block can
// add its body after that output.
//
// The middlewares between this middleware and the handler must send the response
// through the writer that they get, and return it from Unwrap(). When this
// middleware cannot see every way the response can start (for example a writer
// that can hijack the connection only through Unwrap(), or a writer without
// Unwrap() between 2 of these middlewares), it gives no newly discovered path
// parameters to AppSec for the request: the parameters that chi already knows
// when it runs still go to AppSec, as before. A middleware that replaces both
// the writer and the request context between 2 of these middlewares hides the
// outer one, and the inner one cannot see it. With a writer without Unwrap()
// between 2 of these middlewares and a flush between them, a block on path
// parameters and response data can add its body after the response (a block on
// response data alone does the same before this version).
//
// A middleware that writes or flushes the response must come after this
// middleware: AppSec cannot see a response that was sent before it, and a block
// can then add its body after that response.
//
// With r.With(Middleware()) on the matched endpoint, the route is known before
// the handler.
func Middleware(opts ...Option) func(next http.Handler) http.Handler {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn.apply(cfg)
	}
	instr.Logger().Debug("contrib/go-chi/chi.v5: Configuring Middleware: %#v", cfg)
	spanOpts := append(cfg.spanOpts, instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindServer))
	return func(next http.Handler) http.Handler {
		// Maintain a cache of recorded pattern chain -> unmatched.
		// The cache is scoped to this handler because a
		// chain is only meaningful relative to the router that recorded it.
		var subrouterCache sync.Map
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.ignoreRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			opts := options.Expand(spanOpts, 0, 2) // opts must be a copy of spanOpts, locally scoped, to avoid races.
			if !math.IsNaN(cfg.analyticsRate) {
				opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
			}
			opts = append(opts, httptrace.HeaderTagsFromRequest(r, cfg.headerTags))
			span, ctx, finishSpans := httptrace.StartRequestSpan(r, opts...)
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				status := ww.Status()
				finishSpans(status, cfg.isStatusError)
			}()

			// pass the span through the request context
			r = r.WithContext(ctx)

			next := next // avoid modifying the value of next in the outer closure scope
			if instr.AppSecEnabled() && !cfg.appsecDisabled {
				next = withAppsec(next, httptrace.AppSecSpanTagSetter(span, cfg.otelEnabled), cfg, &subrouterCache)
				// Note that the following response writer passed to the handler
				// implements the `interface { Status() int }` expected by httpsec.
			}

			// pass the span through the request context and serve the request to the next middleware
			next.ServeHTTP(ww, r)

			routePattern := reportedRoute(chi.RouteContext(r.Context()), cfg, &subrouterCache)
			if routePattern != "" || !cfg.otelEnabled {
				span.SetTag(ext.HTTPRoute, routePattern)
			}
			if cfg.otelEnabled {
				// Chi exposes the final route pattern only after dispatch, so endpoint tagging
				// must happen here rather than when the span starts.
				if endpoint, ok := httptrace.HTTPEndpoint(routePattern, r); ok {
					span.SetTag(ext.HTTPEndpoint, endpoint)
				}
			}
			var resourceName string
			if cfg.resourceNamer != nil {
				resourceName = cfg.resourceNamer(r)
			} else if cfg.otelEnabled {
				resourceName = httptrace.ServerSpanName(r.Method, routePattern)
			} else {
				resourceName = routePattern
				if resourceName == "" {
					resourceName = "unknown"
				}
				resourceName = r.Method + " " + resourceName
			}
			span.SetTag(ext.ResourceName, resourceName)
		})
	}
}

// reportedRoute returns the route that chi selected for the request, as the
// http.route span tag and AppSec report it. It is only final after chi
// dispatched the request.
func reportedRoute(rctx *chi.Context, cfg *config, cache *sync.Map) string {
	routePattern := rctx.RoutePattern()
	if cfg.otelEnabled && unmatchedSubrouter(rctx, cache) {
		routePattern = ""
	}
	if routePattern != "" || !cfg.otelEnabled {
		routePattern = cfg.modifyResourceName(routePattern)
	}
	return routePattern
}

// unmatchedSubrouter checks whether routing stopped at a Chi mount instead of an
// endpoint. Inspect the recorded patterns so middleware path rewrites are respected.
// The answer is cached: resolving it calls Routes(), which rebuilds the router's
// whole route table, and catch-all patterns reach this on every request.
func unmatchedSubrouter(rctx *chi.Context, cache *sync.Map) bool {
	if rctx == nil || rctx.Routes == nil || len(rctx.RoutePatterns) == 0 {
		return false
	}
	last := rctx.RoutePatterns[len(rctx.RoutePatterns)-1]
	// Mount registers /prefix, /prefix/, and /prefix/*. Ordinary endpoints
	// cannot be incomplete mounts and need no further lookup.
	if !strings.HasSuffix(last, "/*") && rctx.RoutePath != "/" {
		return false
	}
	// Key on the entire chain, joining the individual patterns with NUL
	// (which route patterns never contain) to distinguish between an otherwise
	// identical pattern with different chains.
	// E.g. ["/api", "/*"] and ["/api/*"]
	const patternSep = "\x00"
	key := strings.Join(rctx.RoutePatterns, patternSep)
	if unmatched, ok := cache.Load(key); ok {
		return unmatched.(bool)
	}
	unmatched := true
	routes := rctx.Routes
	for _, pattern := range rctx.RoutePatterns {
		var subroutes chi.Routes
		for _, route := range routes.Routes() {
			if route.Pattern == pattern {
				subroutes = route.SubRoutes
				break
			}
			// Chi omits a mount's /prefix and /prefix/ aliases from Routes().
			if strings.HasSuffix(route.Pattern, "/*") && strings.TrimSuffix(route.Pattern, "/*") == strings.TrimSuffix(pattern, "/") {
				subroutes = route.SubRoutes
			}
		}
		if subroutes == nil {
			unmatched = false
			break
		}
		routes = subroutes
	}
	cache.Store(key, unmatched)
	return unmatched
}
