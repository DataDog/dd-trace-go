// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptreemux

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/dimfeld/httptreemux/v5"
	"github.com/stretchr/testify/assert"
)

func TestContextMux200(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := NewWithContext(
		WithServiceName("my-service"),
		WithSpanOptions(tracer.Tag("testkey", "testvalue")),
	)

	url := "/200"
	router.GET(url, handlerWithContext200(t, url, nil))

	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)

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

func TestContextMux404(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	// Send and verify a request without a handler
	url := "/unknown/path"
	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	NewWithContext(
		WithServiceName("my-service"),
		WithSpanOptions(tracer.Tag("testkey", "testvalue")),
	).ServeHTTP(w, r)
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

func TestContextMux500(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := NewWithContext(
		WithServiceName("my-service"),
		WithSpanOptions(tracer.Tag("testkey", "testvalue")),
	)

	url := "/500"
	router.GET(url, handlerWithContext500(t, url, nil))

	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
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

func TestContextMuxDefaultResourceNamer(t *testing.T) {
	tests := map[string]struct {
		method string
		route  string
		url    string
		params map[string]string
	}{
		"GET /things": {
			method: http.MethodGet,
			route:  "/things",
			url:    "/things",
		},
		"GET /things?a=b": {
			method: http.MethodGet,
			route:  "/things",
			url:    "/things?a=b",
		},
		"GET /thing/:a": {
			method: http.MethodGet,
			route:  "/thing/:a",
			url:    "/thing/123",
			params: map[string]string{"a": "123"},
		},
		"PUT /thing/:a": {
			method: http.MethodPut,
			route:  "/thing/:a",
			url:    "/thing/123",
			params: map[string]string{"a": "123"},
		},
		"GET /thing/:a/:b/:c": {
			method: http.MethodGet,
			route:  "/thing/:a/:b/:c",
			url:    "/thing/zyx/321/cba",
			params: map[string]string{"a": "zyx", "b": "321", "c": "cba"},
		},
		"GET /thing/:a/details": {
			method: http.MethodGet,
			route:  "/thing/:a/details",
			url:    "/thing/123/details",
			params: map[string]string{"a": "123"},
		},
		"GET /thing/:a/:version/details": {
			method: http.MethodGet,
			route:  "/thing/:a/:version/details",
			url:    "/thing/123/2/details",
			params: map[string]string{"a": "123", "version": "2"},
		},
		"GET /thing/:a/:b/details": {
			method: http.MethodGet,
			route:  "/thing/:a/:b/details",
			url:    "/thing/123/2/details",
			params: map[string]string{"a": "123", "b": "2"},
		},
		"GET /thing/:a/:b/:c/details": {
			method: http.MethodGet,
			route:  "/thing/:a/:b/:c/details",
			url:    "/thing/123/2/1/details",
			params: map[string]string{"a": "123", "b": "2", "c": "1"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			mt := mocktracer.Start()
			defer mt.Stop()

			r := httptest.NewRequest(tc.method, tc.url, nil)
			w := httptest.NewRecorder()

			router := NewWithContext()
			router.Handle(tc.method, tc.route, handlerWithContext200(t, tc.route, tc.params))
			router.ServeHTTP(w, r)

			assert.Equal(http.StatusOK, w.Code)
			assert.Equal("OK\n", w.Body.String())

			spans := mt.FinishedSpans()
			assert.Equal(1, len(spans))

			s := spans[0]
			resourceName := tc.method + " " + tc.route
			assert.Equal(resourceName, s.Tag(ext.ResourceName))
			assert.Equal("200", s.Tag(ext.HTTPCode))
			assert.Equal(tc.method, s.Tag(ext.HTTPMethod))
			assert.Equal("http://example.com"+tc.url, s.Tag(ext.HTTPURL))
			assert.Equal(nil, s.Tag(ext.Error))
		})
	}
}

func TestContextMuxResourceNamer(t *testing.T) {
	staticName := "static resource name"
	staticNamer := func(*httptreemux.TreeMux, http.ResponseWriter, *http.Request) string {
		return staticName
	}

	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := NewWithContext(
		WithServiceName("my-service"),
		WithSpanOptions(tracer.Tag("testkey", "testvalue")),
		WithResourceNamer(staticNamer),
	)

	url := "/200"
	router.GET(url, handlerWithContext200(t, url, nil))

	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)

	assert.Equal(200, w.Code)
	assert.Equal("OK\n", w.Body.String())

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))

	s := spans[0]
	assert.Equal("http.request", s.OperationName())
	assert.Equal("my-service", s.Tag(ext.ServiceName))
	assert.Equal(staticName, s.Tag(ext.ResourceName))
	assert.Equal("200", s.Tag(ext.HTTPCode))
	assert.Equal("GET", s.Tag(ext.HTTPMethod))
	assert.Equal("http://example.com"+url, s.Tag(ext.HTTPURL))
	assert.Equal("testvalue", s.Tag("testkey"))
	assert.Equal(nil, s.Tag(ext.Error))
}

func handlerWithContext200(t *testing.T, route string, params map[string]string) http.HandlerFunc {
	assert := assert.New(t)
	return func(w http.ResponseWriter, r *http.Request) {
		crd := httptreemux.ContextData(r.Context())
		assert.Equal(route, crd.Route(), "unexpected route")
		assert.Len(params, len(crd.Params()), "unexpected number of params")
		for k, v := range params {
			if assert.Contains(crd.Params(), k, "expected param not found") {
				assert.Equal(v, crd.Params()[k], "unexpected param value")
			}
		}

		w.Write([]byte("OK\n"))
	}
}

func handlerWithContext500(t *testing.T, route string, params map[string]string) http.HandlerFunc {
	assert := assert.New(t)
	return func(w http.ResponseWriter, r *http.Request) {
		crd := httptreemux.ContextData(r.Context())
		assert.Equal(route, crd.Route(), "unexpected route")
		assert.Len(params, len(crd.Params()), "unexpected number of params")
		for k, v := range params {
			if assert.Contains(crd.Params(), k, "expected param not found") {
				assert.Equal(v, crd.Params()[k], "unexpected param value")
			}
		}

		http.Error(w, "500!", http.StatusInternalServerError)
	}
}
