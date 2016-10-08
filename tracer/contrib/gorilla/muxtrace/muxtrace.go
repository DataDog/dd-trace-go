package muxtrace

import (
	"net/http"
	"strconv"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/gorilla/mux"
)

// MuxTracer is used to trace requests in a mux server.
type MuxTracer struct {
	tracer  *tracer.Tracer
	service string
}

// NewMuxTracer creates a MuxTracer for the given service and tracer.
func NewMuxTracer(service string, t *tracer.Tracer) *MuxTracer {
	return &MuxTracer{
		tracer:  t,
		service: service,
	}
}

// TraceHandleFunc will return a HandlerFunc that will wrap tracing around the
// given handler func.
func (m *MuxTracer) TraceHandleFunc(handler http.HandlerFunc) http.HandlerFunc {

	return func(writer http.ResponseWriter, req *http.Request) {

		// trace the request
		treq, span := m.trace(req)
		defer span.Finish()
		// trace the response
		twriter := newTracedResponseWriter(span, writer)

		// run the request
		handler(twriter, treq)
	}
}

// HandleFunc will add a traced version of the given handler to the router.
func (m *MuxTracer) HandleFunc(router *mux.Router, pattern string, handler http.HandlerFunc) *mux.Route {
	return router.HandleFunc(pattern, m.TraceHandleFunc(handler))
}

// span will create a span for the given request.
func (m *MuxTracer) trace(req *http.Request) (*http.Request, *tracer.Span) {
	resource := getResource(req)

	span := m.tracer.NewSpan("mux.request", m.service, resource)
	span.Type = ext.HTTPType
	span.SetMeta(ext.HTTPMethod, req.Method)

	// patch the span onto the request context.
	treq := setOnRequestContext(req, span)
	return treq, span
}

// getResource returns a resource name for the given http requests. Must be
// called in the scope of a mux handler.
func getResource(req *http.Request) string {
	route := mux.CurrentRoute(req)
	path, err := route.GetPathTemplate()
	if err != nil {
		path = "unknown" // FIXME[matt] when will this happen?
	}
	return req.Method + " " + path
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
		w:    w}
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
}
