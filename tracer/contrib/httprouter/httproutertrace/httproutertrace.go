// Package httproutertrace provides an easy way to trace github.com/julienschmidt/httprouter
package httproutertrace

import (
	"net/http"
	"strconv"

	"strings"

	"fmt"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/julienschmidt/httprouter"
)

// HTTPRouterTracer is used to trace requests in a mux server.
type HTTPRouterTracer struct {
	tracer  *tracer.Tracer
	service string
	router  *httprouter.Router
}

// NewHTTPRouterTracer creates an HttpRouterTracer for the given service and tracer.
func NewHTTPRouterTracer(service string, t *tracer.Tracer, r *httprouter.Router) *HTTPRouterTracer {
	t.SetServiceInfo(service, "http", ext.AppTypeWeb)
	return &HTTPRouterTracer{
		tracer:  t,
		service: service,
		router:  r,
	}
}

// SetRequestSpan sets the span on the request's context.
func SetRequestSpan(r *http.Request, span *tracer.Span) *http.Request {
	if r == nil || span == nil {
		return r
	}
	ctx := span.Context(r.Context())
	return r.WithContext(ctx)
}

// trace will create a span for the given request.
func (ht *HTTPRouterTracer) trace(req *http.Request) (*http.Request, *tracer.Span) {
	path := req.URL.Path
	_, ps, _ := ht.router.Lookup(req.Method, path)
	resource := req.Method + " " + path
	for _, param := range ps {
		resource = strings.Replace(resource, param.Value, fmt.Sprintf(":%s", param.Key), 1)
	}
	span := ht.tracer.NewRootSpan("http.request", ht.service, resource)
	span.Type = ext.HTTPType
	span.SetMeta(ext.HTTPMethod, req.Method)
	span.SetMeta(ext.HTTPURL, path)

	// patch the span onto the request context.
	treq := SetRequestSpan(req, span)
	return treq, span
}

// Middleware creates a new standard http middleware to use with middleware chainers such as alice, negroni
func (ht *HTTPRouterTracer) Middleware() func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return ht.TraceHandlerFunc(h.ServeHTTP)
	}
}

// TraceHandle will return a Handle that will wrap tracing around the
// given Handle.
func (ht *HTTPRouterTracer) TraceHandle(handle httprouter.Handle) httprouter.Handle {
	return func(writer http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		// bail our if tracing isn't enabled.
		if !ht.tracer.Enabled() {
			handle(writer, req, ps)
			return
		}

		// trace the request
		tracedRequest, span := ht.trace(req)
		defer span.Finish()

		// trace the response
		tracedWriter := newTracedResponseWriter(span, writer)

		// run the request
		handle(tracedWriter, tracedRequest, ps)
	}
}

// TraceHandlerFunc will return a HandlerFunc that will wrap tracing around the
// given handler func.
func (ht *HTTPRouterTracer) TraceHandlerFunc(handler http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		ht.TraceHandle(func(writer http.ResponseWriter, req *http.Request, _ httprouter.Params) {
			handler(writer, req)
		})(writer, req, nil)
	}
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
