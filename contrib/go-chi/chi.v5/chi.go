// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package chi provides tracing functions for tracing the go-chi/chi/v5 package (https://github.com/go-chi/chi).
package chi // import "gopkg.in/CodapeWild/dd-trace-go.v1/contrib/go-chi/chi.v5"

import (
	"fmt"
	"math"
	"net/http"
	"strconv"

	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/CodapeWild/dd-trace-go.v1/internal/log"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Middleware returns middleware that will trace incoming requests.
func Middleware(opts ...Option) func(next http.Handler) http.Handler {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	log.Debug("contrib/go-chi/chi.v5: Configuring Middleware: %#v", cfg)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			opts := []ddtrace.StartSpanOption{
				tracer.SpanType(ext.SpanTypeWeb),
				tracer.ServiceName(cfg.serviceName),
				tracer.Tag(ext.HTTPMethod, r.Method),
				tracer.Tag(ext.HTTPURL, r.URL.Path),
				tracer.Measured(),
			}
			if !math.IsNaN(cfg.analyticsRate) {
				opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
			}
			if spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header)); err == nil {
				opts = append(opts, tracer.ChildOf(spanctx))
			}
			opts = append(opts, cfg.spanOpts...)
			span, ctx := tracer.StartSpanFromContext(r.Context(), "http.request", opts...)
			defer span.Finish()

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			// pass the span through the request context and serve the request to the next middleware
			next.ServeHTTP(ww, r.WithContext(ctx))

			// set the resource name as we get it only once the handler is executed
			resourceName := chi.RouteContext(r.Context()).RoutePattern()
			if resourceName == "" {
				resourceName = "unknown"
			}
			resourceName = r.Method + " " + resourceName
			span.SetTag(ext.ResourceName, resourceName)

			// set the status code
			status := ww.Status()
			// 0 status means one has not yet been sent in which case net/http library will write StatusOK
			if ww.Status() == 0 {
				status = http.StatusOK
			}
			span.SetTag(ext.HTTPCode, strconv.Itoa(status))

			if cfg.isStatusError(status) {
				// mark 5xx server error
				span.SetTag(ext.Error, fmt.Errorf("%d: %s", status, http.StatusText(status)))
			}
		})
	}
}
