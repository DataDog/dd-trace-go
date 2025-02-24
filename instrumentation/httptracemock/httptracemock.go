// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptracemock

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
)

// ServeMux is an HTTP request multiplexer that traces all the incoming requests.
type ServeMux struct {
	*http.ServeMux
	spanOpts []tracer.StartSpanOption
}

// NewServeMux allocates and returns an http.ServeMux augmented with the
// global tracer.
func NewServeMux() *ServeMux {
	spanOpts := []tracer.StartSpanOption{
		tracer.Tag(ext.SpanKind, ext.SpanKindServer),
		tracer.Tag(ext.Component, "net/http"),
	}
	return &ServeMux{
		ServeMux: http.NewServeMux(),
		spanOpts: spanOpts,
	}
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// ServeHTTP dispatches the request to the handler
// whose pattern most closely matches the request URL.
// We only need to rewrite this function to be able to trace
// all the incoming requests to the underlying multiplexer
func (mux *ServeMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// get the resource associated to this request
	_, route := mux.Handler(r)

	resource := r.Method + " " + route
	so := make([]tracer.StartSpanOption, len(mux.spanOpts), len(mux.spanOpts)+1)
	copy(so, mux.spanOpts)
	so = append(so, tracer.ResourceName(resource))

	rw := &responseWriter{ResponseWriter: w}
	span, ctx, finishSpans := httptrace.StartRequestSpan(r, so...)
	defer func() {
		finishSpans(rw.statusCode, nil)
	}()
	var h http.Handler = mux.ServeMux
	if appsec.Enabled() {
		h = httpsec.WrapHandler(h, span, nil, nil)
	}
	h.ServeHTTP(rw, r.WithContext(ctx))
}
