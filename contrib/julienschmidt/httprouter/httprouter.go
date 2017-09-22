package httprouter

import (
	"net/http"
	"strings"

	"github.com/julienschmidt/httprouter"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http"
)

// Router is a traced version of httprouter.Router.
type Router struct {
	*httprouter.Router
	*tracer.Tracer
	service string
}

// New returns a new initialized Router.
// The last parameter is optional and allows to pass a custom tracer.
func New(service string, trc ...*tracer.Tracer) *Router {
	t := getTracer(trc)
	t.SetServiceInfo(service, "julienschmidt/httprouter", ext.AppTypeWeb)
	return &Router{httprouter.New(), t, service}
}

// ServeHTTP makes the router implement the http.Handler interface.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// get the resource associated to this request
	route := req.URL.Path
	_, ps, _ := r.Router.Lookup(req.Method, route)
	for _, param := range ps {
		route = strings.Replace(route, param.Value, ":"+param.Key, 1)
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
