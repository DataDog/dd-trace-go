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
// With Use before routing, chi selects the route after this middleware starts. The
// WAF gets the route and the path parameters that chi knows when the middleware
// starts, as before the route was reported: with Use before routing, no path
// parameter, so a rule on path parameters does not match. Use
// r.With(Middleware()) on the endpoint for these rules: chi knows the route and
// its path parameters before the middleware starts.
//
// After the handler returns, the middleware reports the route that chi selected to
// AppSec (API Security uses it as the sampling key of the endpoint). It gives no
// path parameters to the WAF at any time: this report adds no WAF evaluation. A
// block or a redirect that the WAF stored replaces the response only if the
// response is not sent yet: it never stops the handler, which already ran. If the
// response is sent, it does not change; AppSec reports a failed block (a redirect
// is not counted as a block). A flush that returns an error does not itself mark
// the response as sent: if it wrote part of the response, a block can add its body
// after that output. A flush (direct, or through http.ResponseController) that
// reaches a writer that does not send it (for example a compressing writer that
// ignores a flush before the first write, or a buffering writer that returns nil)
// does not mark the response as sent either, unless the writer that really flushes
// is a known server writer (net/http HTTP/1 and HTTP/2, golang.org/x/net/http2,
// httptest.ResponseRecorder) or an outer middleware of this kind that recorded the
// flush; if the writer sent the output, a block can then add its body after it, as
// before this version.
//
// The middlewares between this middleware and the handler must send the response
// through the writer that they get, and return it from Unwrap(). With a writer
// without Unwrap() between 2 of these middlewares and a flush between them, a block
// on response data can add its body after the response (as before this version).
//
// A middleware that writes or flushes the response must come after this
// middleware: AppSec cannot see a response that was sent before it, and a block
// can then add its body after that response.
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
