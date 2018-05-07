// Package mux provides tracing functions for tracing the gorilla/mux package (https://github.com/gorilla/mux).
package mux // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/gorilla/mux"

import (
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httputil"

	"github.com/gorilla/mux"
)

// Router registers routes to be matched and dispatches a handler.
type Router struct {
	*mux.Router
	config *routerConfig
}

// NewRouter returns a new router instance traced with the global tracer.
func NewRouter(opts ...RouterOption) *Router {
	cfg := new(routerConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	return &Router{
		Router: mux.NewRouter(),
		config: cfg,
	}
}

// ServeHTTP dispatches the request to the handler
// whose pattern most closely matches the request URL.
// We only need to rewrite this function to be able to trace
// all the incoming requests to the underlying multiplexer
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var (
		match mux.RouteMatch
		route string
		err   error
	)
	// get the resource associated to this request
	if r.Match(req, &match) {
		if match.Route != nil {
			route, err = match.Route.GetPathTemplate()
			if err != nil {
				route = "unknown"
			}
		} else {
			route = "unknown"
		}
	} else {
		route = "unknown"
	}
	resource := req.Method + " " + route
	httputil.TraceAndServe(r.Router, w, req, r.config.serviceName, resource)
}
