package httputil // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httputil"

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// TraceAndServe will apply tracing to the given http.Handler using the passed tracer under the given service and resource.
func TraceAndServe(h http.Handler, w http.ResponseWriter, r *http.Request, service, resource string, spanopts ...ddtrace.StartSpanOption) {
	opts := append([]ddtrace.StartSpanOption{
		tracer.SpanType(ext.AppTypeWeb),
		tracer.ServiceName(service),
		tracer.ResourceName(resource),
		tracer.Tag(ext.HTTPMethod, r.Method),
		tracer.Tag(ext.HTTPURL, r.URL.Path),
	}, spanopts...)
	if spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header)); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, ctx := tracer.StartSpanFromContext(r.Context(), "http.request", opts...)
	defer span.Finish()
	if _, ok := w.(http.Hijacker); ok {
		w = newHijackableResponseWriter(w, span)
	} else {
		w = newResponseWriter(w, span)
	}
	h.ServeHTTP(w, r.WithContext(ctx))
}

// responseWriter is a small wrapper around an http response writer that will
// intercept and store the status of a request.
type responseWriter struct {
	http.ResponseWriter
	span   ddtrace.Span
	status int
}

var (
	_ http.Hijacker       = (*hijackableResponseWriter)(nil)
	_ http.ResponseWriter = (*hijackableResponseWriter)(nil)
	_ http.ResponseWriter = (*responseWriter)(nil)
)

type hijackableResponseWriter struct{ *responseWriter }

func newHijackableResponseWriter(w http.ResponseWriter, span ddtrace.Span) *hijackableResponseWriter {
	return &hijackableResponseWriter{newResponseWriter(w, span)}
}

func (hrw *hijackableResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := hrw.responseWriter.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("underlying ResponseWriter does not implement http.Hijacker")
	}
	return h.Hijack()
}

func newResponseWriter(w http.ResponseWriter, span ddtrace.Span) *responseWriter {
	return &responseWriter{w, span, 0}
}

// Write writes the data to the connection as part of an HTTP reply.
// We explicitely call WriteHeader with the 200 status code
// in order to get it reported into the span.
func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// WriteHeader sends an HTTP response header with status code.
// It also sets the status code to the span.
func (w *responseWriter) WriteHeader(status int) {
	w.ResponseWriter.WriteHeader(status)
	w.status = status
	w.span.SetTag(ext.HTTPCode, strconv.Itoa(status))
	if status >= 500 && status < 600 {
		w.span.SetTag(ext.Error, fmt.Errorf("%d: %s", status, http.StatusText(status)))
	}
}
