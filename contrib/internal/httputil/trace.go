package httputil // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httputil"

import (
	"fmt"
	"net/http"
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// TraceAndServe will apply tracing to the given http.Handler using the passed tracer under the given service and resource.
func TraceAndServe(h http.Handler, w http.ResponseWriter, r *http.Request, service, resource string) {
	span, ctx := tracer.StartSpanFromContext(r.Context(), "http.request",
		tracer.SpanType(ext.AppTypeWeb),
		tracer.ServiceName(service),
		tracer.ResourceName(resource),
		tracer.Tag(ext.HTTPMethod, r.Method),
		tracer.Tag(ext.HTTPURL, r.URL.Path),
	)
	defer span.Finish()
	h.ServeHTTP(NewResponseWriter(w, span), r.WithContext(ctx))
}

// ResponseWriter is a small wrapper around an http response writer that will
// intercept and store the status of a request.
// It implements the ResponseWriter interface.
type ResponseWriter struct {
	http.ResponseWriter
	span   ddtrace.Span
	status int
}

// NewResponseWriter allocateds and returns a new ResponseWriter.
func NewResponseWriter(w http.ResponseWriter, span ddtrace.Span) *ResponseWriter {
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
	w.span.SetTag(ext.HTTPCode, strconv.Itoa(status))
	if status >= 500 && status < 600 {
		w.span.SetTag(ext.Error, fmt.Errorf("%d: %s", status, http.StatusText(status)))
	}
}
