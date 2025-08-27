// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package wrap

import (
	"net/http"

	internal "github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"
	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/pattern"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

// ServeMux is an HTTP request multiplexer that traces all the incoming requests.
type ServeMux struct {
	*http.ServeMux
	cfg *internal.Config
}

// NewServeMux allocates and returns an http.ServeMux augmented with the
// global tracer.
func NewServeMux(opts ...internal.Option) *ServeMux {
	instr := internal.Instrumentation
	cfg := internal.Default(instr)
	cfg.ApplyOpts(opts...)
	cfg.SpanOpts = append(cfg.SpanOpts, tracer.Tag(ext.SpanKind, ext.SpanKindServer))
	cfg.SpanOpts = append(cfg.SpanOpts, tracer.Tag(ext.Component, internal.ComponentName))
	instr.Logger().Debug("contrib/net/http: Configuring ServeMux: %#v", cfg)
	return &ServeMux{
		ServeMux: http.NewServeMux(),
		cfg:      cfg,
	}
}

// Handle registers the handler for the given pattern.
func (mux *ServeMux) Handle(pttrn string, inner http.Handler) {
	handlerFunc := inner
	if internal.Instrumentation.AppSecEnabled() {
		// Calling TraceAndServe before `http.ServeMux.ServeHTTP` does not give enough information
		// about routing for AppSec to work properly when using the ServeMux tracing wrapper.
		// Therefore, we need to wrap the handlerFunc with a handler that finished the job here
		// after pattern data and matches are available
		// This also means stopping the handle from being called if security rules disallow it
		handlerFunc = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if httpsec.RouteMatched(r.Context(), pattern.Route(r.Pattern), pattern.PathParameters(r.Pattern, r)) != nil {
				return
			}
			inner.ServeHTTP(w, r)
		})
	}

	mux.ServeMux.Handle(pttrn, handlerFunc)
}

// HandleFunc registers the handler function for the given pattern.
func (mux *ServeMux) HandleFunc(pttrn string, handlerFunc func(http.ResponseWriter, *http.Request)) {
	mux.Handle(pttrn, http.HandlerFunc(handlerFunc))
}

// ServeHTTP dispatches the request to the handler
// whose pattern most closely matches the request URL.
// We only need to rewrite this function to be able to trace
// all the incoming requests to the underlying multiplexer
func (mux *ServeMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if mux.cfg.IgnoreRequest(r) {
		mux.ServeMux.ServeHTTP(w, r)
		return
	}
	// get the resource associated to this request
	_, pttrn := mux.Handler(r)
	route := pattern.Route(pttrn)
	resource := mux.cfg.ResourceNamer(r)
	if resource == "" {
		resource = r.Method + " " + route
	}
	so := make([]tracer.StartSpanOption, len(mux.cfg.SpanOpts), len(mux.cfg.SpanOpts)+1)
	copy(so, mux.cfg.SpanOpts)
	so = append(so, httptrace.HeaderTagsFromRequest(r, mux.cfg.HeaderTags))
	TraceAndServe(mux.ServeMux, w, r, &httptrace.ServeConfig{
		Framework:     "net/http",
		Service:       mux.cfg.ServiceName,
		Resource:      resource,
		SpanOpts:      so,
		Route:         route,
		IsStatusError: mux.cfg.IsStatusError,
	})
}
