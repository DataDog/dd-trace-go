package httptrace

import (
	"net/http"
	"strconv"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
)

// HttpTracer is used to trace requests in a net/http server.
type HttpTracer struct {
	tracer  *tracer.Tracer
	service string
}

// NewHttpTracer creates a HttpTracer for the given service and tracer.
func NewHttpTracer(service string, t *tracer.Tracer) *HttpTracer {
	t.SetServiceInfo(service, "net/http", ext.AppTypeWeb)
	return &HttpTracer{
		tracer:  t,
		service: service,
	}
}

// Handler will return a Handler that will wrap tracing around the
// given handler.
func (h *HttpTracer) Handler(handler http.Handler) http.Handler {
	return h.TraceHandlerFunc(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		handler.ServeHTTP(writer, req)
	}))
}

// TraceHandlerFunc will return a HandlerFunc that will wrap tracing around the
// given handler func.
func (h *HttpTracer) TraceHandlerFunc(handler http.HandlerFunc) http.HandlerFunc {

	return func(writer http.ResponseWriter, req *http.Request) {

		// bail out if tracing isn't enabled.
		if !h.tracer.Enabled() {
			handler(writer, req)
			return
		}

		// trace the request
		tracedRequest, span := h.trace(req)
		defer span.Finish()

		// trace the response
		tracedWriter := newTracedResponseWriter(span, writer)

		// run the request
		handler(tracedWriter, tracedRequest)
	}
}

// span will create a span for the given request.
func (h *HttpTracer) trace(req *http.Request) (*http.Request, *tracer.Span) {
	resource := req.Method + " " + req.URL.Path

	span := h.tracer.NewRootSpan("http.request", h.service, resource)
	span.Type = ext.HTTPType
	span.SetMeta(ext.HTTPMethod, req.Method)
	span.SetMeta(ext.HTTPURL, req.URL.Path)

	// patch the span onto the request context.
	treq := SetRequestSpan(req, span)
	return treq, span
}

// tracedResponseWriter is a small wrapper around an http response writer that will
// intercept and store the status of a request.
type tracedResponseWriter struct {
	span   *tracer.Span
	w      http.ResponseWriter
	status int
}

func newTracedResponseWriter(span *tracer.Span, w http.ResponseWriter) *tracedResponseWriter {
	return &tracedResponseWriter{
		span: span,
		w:    w,
	}
}

func (t *tracedResponseWriter) Header() http.Header {
	return t.w.Header()
}

func (t *tracedResponseWriter) Write(b []byte) (int, error) {
	if t.status == 0 {
		t.WriteHeader(http.StatusOK)
	}
	return t.w.Write(b)
}

func (t *tracedResponseWriter) WriteHeader(status int) {
	t.w.WriteHeader(status)
	t.status = status
	t.span.SetMeta(ext.HTTPCode, strconv.Itoa(status))
	if status >= 500 && status < 600 {
		t.span.Error = 1
	}
}

// SetRequestSpan sets the span on the request's context.
func SetRequestSpan(r *http.Request, span *tracer.Span) *http.Request {
	if r == nil || span == nil {
		return r
	}

	ctx := tracer.ContextWithSpan(r.Context(), span)
	return r.WithContext(ctx)
}

// GetRequestSpan will return the span associated with the given request. It
// will return nil/false if it doesn't exist.
func GetRequestSpan(r *http.Request) (*tracer.Span, bool) {
	span, ok := tracer.SpanFromContext(r.Context())
	return span, ok
}
