package muxtrace

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
)

func TestMuxTracer200(t *testing.T) {
	assert := assert.New(t)

	// setup
	tracer, transport, router := setup(t)

	// SEnd and verify a 200 request
	req := httptest.NewRequest("GET", "/200", nil)
	writer := httptest.NewRecorder()
	router.ServeHTTP(writer, req)
	assert.Equal(writer.Code, 200)
	assert.Equal(writer.Body.String(), "200!")

	// ensure properly traced
	assert.Nil(tracer.Flush())
	spans := transport.spans
	assert.Len(spans, 1)

	s := spans[0]
	assert.Equal(s.Name, "mux.request")
	assert.Equal(s.Service, "my-service")
	assert.Equal(s.Resource, "GET /200")
	assert.Equal(s.GetMeta("http.status_code"), "200")
	assert.Equal(s.GetMeta("http.method"), "GET")
}

func TestMuxTracer500(t *testing.T) {
	assert := assert.New(t)

	// setup
	tracer, transport, router := setup(t)

	// SEnd and verify a 200 request
	req := httptest.NewRequest("GET", "/500", nil)
	writer := httptest.NewRecorder()
	router.ServeHTTP(writer, req)
	assert.Equal(writer.Code, 500)
	assert.Equal(writer.Body.String(), "500!\n")

	// ensure properly traced
	assert.Nil(tracer.Flush())
	spans := transport.spans
	assert.Len(spans, 1)

	s := spans[0]
	assert.Equal(s.Name, "mux.request")
	assert.Equal(s.Service, "my-service")
	assert.Equal(s.Resource, "GET /500")
	assert.Equal(s.GetMeta("http.status_code"), "500")
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

func setup(t *testing.T) (*tracer.Tracer, *dummyTransport, *mux.Router) {
	tracer, transport := getTestTracer()
	mt := NewMuxTracer("my-service", tracer)
	r := mux.NewRouter()

	// Ensure we can use HandleFunc and it returns a route
	mt.HandleFunc(r, "/200", handler200(t)).Methods("Get")

	// And we can allso handle a bare func
	r.HandleFunc("/500", mt.TraceHandleFunc(handler500(t)))

	return tracer, transport, r
}

// getTestTracer returns a tracer which will buffer but not submit spans.
func getTestTracer() (*tracer.Tracer, *dummyTransport) {
	trans := &dummyTransport{}
	trac := tracer.NewTracerTransport(trans)
	return trac, trans
}

// dummyTransport is a transport that just buffers spans.
type dummyTransport struct {
	spans []*tracer.Span
}

func (d *dummyTransport) Send(s []*tracer.Span) error {
	d.spans = append(d.spans, s...)
	return nil
}
