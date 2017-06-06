package muxtrace

import (
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/test"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMuxTracerDisabled(t *testing.T) {
	assert := assert.New(t)

	testTracer, testTransport := test.GetTestTracer()
	muxTracer := NewMuxTracer("disabled-service", testTracer)
	router := mux.NewRouter()
	muxTracer.HandleFunc(router, "/disabled", func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte("disabled!"))
		assert.Nil(err)
		// Ensure we have no tracing context.
		span, ok := tracer.SpanFromContext(r.Context())
		assert.Nil(span)
		assert.False(ok)
	})
	testTracer.SetEnabled(false) // the key line in this test.

	// make the request
	req := httptest.NewRequest("GET", "/disabled", nil)
	writer := httptest.NewRecorder()
	router.ServeHTTP(writer, req)
	assert.Equal(writer.Code, 200)
	assert.Equal(writer.Body.String(), "disabled!")

	// assert nothing was traced.
	assert.Nil(testTracer.FlushTraces())
	traces := testTransport.Traces()
	assert.Len(traces, 0)
}

func TestMuxTracerSubrequest(t *testing.T) {
	assert := assert.New(t)

	// Send and verify a 200 request
	for _, url := range []string{"/sub/child1", "/sub/child2"} {
		tracer, transport, router := setup(t)
		req := httptest.NewRequest("GET", url, nil)
		writer := httptest.NewRecorder()
		router.ServeHTTP(writer, req)
		assert.Equal(writer.Code, 200)
		assert.Equal(writer.Body.String(), "200!")

		// ensure properly traced
		assert.Nil(tracer.FlushTraces())
		traces := transport.Traces()
		assert.Len(traces, 1)
		spans := traces[0]
		assert.Len(spans, 1)

		s := spans[0]
		assert.Equal(s.Name, "mux.request")
		assert.Equal(s.Service, "my-service")
		assert.Equal(s.Resource, "GET "+url)
		assert.Equal(s.GetMeta("http.status_code"), "200")
		assert.Equal(s.GetMeta("http.method"), "GET")
		assert.Equal(s.GetMeta("http.url"), url)
		assert.Equal(s.Error, int32(0))
	}
}

func TestMuxTracer200(t *testing.T) {
	assert := assert.New(t)

	// setup
	tracer, transport, router := setup(t)

	// Send and verify a 200 request
	url := "/200"
	req := httptest.NewRequest("GET", url, nil)
	writer := httptest.NewRecorder()
	router.ServeHTTP(writer, req)
	assert.Equal(writer.Code, 200)
	assert.Equal(writer.Body.String(), "200!")

	// ensure properly traced
	assert.Nil(tracer.FlushTraces())
	traces := transport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)

	s := spans[0]
	assert.Equal(s.Name, "mux.request")
	assert.Equal(s.Service, "my-service")
	assert.Equal(s.Resource, "GET "+url)
	assert.Equal(s.GetMeta("http.status_code"), "200")
	assert.Equal(s.GetMeta("http.method"), "GET")
	assert.Equal(s.GetMeta("http.url"), url)
	assert.Equal(s.Error, int32(0))
}

func TestMuxTracer500(t *testing.T) {
	assert := assert.New(t)

	// setup
	tracer, transport, router := setup(t)

	// SEnd and verify a 200 request
	url := "/500"
	req := httptest.NewRequest("GET", url, nil)
	writer := httptest.NewRecorder()
	router.ServeHTTP(writer, req)
	assert.Equal(writer.Code, 500)
	assert.Equal(writer.Body.String(), "500!\n")

	// ensure properly traced
	assert.Nil(tracer.FlushTraces())
	traces := transport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)

	s := spans[0]
	assert.Equal(s.Name, "mux.request")
	assert.Equal(s.Service, "my-service")
	assert.Equal(s.Resource, "GET "+url)
	assert.Equal(s.GetMeta("http.status_code"), "500")
	assert.Equal(s.GetMeta("http.method"), "GET")
	assert.Equal(s.GetMeta("http.url"), url)
	assert.Equal(s.Error, int32(1))
}

// test handlers

func handler200(t *testing.T) http.HandlerFunc {
	assert := assert.New(t)
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte("200!"))
		assert.Nil(err)
		span := tracer.SpanFromContextDefault(r.Context())
		assert.Equal(span.Service, "my-service")
		assert.Equal(span.Duration, int64(0))
	}
}

func handler500(t *testing.T) http.HandlerFunc {
	assert := assert.New(t)
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "500!", http.StatusInternalServerError)
		span := tracer.SpanFromContextDefault(r.Context())
		assert.Equal(span.Service, "my-service")
		assert.Equal(span.Duration, int64(0))
	}
}

func setup(t *testing.T) (*tracer.Tracer, *test.DummyTransport, *mux.Router) {
	tracer, transport := test.GetTestTracer()
	mt := NewMuxTracer("my-service", tracer)

	r := mux.NewRouter()

	h200 := handler200(t)
	h500 := handler500(t)

	// Ensure we can use HandleFunc and it returns a route
	mt.HandleFunc(r, "/200", h200).Methods("Get")
	// And we can allso handle a bare func
	r.HandleFunc("/500", mt.TraceHandleFunc(h500))

	// do a subrouter (one in each way)
	sub := r.PathPrefix("/sub").Subrouter()
	sub.HandleFunc("/child1", mt.TraceHandleFunc(h200))
	mt.HandleFunc(sub, "/child2", h200)

	return tracer, transport, r
}
