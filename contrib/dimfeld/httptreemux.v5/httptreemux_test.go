// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptreemux

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/dimfeld/httptreemux/v5"
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

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))

	s := spans[0]
	assert.Equal("http.request", s.OperationName())
	assert.Equal("my-service", s.Tag(ext.ServiceName))
	assert.Equal("GET "+url, s.Tag(ext.ResourceName))
	assert.Equal("200", s.Tag(ext.HTTPCode))
	assert.Equal("GET", s.Tag(ext.HTTPMethod))
	assert.Equal("http://example.com"+url, s.Tag(ext.HTTPURL))
	assert.Equal("testvalue", s.Tag("testkey"))
	assert.Equal(nil, s.Tag(ext.Error))
}

func TestHttpTracer404(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	// Send and verify a request without a handler
	url := "/unknown/path"
	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	router().ServeHTTP(w, r)
	assert.Equal(404, w.Code)
	assert.Equal("404 page not found\n", w.Body.String())

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))

	s := spans[0]
	assert.Equal("http.request", s.OperationName())
	assert.Equal("my-service", s.Tag(ext.ServiceName))
	assert.Equal("GET unknown", s.Tag(ext.ResourceName))
	assert.Equal("404", s.Tag(ext.HTTPCode))
	assert.Equal("GET", s.Tag(ext.HTTPMethod))
	assert.Equal("http://example.com"+url, s.Tag(ext.HTTPURL))
	assert.Equal("testvalue", s.Tag("testkey"))
	assert.Equal(nil, s.Tag(ext.Error))
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
	assert.Equal("http://example.com"+url, s.Tag(ext.HTTPURL))
	assert.Equal("testvalue", s.Tag("testkey"))
	assert.Equal("500: Internal Server Error", s.Tag(ext.Error).(error).Error())
}

func TestDefaultResourceNamer(t *testing.T) {
	tests := map[string]struct {
		method string
		path   string
		url    string
	}{
		"GET /things": {
			method: http.MethodGet,
			path:   "/things",
			url:    "/things"},
		"GET /things?a=b": {
			method: http.MethodGet,
			path:   "/things",
			url:    "/things?a=b"},
		"GET /thing/:a": {
			method: http.MethodGet,
			path:   "/thing/:a",
			url:    "/thing/123"},
		"PUT /thing/:a": {
			method: http.MethodPut,
			path:   "/thing/:a",
			url:    "/thing/123"},
		"GET /thing/:a/:b/:c": {
			method: http.MethodGet,
			path:   "/thing/:a/:b/:c",
			url:    "/thing/zyx/321/cba"},
		"GET /thing/:a/details": {
			method: http.MethodGet,
			path:   "/thing/:a/details",
			url:    "/thing/123/details"},
		"GET /thing/:a/:version/details": {
			method: http.MethodGet,
			path:   "/thing/:a/:version/details",
			url:    "/thing/123/2/details"},
		"GET /thing/:a/:b/details": {
			method: http.MethodGet,
			path:   "/thing/:a/:b/details",
			url:    "/thing/123/2/details"},
		"GET /thing/:a/:b/:c/details": {
			method: http.MethodGet,
			path:   "/thing/:a/:b/:c/details",
			url:    "/thing/123/2/1/details"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			mt := mocktracer.Start()
			defer mt.Stop()

			r := httptest.NewRequest(tc.method, tc.url, nil)
			w := httptest.NewRecorder()

			router := New()
			router.Handle(tc.method, tc.path, handler200)
			router.ServeHTTP(w, r)

			assert.Equal(http.StatusOK, w.Code)
			assert.Equal("OK\n", w.Body.String())

			spans := mt.FinishedSpans()
			assert.Equal(1, len(spans))

			s := spans[0]
			resourceName := tc.method + " " + tc.path
			assert.Equal(resourceName, s.Tag(ext.ResourceName))
			assert.Equal("200", s.Tag(ext.HTTPCode))
			assert.Equal(tc.method, s.Tag(ext.HTTPMethod))
			assert.Equal("http://example.com"+tc.url, s.Tag(ext.HTTPURL))
			assert.Equal(nil, s.Tag(ext.Error))
		})
	}
}

func TestResourceNamer(t *testing.T) {
	staticName := "static resource name"
	staticNamer := func(*httptreemux.TreeMux, http.ResponseWriter, *http.Request) string {
		return staticName
	}

	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := New(
		WithServiceName("my-service"),
		WithSpanOptions(tracer.Tag("testkey", "testvalue")),
		WithResourceNamer(staticNamer),
	)

	// Note that the router has no handlers since we expect a 404

	// Send and verify a request without a handler
	url := "/path/that/does/not/exist"
	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	assert.Equal(404, w.Code)
	assert.Equal("404 page not found\n", w.Body.String())

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))

	s := spans[0]
	assert.Equal("http.request", s.OperationName())
	assert.Equal("my-service", s.Tag(ext.ServiceName))
	assert.Equal(staticName, s.Tag(ext.ResourceName))
	assert.Equal("404", s.Tag(ext.HTTPCode))
	assert.Equal("GET", s.Tag(ext.HTTPMethod))
	assert.Equal("http://example.com"+url, s.Tag(ext.HTTPURL))
	assert.Equal("testvalue", s.Tag("testkey"))
	assert.Equal(nil, s.Tag(ext.Error))
}

func TestNamingSchema(t *testing.T) {
	genSpans := namingschematest.GenSpansFn(func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []RouterOption
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		mux := New(opts...)
		mux.GET("/200", handler200)
		r := httptest.NewRequest("GET", "/200", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)

		return mt.FinishedSpans()
	})
	namingschematest.NewHTTPServerTest(genSpans, "http.router")(t)
}

func router() http.Handler {
	router := New(
		WithServiceName("my-service"),
		WithSpanOptions(tracer.Tag("testkey", "testvalue")),
	)

	router.GET("/200", handler200)
	router.GET("/500", handler500)

	return router
}

func handler200(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
	w.Write([]byte("OK\n"))
}

func handler500(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
	http.Error(w, "500!", http.StatusInternalServerError)
}
