// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package chi provides tracing functions for tracing the go-chi/chi package (https://github.com/go-chi/chi).
package chi // import "github.com/DataDog/dd-trace-go/contrib/go-chi/chi/v2"

import (
	"math"
	"net/http"

	"github.com/DataDog/dd-trace-go/contrib/go-chi/chi/v2/internal/endpointhook"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
)

const componentName = "go-chi/chi"

type endpointRouteReporter struct{}

func (endpointRouteReporter) ReportedRoute(rctx *chi.Context) string { return rctx.RoutePattern() }

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageChi)
}

// Middleware returns middleware that will trace incoming requests.
//
// # AppSec
//
// Add Middleware before any middleware that writes or flushes the response, and
// make the response writers of the middlewares after it implement Unwrap(): AppSec
// must see the response to decide if a block can still replace it.
//
// With Orchestrion or otelc, no code change is necessary. They add Middleware to
// the routers that chi.NewRouter and chi.NewMux return, and they wrap the endpoint
// handlers of chi. After chi selected the endpoint, the wrapper gives AppSec the
// route and the path parameters, before the handler: a rule on a path parameter can
// then block the request before the handler. For a name used at 2 levels, AppSec
// gets the value of the endpoint. AppSec gets "*" only for a catch-all route
// registered with a method, such as Get("/files/*", h), and it gets the value of
// that route. The nearest Middleware decides whether the wrapper runs: one that ignores the request with
// WithIgnoreRequest turns the wrapper off.
//
// Without Orchestrion or otelc, chi runs a Middleware added with Use before routing:
// AppSec gets no path parameters, and it gets the route after the handler. To give
// AppSec the path parameters before the handler, add Middleware where chi already
// selected the endpoint: on the endpoint (r.With(Middleware()).Get(...)), or with Use
// in the Group that registers the endpoint. AppSec then also gets "*", and for a name
// used at 2 levels, the last value. A Group around Route or Mount does not get the
// path parameters of the child router. If you also keep a Middleware at the root,
// AppSec also covers the requests that match no endpoint (404, 405) and the
// middlewares before routing, and each matched request gets 1 more span. Both
// middlewares use the same AppSec request context.
func Middleware(opts ...Option) func(next http.Handler) http.Handler {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn.apply(cfg)
	}
	instr.Logger().Debug("contrib/go-chi/chi: Configuring Middleware: %#v", cfg)
	spanOpts := append(cfg.spanOpts, instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindServer))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			appsecEnabled := instr.AppSecEnabled()
			mark := appsecEnabled && endpointhook.Woven()
			if cfg.ignoreRequest(r) {
				if mark {
					r = r.WithContext(endpointhook.With(r.Context(), endpointhook.Marker{Disabled: true}))
				}
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
			if mark {
				if rctx := chi.RouteContext(ctx); rctx != nil {
					ctx = endpointhook.With(ctx, endpointhook.Marker{Depth: len(rctx.RoutePatterns), Route: endpointRouteReporter{}})
				}
			}
			r = r.WithContext(ctx)

			next := next // avoid modifying the value of next in the outer closure scope
			if appsecEnabled {
				next = withAppsec(next, span)
				// Note that the following response writer passed to the handler
				// implements the `interface { Status() int }` expected by httpsec.
			}

			// pass the span through the request context and serve the request to the next middleware
			next.ServeHTTP(ww, r)
			span.SetTag(ext.HTTPRoute, chi.RouteContext(r.Context()).RoutePattern())
			span.SetTag(ext.ResourceName, cfg.resourceNamer(r))
		})
	}
}
