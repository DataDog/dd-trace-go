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
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec"
)

// ServeConfig specifies the tracing configuration when using TraceAndServe.
type ServeConfig struct {
	// Service specifies the service name to use. If left blank, the global service name
	// will be inherited.
	Service string
	// Resource optionally specifies the resource name for this request.
	Resource string
	// QueryParams specifies any query parameters that be appended to the resulting "http.url" tag.
	QueryParams bool
	// RouteParams specifies framework-specific route parameters (e.g. for route /user/:id coming
	// in as /user/123 we'll have {"id": "123"}). This field is optional and is used for monitoring
	// by AppSec. It is only taken into account when AppSec is enabled.
	RouteParams map[string]string
	// FinishOpts specifies any options to be used when finishing the request span.
	FinishOpts []ddtrace.FinishOption
	// SpanOpts specifies any options to be applied to the request starting span.
	SpanOpts []ddtrace.StartSpanOption
}

// TraceAndServe serves the handler h using the given ResponseWriter and Request, applying tracing
// according to the specified config.
func TraceAndServe(h http.Handler, w http.ResponseWriter, r *http.Request, cfg *ServeConfig) {
	if cfg == nil {
		cfg = new(ServeConfig)
	}
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

	if appsec.Enabled() {
		h = httpsec.WrapHandler(h, span, cfg.RouteParams)
	}
	h.ServeHTTP(wrapResponseWriter(w, span), r.WithContext(ctx))
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
