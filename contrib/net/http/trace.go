// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"

//go:generate sh -c "go run make_responsewriter.go | gofmt > trace_gen.go"

import (
	"fmt"
	"net/http"
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpinstr"
)

// ServeConfig defines the configuration for request tracing.
type ServeConfig struct {
	// Service set service name for tracing
	Service string
	// Resource set resource name for tracing
	Resource string
	// QueryParams specifies that request query parameters should be appended to http.url tag
	QueryParams bool
	// FinishOpts is applied to span finished
	FinishOpts []ddtrace.FinishOption
	// SpanOpts is applied to span started additionally
	SpanOpts []ddtrace.StartSpanOption
}

// TraceAndServe will apply tracing to the given http.Handler using the passed ResponseWriter, Request and ServeConfig.
func TraceAndServe(h http.Handler, w http.ResponseWriter, r *http.Request, cfg *ServeConfig) {
	path := r.URL.Path
	if cfg.QueryParams {
		path += "?" + r.URL.RawQuery
	}
	opts := append([]ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeWeb),
		tracer.ServiceName(cfg.Service),
		tracer.ResourceName(cfg.Resource),
		tracer.Tag(ext.HTTPMethod, r.Method),
		tracer.Tag(ext.HTTPURL, path),
	}, cfg.SpanOpts...)
	if r.URL.Host != "" {
		opts = append([]ddtrace.StartSpanOption{
			tracer.Tag("http.host", r.URL.Host),
		}, opts...)
	}
	if spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header)); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, ctx := tracer.StartSpanFromContext(r.Context(), "http.request", opts...)
	defer span.Finish(cfg.FinishOpts...)

	w = wrapResponseWriter(w, span)
	httpinstr.WrapHandler(h, span).ServeHTTP(w, r.WithContext(ctx))
}

// responseWriter is a small wrapper around an http response writer that will
// intercept and store the status of a request.
type responseWriter struct {
	http.ResponseWriter
	span   ddtrace.Span
	status int
}

func newResponseWriter(w http.ResponseWriter, span ddtrace.Span) *responseWriter {
	return &responseWriter{w, span, 0}
}

// Status returns the status code that was monitored.
func (w *responseWriter) Status() int {
	return w.status
}

// Write writes the data to the connection as part of an HTTP reply.
// We explicitly call WriteHeader with the 200 status code
// in order to get it reported into the span.
func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// WriteHeader sends an HTTP response header with status code.
// It also sets the status code to the span.
func (w *responseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.ResponseWriter.WriteHeader(status)
	w.status = status
	w.span.SetTag(ext.HTTPCode, strconv.Itoa(status))
	if status >= 500 && status < 600 {
		w.span.SetTag(ext.Error, fmt.Errorf("%d: %s", status, http.StatusText(status)))
	}
}
