package chi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DataDog/dd-trace-go/tracer/tracertest"
	"github.com/go-chi/chi"
	"github.com/stretchr/testify/assert"
)

func TestTracingMiddleWare(t *testing.T) {
	assert := assert.New(t)

	tracer, transport := tracertest.GetTestTracer()

	// Router setup
	r := chi.NewRouter()

	middleware := Middleware("my-service", "/test/{id}", tracer)
	r.With(middleware).Get("/test/{id}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ᕕ( ᐛ )ᕗ")
	})

	url := "/test/24601"
	req := httptest.NewRequest("GET", url, nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	assert.Equal(200, res.Code)
	assert.Equal("ᕕ( ᐛ )ᕗ", res.Body.String())

	tracer.ForceFlush()
	traces := transport.Traces()
	assert.Equal(1, len(traces))
	spans := traces[0]
	assert.Equal(1, len(spans))

	s := spans[0]
	assert.Equal("http.request", s.Name)
	assert.Equal("my-service", s.Service)
	assert.Equal("/test/{id}", s.Resource)
	assert.Equal("200", s.GetMeta("http.status_code"))
	assert.Equal("GET", s.GetMeta("http.method"))
	assert.Equal(url, s.GetMeta("http.url"))
	assert.Equal(int32(0), s.Error)
}

func TestTracingMiddleWareError(t *testing.T) {
	assert := assert.New(t)

	tracer, transport := tracertest.GetTestTracer()

	// Router setup
	r := chi.NewRouter()
	middleware := Middleware("my-service", "/test/{id}", tracer)
	r.With(middleware).Get("/test/{id}", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "¯\\_(ツ)_/¯", http.StatusInternalServerError)
	})

	url := "/test/24601"
	req := httptest.NewRequest("GET", url, nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	assert.Equal(500, res.Code)
	assert.Equal("¯\\_(ツ)_/¯\n", res.Body.String())

	tracer.ForceFlush()
	traces := transport.Traces()
	assert.Equal(1, len(traces))
	spans := traces[0]
	assert.Equal(1, len(spans))

	s := spans[0]
	assert.Equal("http.request", s.Name)
	assert.Equal("my-service", s.Service)
	assert.Equal("/test/{id}", s.Resource)
	assert.Equal("500", s.GetMeta("http.status_code"))
	assert.Equal("GET", s.GetMeta("http.method"))
	assert.Equal(url, s.GetMeta("http.url"))
	assert.Equal(int32(1), s.Error)
}
