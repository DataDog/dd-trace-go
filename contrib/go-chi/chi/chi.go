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
func Middleware(opts ...Option) func(next http.Handler) http.Handler {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn.apply(cfg)
	}
	instr.Logger().Debug("contrib/go-chi/chi: Configuring Middleware: %#v", cfg)
	spanOpts := append(cfg.spanOpts, tracer.ServiceName(cfg.serviceName),
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
				next = withAppsec(next, r, span)
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
