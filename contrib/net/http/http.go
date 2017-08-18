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
func NewServeMux(service string, t *tracer.Tracer) *ServeMux {
	if t == nil {
		t = tracer.DefaultTracer
	}
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
