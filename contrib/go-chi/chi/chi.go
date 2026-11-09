// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package chi provides tracing functions for tracing the go-chi/chi package (https://github.com/go-chi/chi).
package chi // import "github.com/DataDog/dd-trace-go/contrib/go-chi/chi/v2"

import (
	"math"
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
)

const componentName = "go-chi/chi"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageChi)
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
// router behind a handler wrapper, or an ordinary handler). The parameters that
// chi already knows when this middleware runs are never replaced by the early
// check. When the early check is wrong, a detection-only rule that matches the
// wrong parameters can also hide a detection-only rule of the same type that
// matches the correct parameters later (also from a r.With(Middleware()) on the
// endpoint). AppSec cannot remove parameters that it already has: if chi finally
// selects a route without parameters, the early parameters stay.
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
	instr.Logger().Debug("contrib/go-chi/chi: Configuring Middleware: %#v", cfg)
	spanOpts := append(cfg.spanOpts, instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindServer))
	return func(next http.Handler) http.Handler {
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
			if instr.AppSecEnabled() {
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
