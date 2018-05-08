package mux

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/stretchr/testify/assert"
)

func TestHttpTracer200(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	// Send and verify a 200 request
	url := "/200"
	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	router().ServeHTTP(w, r)
	assert.Equal(200, w.Code)
	assert.Equal("OK\n", w.Body.String())

	// Ensure the request is properly traced
	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))

	s := spans[0]
	assert.Equal("http.request", s.OperationName())
	assert.Equal("my-service", s.Tag(ext.ServiceName))
	assert.Equal("GET "+url, s.Tag(ext.ResourceName))
	assert.Equal("200", s.Tag(ext.HTTPCode))
	assert.Equal("GET", s.Tag(ext.HTTPMethod))
	assert.Equal(url, s.Tag(ext.HTTPURL))
	assert.Equal(nil, s.Tag(ext.Error))
}

func TestHttpTracer404(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	// Send and verify a 500 request
	url := "/not_a_real_route"
	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	router().ServeHTTP(w, r)
	assert.Equal(404, w.Code)
	assert.Equal("404!\n", w.Body.String())

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))

	s := spans[0]
	assert.Equal("http.request", s.OperationName())
	assert.Equal("my-service", s.Tag(ext.ServiceName))
	assert.Equal("GET unknown", s.Tag(ext.ResourceName))
	assert.Equal("404", s.Tag(ext.HTTPCode))
	assert.Equal("GET", s.Tag(ext.HTTPMethod))
	assert.Equal(url, s.Tag(ext.HTTPURL))
}

func TestHttpTracer405(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	// Send and verify a 500 request
	url := "/method"
	r := httptest.NewRequest("POST", url, nil)
	w := httptest.NewRecorder()
	router().ServeHTTP(w, r)
	assert.Equal(405, w.Code)
	assert.Equal("405!\n", w.Body.String())

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))

	s := spans[0]
	assert.Equal("http.request", s.OperationName())
	assert.Equal("my-service", s.Tag(ext.ServiceName))
	assert.Equal("POST unknown", s.Tag(ext.ResourceName))
	assert.Equal("405", s.Tag(ext.HTTPCode))
	assert.Equal("POST", s.Tag(ext.HTTPMethod))
	assert.Equal(url, s.Tag(ext.HTTPURL))
}

func TestHttpTracer500(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	// Send and verify a 500 request
	url := "/500"
	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	router().ServeHTTP(w, r)
	assert.Equal(500, w.Code)
	assert.Equal("500!\n", w.Body.String())

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))

	s := spans[0]
	assert.Equal("http.request", s.OperationName())
	assert.Equal("my-service", s.Tag(ext.ServiceName))
	assert.Equal("GET "+url, s.Tag(ext.ResourceName))
	assert.Equal("500", s.Tag(ext.HTTPCode))
	assert.Equal("GET", s.Tag(ext.HTTPMethod))
	assert.Equal(url, s.Tag(ext.HTTPURL))
	assert.Equal("500: Internal Server Error", s.Tag(ext.Error).(error).Error())
}

func router() http.Handler {
	mux := NewRouter(WithServiceName("my-service"))
	mux.HandleFunc("/200", handler200)
	mux.HandleFunc("/500", handler500)
	mux.HandleFunc("/method", handler200).Methods("GET")
	mux.NotFoundHandler = get404Handler()
	mux.MethodNotAllowedHandler = get405Handler()
	return mux
}

func handler200(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("OK\n"))
}

func get404Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "404!", http.StatusNotFound)
	})
}

func get405Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "405!", http.StatusMethodNotAllowed)
	})
}

func handler500(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "500!", http.StatusInternalServerError)
}
