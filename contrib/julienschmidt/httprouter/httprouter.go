// Package httprouter provides functions to trace the julienschmidt/httprouter package (https://github.com/julienschmidt/httprouter).
package httprouter

import (
	"net/http"
	"strings"

	"github.com/julienschmidt/httprouter"

	"github.com/DataDog/dd-trace-go/tracer/ext"

	"github.com/DataDog/dd-trace-go/contrib/internal"
)

// Router is a traced version of httprouter.Router.
type Router struct {
	*httprouter.Router
	config *routerConfig
}

// New returns a new router augmented with tracing.
func New(opts ...RouterOption) *Router {
	cfg := new(routerConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	cfg.tracer.SetServiceInfo(cfg.serviceName, "julienschmidt/httprouter", ext.AppTypeWeb)
	return &Router{httprouter.New(), cfg}
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// get the resource associated to this request
	route := req.URL.Path
	_, ps, _ := r.Router.Lookup(req.Method, route)
	for _, param := range ps {
		route = strings.Replace(route, param.Value, ":"+param.Key, 1)
	}
	resource := req.Method + " " + route
	internal.TraceAndServe(r.Router, w, req, r.config.serviceName, resource, r.config.tracer)
}
