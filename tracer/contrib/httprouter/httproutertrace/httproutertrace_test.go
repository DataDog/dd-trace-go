package httproutertrace

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/julienschmidt/httprouter"
	"github.com/stretchr/testify/assert"
)

func TestRouteWithParams(t *testing.T) {
	assert := assert.New(t)
	tracer, transport, ht, router := getTestTracer("my-service")
	router.GET("/test/:id/foo/:id2", ht.TraceHandle(handlerParams(t)))
	// Send and verify a request with a named parameter in the URL
	url := "/test/42/foo/41"
	req := httptest.NewRequest("GET", url, nil)
	writer := httptest.NewRecorder()
	router.ServeHTTP(writer, req)
	assert.Equal(200, writer.Code)
	assert.Equal("200!", writer.Body.String())

	// ensure properly traced
	tracer.ForceFlush()
	traces := transport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)

	s := spans[0]
	assert.Equal("http.request", s.Name)
	assert.Equal("my-service", s.Service)
	assert.Equal("GET /test/:id/foo/:id2", s.Resource)
	assert.Equal("200", s.GetMeta("http.status_code"))
	assert.Equal("GET", s.GetMeta("http.method"))
	assert.Equal(url, s.GetMeta("http.url"))
	assert.Equal(int32(0), s.Error)
}

func handlerParams(t *testing.T) httprouter.Handle {
	assert := assert.New(t)
	return func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		n, err := w.Write([]byte("200!"))
		assert.Equal("42", ps.ByName("id"))
		assert.Nil(err)
		assert.Equal(4, n)
		span := tracer.SpanFromContextDefault(r.Context())
		assert.Equal(span.Service, "my-service")
		assert.Equal(span.Duration, int64(0))
	}
}

func TestMiddleware(t *testing.T) {
	assert := assert.New(t)
	h200, h500 := handler200(t), handler500(t)
	tracer, transport, ht, router := getTestTracer("my-service")
	router.HandlerFunc("GET", "/500", h500)
	router.HandlerFunc("GET", "/200", h200)
	tr := ht.Middleware()(router) // Wrap the router with a traced middleware
	// Send and verify a 200 request
	url := "/200"
	req := httptest.NewRequest("GET", url, nil)
	writer := httptest.NewRecorder()
	tr.ServeHTTP(writer, req)
	assert.Equal(200, writer.Code)
	assert.Equal("200!", writer.Body.String())

	// ensure properly traced
	tracer.ForceFlush()
	traces := transport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)

	s := spans[0]
	assert.Equal("http.request", s.Name)
	assert.Equal("my-service", s.Service)
	assert.Equal("GET "+url, s.Resource)
	assert.Equal("200", s.GetMeta("http.status_code"))
	assert.Equal("GET", s.GetMeta("http.method"))
	assert.Equal(url, s.GetMeta("http.url"))
	assert.Equal(int32(0), s.Error)

}

func TestHTTPRouterTracerDisabled(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport, httprouterTracer, router := getTestTracer("disabled-service")
	router.HandlerFunc("GET", "/disabled", httprouterTracer.TraceHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte("disabled!"))
		assert.Nil(err)
		// Ensure we have no tracing context.
		span, ok := tracer.SpanFromContext(r.Context())
		assert.Nil(span)
		assert.False(ok)
	}))
	testTracer.SetEnabled(false) // the key line in this test.

	// make the request
	req := httptest.NewRequest("GET", "/disabled", nil)
	writer := httptest.NewRecorder()
	router.ServeHTTP(writer, req)
	assert.Equal(200, writer.Code)
	assert.Equal("disabled!", writer.Body.String())

	// assert nothing was traced.
	testTracer.ForceFlush()
	traces := testTransport.Traces()
	assert.Len(traces, 0)
}

