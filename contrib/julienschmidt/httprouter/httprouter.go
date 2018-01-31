// Package httprouter provides functions to trace the julienschmidt/httprouter package (https://github.com/julienschmidt/httprouter).
package httprouter

import (
	"net/http"
	"strings"

	"github.com/julienschmidt/httprouter"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"

	"github.com/DataDog/dd-trace-go/contrib/internal"
)

// Router is a traced version of httprouter.Router.
type Router struct {
	*httprouter.Router
	tracer  *tracer.Tracer
	service string
}

// New returns a new router augmented with tracing.
func New() *Router {
	return NewWithServiceName("httprouter.router", tracer.DefaultTracer)
}

// NewWithServiceName returns a new Router which is traced under the given
// service name.
//
// TODO(gbbr): Remove tracer arg. when we switch to OT.
func NewWithServiceName(service string, t *tracer.Tracer) *Router {
	t.SetServiceInfo(service, "julienschmidt/httprouter", ext.AppTypeWeb)
	return &Router{httprouter.New(), t, service}
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
	internal.TraceAndServe(r.Router, w, req, r.service, resource, r.tracer)
}
