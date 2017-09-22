package http

import (
	"net/http"
	"strconv"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
)

type ServeHTTP func(http.ResponseWriter, *http.Request)

// Trace will trace the request before calling the ServeHTTP function passed in argument.
func Trace(serveHTTP ServeHTTP, w http.ResponseWriter, r *http.Request, service, resource string, t *tracer.Tracer) {
	// bail out if tracing isn't enabled
	if !t.Enabled() {
		serveHTTP(w, r)
		return
	}

	// TODO: get the span from the request context
	span := t.NewRootSpan("http.request", service, resource)
	defer span.Finish()

	span.Type = ext.HTTPType
	span.SetMeta(ext.HTTPMethod, r.Method)
	span.SetMeta(ext.HTTPURL, r.URL.Path)

	// pass the span through the request context
	ctx := span.Context(r.Context())
	traceRequest := r.WithContext(ctx)

	// trace the response to get the status code
	traceWriter := NewResponseWriter(w, span)

	// serve the request to the underlying multiplexer
	serveHTTP(traceWriter, traceRequest)
}

// ResponseWriter is a small wrapper around an http response writer that will
// intercept and store the status of a request.
// It implements the ResponseWriter interface.
type ResponseWriter struct {
	http.ResponseWriter
	span   *tracer.Span
	status int
}

// New ResponseWriter allocateds and returns a new ResponseWriter.
func NewResponseWriter(w http.ResponseWriter, span *tracer.Span) *ResponseWriter {
	return &ResponseWriter{w, span, 0}
}

// Write writes the data to the connection as part of an HTTP reply.
// We explicitely call WriteHeader with the 200 status code
// in order to get it reported into the span.
func (w *ResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// WriteHeader sends an HTTP response header with status code.
// It also sets the status code to the span.
func (w *ResponseWriter) WriteHeader(status int) {
	w.ResponseWriter.WriteHeader(status)
	w.status = status
	w.span.SetMeta(ext.HTTPCode, strconv.Itoa(status))
	if status >= 500 && status < 600 {
		w.span.Error = 1
	}
}