func TestHTTPRouterTracer200(t *testing.T) {
	assert := assert.New(t)

	// setup
	tracer, transport, router := setup(t)

	// Send and verify a 200 request
	url := "/200"
	req := httptest.NewRequest("GET", url, nil)
	writer := httptest.NewRecorder()
	router.ServeHTTP(writer, req)
	assert.Equal(200, writer.Code)
	assert.Equal("200!", writer.Body.String())

	// ensure properly traced
	tracer.ForceFlush()
	traces := transport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)

	s := spans[0]
	assert.Equal("http.request", s.Name)
	assert.Equal("my-service", s.Service)
	assert.Equal("GET "+url, s.Resource)
	assert.Equal("200", s.GetMeta("http.status_code"))
	assert.Equal("GET", s.GetMeta("http.method"))
	assert.Equal(url, s.GetMeta("http.url"))
	assert.Equal(int32(0), s.Error)
}

func TestHTTPRouterTracer500(t *testing.T) {
	assert := assert.New(t)

	// setup
	tracer, transport, router := setup(t)

	// Send and verify a 200 request
	url := "/500"
	req := httptest.NewRequest("GET", url, nil)
	writer := httptest.NewRecorder()
	router.ServeHTTP(writer, req)
	assert.Equal(500, writer.Code)
	assert.Equal("500!\n", writer.Body.String())

	// ensure properly traced
	tracer.ForceFlush()
	traces := transport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)

	s := spans[0]
	assert.Equal("http.request", s.Name)
	assert.Equal("my-service", s.Service)
	assert.Equal("GET "+url, s.Resource)
	assert.Equal("500", s.GetMeta("http.status_code"))
	assert.Equal("GET", s.GetMeta("http.method"))
	assert.Equal(url, s.GetMeta("http.url"))
	assert.Equal(int32(1), s.Error)
}

// test handlers
func handler200(t *testing.T) http.HandlerFunc {
	assert := assert.New(t)
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte("200!"))
		assert.Nil(err)
		span := tracer.SpanFromContextDefault(r.Context())
		assert.Equal("my-service", span.Service)
		assert.Equal(int64(0), span.Duration)
	}
}

func handler500(t *testing.T) http.HandlerFunc {
	assert := assert.New(t)
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "500!", http.StatusInternalServerError)
		span := tracer.SpanFromContextDefault(r.Context())
		assert.Equal("my-service", span.Service)
		assert.Equal(int64(0), span.Duration)
	}
}

func setup(t *testing.T) (*tracer.Tracer, *dummyTransport, *httprouter.Router) {
	tracer, transport, ht, r := getTestTracer("my-service")
	h200 := handler200(t)
	h500 := handler500(t)

	r.HandlerFunc("GET", "/200", ht.TraceHandlerFunc(h200))
	r.HandlerFunc("GET", "/500", ht.TraceHandlerFunc(h500))

	return tracer, transport, r
}

// getTestTracer returns a Tracer with a DummyTransport and a router
func getTestTracer(service string) (*tracer.Tracer, *dummyTransport, *HTTPRouterTracer, *httprouter.Router) {
	router := httprouter.New()
	transport := &dummyTransport{}
	tracer := tracer.NewTracerTransport(transport)
	httprouterTracer := NewHTTPRouterTracer(service, tracer, router)
	return tracer, transport, httprouterTracer, router
}

// dummyTransport is a transport that just buffers spans and encoding
type dummyTransport struct {
	traces   [][]*tracer.Span
	services map[string]tracer.Service
}

func (t *dummyTransport) SendTraces(traces [][]*tracer.Span) (*http.Response, error) {
	t.traces = append(t.traces, traces...)
	return nil, nil
}

func (t *dummyTransport) SendServices(services map[string]tracer.Service) (*http.Response, error) {
	t.services = services
	return nil, nil
}

func (t *dummyTransport) Traces() [][]*tracer.Span {
	traces := t.traces
	t.traces = nil
	return traces
}

func (t *dummyTransport) SetHeader(key, value string) {}
