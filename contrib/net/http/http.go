package http

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
)

// ServeMux is an HTTP request multiplexer that traces all the incoming requests.
type ServeMux struct {
	*http.ServeMux
	*tracer.Tracer
	service string
}

// NewServeMux allocates and returns a new ServeMux.
// The last parameter is optional and allows to pass a custom tracer.
func NewServeMux(service string, trc ...*tracer.Tracer) *ServeMux {
	t := getTracer(trc)
	t.SetServiceInfo(service, "net/http", ext.AppTypeWeb)
	return &ServeMux{http.NewServeMux(), t, service}
}

// ServeHTTP dispatches the request to the handler
// whose pattern most closely matches the request URL.
// We only need to rewrite this function to be able to trace
// all the incoming requests to the underlying multiplexer
func (mux *ServeMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// get the resource associated to this request
	_, route := mux.Handler(r)
	resource := r.Method + " " + route

	// we need to wrap the ServeHTTP method to be able to trace it
	Trace(mux.ServeMux.ServeHTTP, w, r, mux.service, resource, mux.Tracer)
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
