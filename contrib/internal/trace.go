package internal

import (
	"net/http"
	"strconv"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
)

// TraceAndServe will apply tracing to the given http.Handler using the passed tracer under the given service and resource.
func TraceAndServe(h http.Handler, w http.ResponseWriter, r *http.Request, service, resource string, t *tracer.Tracer) {
	// bail out if tracing isn't enabled
	if !t.Enabled() {
		h.ServeHTTP(w, r)
		return
	}

	span, ctx := t.NewChildSpanWithContext("http.request", r.Context())
	defer span.Finish()

	span.Type = ext.HTTPType
	span.Service = service
	span.Resource = resource
	span.SetMeta(ext.HTTPMethod, r.Method)
	span.SetMeta(ext.HTTPURL, r.URL.Path)

	traceRequest := r.WithContext(ctx)
	traceWriter := NewResponseWriter(w, span)

	h.ServeHTTP(traceWriter, traceRequest)
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
