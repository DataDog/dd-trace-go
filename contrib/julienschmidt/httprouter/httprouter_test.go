package httprouter

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/julienschmidt/httprouter"
	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/tracertest"
)

func TestHttpTracerDisabled(t *testing.T) {
	assert := assert.New(t)

	testTracer, testTransport := tracertest.GetTestTracer()
	testTracer.SetEnabled(false)

	router := NewWithServiceName("my-service", testTracer)
	router.GET("/disabled", func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		_, err := w.Write([]byte("disabled!"))
		assert.Nil(err)

		// Ensure we have no tracing context
		span, ok := tracer.SpanFromContext(r.Context())
		assert.Nil(span)
		assert.False(ok)
	})

	// Make the request
	r := httptest.NewRequest("GET", "/disabled", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	assert.Equal(200, w.Code)
	assert.Equal("disabled!", w.Body.String())

	// Assert nothing was traced
	testTracer.ForceFlush()
	traces := testTransport.Traces()
	assert.Len(traces, 0)
}

func TestHttpTracer200(t *testing.T) {
	assert := assert.New(t)
	tracer, transport, router := setup(t)

	// Send and verify a 200 request
	url := "/200"
	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	assert.Equal(200, w.Code)
	assert.Equal("200!\n", w.Body.String())

	// Ensure the request is properly traced
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

func TestHttpTracer500(t *testing.T) {
	assert := assert.New(t)
	tracer, transport, router := setup(t)

	// Send and verify a 500 request
	url := "/500"
	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	assert.Equal(500, w.Code)
	assert.Equal("500!\n", w.Body.String())

	// Ensure the request is properly traced
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

func setup(t *testing.T) (*tracer.Tracer, *tracertest.DummyTransport, http.Handler) {
	h200 := handler200(t)
	h500 := handler500(t)
	tracer, transport := tracertest.GetTestTracer()

	router := NewWithServiceName("my-service", tracer)
	router.GET("/200", h200)
	router.GET("/500", h500)

	return tracer, transport, router
}

func handler200(t *testing.T) httprouter.Handle {
	assert := assert.New(t)
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		_, err := w.Write([]byte("200!\n"))
		assert.Nil(err)

		span := tracer.SpanFromContextDefault(r.Context())
		assert.Equal("my-service", span.Service)
		assert.Equal(int64(0), span.Duration)
	}
}

func handler500(t *testing.T) httprouter.Handle {
	assert := assert.New(t)
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		http.Error(w, "500!", http.StatusInternalServerError)
		span := tracer.SpanFromContextDefault(r.Context())

		assert.Equal("my-service", span.Service)
		assert.Equal(int64(0), span.Duration)
	}
}
