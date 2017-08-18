// Package mux provides tracing functions for the Gorilla Mux framework.
package mux

import (
	"net/http"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/gorilla/mux"
)

// Router registers routes to be matched and dispatches a handler.
type Router struct {
	*mux.Router
	*tracer.Tracer
	service string
}

// NewRouter returns a new router instance.
// The last parameter is optional and allows to pass a custom tracer.
func NewRouter(service string, trc ...*tracer.Tracer) *Router {
	t := getTracer(trc)
	t.SetServiceInfo(service, "gorilla/mux", ext.AppTypeWeb)
	return &Router{mux.NewRouter(), t, service}
}

// ServeHTTP dispatches the request to the handler
// whose pattern most closely matches the request URL.
// We only need to rewrite this function to be able to trace
// all the incoming requests to the underlying multiplexer
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var match mux.RouteMatch
	var route string
	var err error

	// get the resource associated to this request
	if r.Match(req, &match) {
		route, err = match.Route.GetPathTemplate()
		if err != nil {
			route = "unkown"
		}
	} else {
		route = "unkown"
	}
	resource := req.Method + " " + route

	// we need to wrap the ServeHTTP method to be able to trace it
	httptrace.Trace(r.Router.ServeHTTP, w, req, r.service, resource, r.Tracer)
}

// getTracer returns either the tracer passed as the last argument or a default tracer.
func getTracer(tracers []*tracer.Tracer) *tracer.Tracer {
	var t *tracer.Tracer
	if len(tracers) == 0 || (len(tracers) > 0 && tracers[0] == nil) {
		t = tracer.DefaultTracer
	} else {
		t = tracers[0]
	}
	return t
}
