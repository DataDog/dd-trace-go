// Package http provides functions to trace the net/http package (https://golang.org/pkg/net/http).
package http

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/contrib/internal"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
)

// ServeMux is an HTTP request multiplexer that traces all the incoming requests.
type ServeMux struct {
	*http.ServeMux
	config *muxConfig
}

// NewServeMux allocates and returns an http.ServeMux augmented with the
// global tracer.
func NewServeMux(opts ...MuxOption) *ServeMux {
	cfg := new(muxConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	cfg.tracer.SetServiceInfo(cfg.serviceName, "net/http", ext.AppTypeWeb)
	return &ServeMux{
		ServeMux: http.NewServeMux(),
		config:   cfg,
	}
}

// ServeHTTP dispatches the request to the handler
// whose pattern most closely matches the request URL.
// We only need to rewrite this function to be able to trace
// all the incoming requests to the underlying multiplexer
func (mux *ServeMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// get the resource associated to this request
	_, route := mux.Handler(r)
	resource := r.Method + " " + route
	internal.TraceAndServe(mux.ServeMux, w, r, mux.config.serviceName, resource, mux.config.tracer)
}

// WrapHandlerWithTracer wraps an http.Handler with the default tracer using the
// specified service and resource.
func WrapHandler(h http.Handler, service, resource string) http.Handler {
	return WrapHandlerWithTracer(h, service, resource, tracer.DefaultTracer)
}

// WrapHandlerWithTracer wraps an http.Handler with the given tracer using the
// specified service and resource.
//
// TODO(gbbr): Remove this once we switch to OpenTracing fully.
func WrapHandlerWithTracer(h http.Handler, service, resource string, t *tracer.Tracer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		internal.TraceAndServe(h, w, req, service, resource, t)
	})
}
