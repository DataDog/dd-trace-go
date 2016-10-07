package muxtrace

import (
	"log"
	"net/http"
	"strconv"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/gorilla/mux"
)

type MuxTracer struct {
	tracer  *tracer.Tracer
	service string
}

func NewMuxTracer(service string, t *tracer.Tracer) *MuxTracer {
	log.Println("new mux tracer") // KILLME
	return &MuxTracer{
		tracer:  t,
		service: service,
	}
}

func (m *MuxTracer) TraceHandlerFunc(handler http.HandlerFunc) http.HandlerFunc {

	return func(w http.ResponseWriter, req *http.Request) {

		// trace the request
		span := m.span(req)
		defer span.Finish()
		writer := newTracedResponseWriter(span, w)

		// run the request
		handler(writer, req)
	}
}

// span will create a span for the given request.
func (m *MuxTracer) span(req *http.Request) *tracer.Span {
	resource := getResource(req)
	span := m.tracer.NewSpan("mux.request", m.service, resource)
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
	t.span.SetMeta("http.status_code", strconv.Itoa(status))
}
