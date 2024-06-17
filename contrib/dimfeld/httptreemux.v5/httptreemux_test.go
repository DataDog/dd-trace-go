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
	assert.Equal("/200", s.Tag(ext.HTTPRoute))
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
	assert.NotContains(s.Tags(), ext.HTTPRoute)
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
	assert.Equal("/500", s.Tag(ext.HTTPRoute))
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
			assert.Equal(tc.path, s.Tag(ext.HTTPRoute))
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

func TestTrailingSlashRoutesWithBehaviorRedirect301(t *testing.T) {
	t.Run("GET unknown", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := New(
			WithServiceName("my-service"),
			WithSpanOptions(tracer.Tag("testkey", "testvalue")),
		)
		router.RedirectBehavior = httptreemux.Redirect301 // default

		// Note that the router has no handlers since we expect a 404

		url := "/api/paramvalue"
		r := httptest.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)

		assert.Equal(http.StatusNotFound, w.Code)
		assert.Contains(w.Body.String(), "404 page not found")

		spans := mt.FinishedSpans()
		assert.Equal(1, len(spans))

		s := spans[0]
		assert.Equal("http.request", s.OperationName())
		assert.Equal("my-service", s.Tag(ext.ServiceName))
		assert.Equal("GET unknown", s.Tag(ext.ResourceName))
		assert.Equal("404", s.Tag(ext.HTTPCode))
		assert.Equal("GET", s.Tag(ext.HTTPMethod))
		assert.Equal("http://example.com/api/paramvalue", s.Tag(ext.HTTPURL))
		assert.Equal("testvalue", s.Tag("testkey"))
		assert.Nil(s.Tag(ext.Error))
		assert.NotContains(s.Tags(), ext.HTTPRoute)
	})

	t.Run("GET /api/:parameter", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := New(
			WithServiceName("my-service"),
			WithSpanOptions(tracer.Tag("testkey", "testvalue")),
		)
		router.GET("/api/:parameter", handler200)         // without trailing slash
		router.RedirectBehavior = httptreemux.Redirect301 // default

		url := "/api/paramvalue/" // with trailing slash
		r := httptest.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)

		assert.Equal(http.StatusMovedPermanently, w.Code)
		assert.Contains(w.Body.String(), "Moved Permanently")

		spans := mt.FinishedSpans()
		assert.Equal(1, len(spans))

		s := spans[0]
		assert.Equal("http.request", s.OperationName())
		assert.Equal("my-service", s.Tag(ext.ServiceName))
		assert.Equal("GET /api/:parameter/", s.Tag(ext.ResourceName))
		assert.Equal("301", s.Tag(ext.HTTPCode))
		assert.Equal("GET", s.Tag(ext.HTTPMethod))
		assert.Equal("http://example.com/api/paramvalue/", s.Tag(ext.HTTPURL))
		assert.Equal("testvalue", s.Tag("testkey"))
		assert.Nil(s.Tag(ext.Error))
		assert.Contains(s.Tags(), ext.HTTPRoute)
	})

	t.Run("GET /api/:parameter/", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := New(
			WithServiceName("my-service"),
			WithSpanOptions(tracer.Tag("testkey", "testvalue")),
		)
		router.GET("/api/:parameter/", handler200)        // with trailing slash
		router.RedirectBehavior = httptreemux.Redirect301 // default

		url := "/api/paramvalue" // without trailing slash
		r := httptest.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)

		assert.Equal(http.StatusMovedPermanently, w.Code)
		assert.Contains(w.Body.String(), "Moved Permanently")

		spans := mt.FinishedSpans()
		assert.Equal(1, len(spans))

		s := spans[0]
		assert.Equal("http.request", s.OperationName())
		assert.Equal("my-service", s.Tag(ext.ServiceName))
		assert.Equal("GET /api/:parameter", s.Tag(ext.ResourceName))
		assert.Equal("301", s.Tag(ext.HTTPCode))
		assert.Equal("GET", s.Tag(ext.HTTPMethod))
		assert.Equal("http://example.com/api/paramvalue", s.Tag(ext.HTTPURL))
		assert.Equal("testvalue", s.Tag("testkey"))
		assert.Nil(s.Tag(ext.Error))
		assert.Contains(s.Tags(), ext.HTTPRoute)
	})
}

func TestTrailingSlashRoutesWithBehaviorRedirect307(t *testing.T) {
	t.Run("GET unknown", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := New(
			WithServiceName("my-service"),
			WithSpanOptions(tracer.Tag("testkey", "testvalue")),
		)
		router.RedirectBehavior = httptreemux.Redirect307

		// Note that the router has no handlers since we expect a 404

		url := "/api/paramvalue"
		r := httptest.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)

		assert.Equal(http.StatusNotFound, w.Code)
		assert.Contains(w.Body.String(), "404 page not found")

		spans := mt.FinishedSpans()
		assert.Equal(1, len(spans))

		s := spans[0]
		assert.Equal("http.request", s.OperationName())
		assert.Equal("my-service", s.Tag(ext.ServiceName))
		assert.Equal("GET unknown", s.Tag(ext.ResourceName))
		assert.Equal("404", s.Tag(ext.HTTPCode))
		assert.Equal("GET", s.Tag(ext.HTTPMethod))
		assert.Equal("http://example.com/api/paramvalue", s.Tag(ext.HTTPURL))
		assert.Equal("testvalue", s.Tag("testkey"))
		assert.Nil(s.Tag(ext.Error))
		assert.NotContains(s.Tags(), ext.HTTPRoute)
	})

	t.Run("GET /api/:parameter", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := New(
			WithServiceName("my-service"),
			WithSpanOptions(tracer.Tag("testkey", "testvalue")),
		)
		router.GET("/api/:parameter", handler200) // without trailing slash
		router.RedirectBehavior = httptreemux.Redirect307

		url := "/api/paramvalue/" // with trailing slash
		r := httptest.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)

		assert.Equal(http.StatusTemporaryRedirect, w.Code)
		assert.Contains(w.Body.String(), "Temporary Redirect")

		spans := mt.FinishedSpans()
		assert.Equal(1, len(spans))

		s := spans[0]
		assert.Equal("http.request", s.OperationName())
		assert.Equal("my-service", s.Tag(ext.ServiceName))
		assert.Equal("GET /api/:parameter/", s.Tag(ext.ResourceName))
		assert.Equal("307", s.Tag(ext.HTTPCode))
		assert.Equal("GET", s.Tag(ext.HTTPMethod))
		assert.Equal("http://example.com/api/paramvalue/", s.Tag(ext.HTTPURL))
		assert.Equal("testvalue", s.Tag("testkey"))
		assert.Nil(s.Tag(ext.Error))
		assert.Contains(s.Tags(), ext.HTTPRoute)
	})

	t.Run("GET /api/:parameter/", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := New(
			WithServiceName("my-service"),
			WithSpanOptions(tracer.Tag("testkey", "testvalue")),
		)
		router.GET("/api/:parameter/", handler200) // with trailing slash
		router.RedirectBehavior = httptreemux.Redirect307

		url := "/api/paramvalue" // without trailing slash
		r := httptest.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)

		assert.Equal(http.StatusTemporaryRedirect, w.Code)
		assert.Contains(w.Body.String(), "Temporary Redirect")

		spans := mt.FinishedSpans()
		assert.Equal(1, len(spans))

		s := spans[0]
		assert.Equal("http.request", s.OperationName())
		assert.Equal("my-service", s.Tag(ext.ServiceName))
		assert.Equal("GET /api/:parameter", s.Tag(ext.ResourceName))
		assert.Equal("307", s.Tag(ext.HTTPCode))
		assert.Equal("GET", s.Tag(ext.HTTPMethod))
		assert.Equal("http://example.com/api/paramvalue", s.Tag(ext.HTTPURL))
		assert.Equal("testvalue", s.Tag("testkey"))
		assert.Nil(s.Tag(ext.Error))
		assert.Contains(s.Tags(), ext.HTTPRoute)
	})
}

func TestTrailingSlashRoutesWithBehaviorRedirect308(t *testing.T) {
	t.Run("GET unknown", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := New(
			WithServiceName("my-service"),
			WithSpanOptions(tracer.Tag("testkey", "testvalue")),
		)
		router.RedirectBehavior = httptreemux.Redirect308

		// Note that the router has no handlers since we expect a 404

		url := "/api/paramvalue"
		r := httptest.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)

		assert.Equal(http.StatusNotFound, w.Code)
		assert.Contains(w.Body.String(), "404 page not found")

		spans := mt.FinishedSpans()
		assert.Equal(1, len(spans))

		s := spans[0]
		assert.Equal("http.request", s.OperationName())
		assert.Equal("my-service", s.Tag(ext.ServiceName))
		assert.Equal("GET unknown", s.Tag(ext.ResourceName))
		assert.Equal("404", s.Tag(ext.HTTPCode))
		assert.Equal("GET", s.Tag(ext.HTTPMethod))
		assert.Equal("http://example.com/api/paramvalue", s.Tag(ext.HTTPURL))
		assert.Equal("testvalue", s.Tag("testkey"))
		assert.Nil(s.Tag(ext.Error))
		assert.NotContains(s.Tags(), ext.HTTPRoute)
	})

	t.Run("GET /api/:parameter", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := New(
			WithServiceName("my-service"),
			WithSpanOptions(tracer.Tag("testkey", "testvalue")),
		)
		router.GET("/api/:parameter", handler200) // without trailing slash
		router.RedirectBehavior = httptreemux.Redirect308

		url := "/api/paramvalue/" // with trailing slash
		r := httptest.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)

		assert.Equal(http.StatusPermanentRedirect, w.Code)
		assert.Contains(w.Body.String(), "Permanent Redirect")

		spans := mt.FinishedSpans()
		assert.Equal(1, len(spans))

		s := spans[0]
		assert.Equal("http.request", s.OperationName())
		assert.Equal("my-service", s.Tag(ext.ServiceName))
		assert.Equal("GET /api/:parameter/", s.Tag(ext.ResourceName))
		assert.Equal("308", s.Tag(ext.HTTPCode))
		assert.Equal("GET", s.Tag(ext.HTTPMethod))
		assert.Equal("http://example.com/api/paramvalue/", s.Tag(ext.HTTPURL))
		assert.Equal("testvalue", s.Tag("testkey"))
		assert.Nil(s.Tag(ext.Error))
		assert.Contains(s.Tags(), ext.HTTPRoute)
	})

	t.Run("GET /api/:parameter/", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := New(
			WithServiceName("my-service"),
			WithSpanOptions(tracer.Tag("testkey", "testvalue")),
		)
		router.GET("/api/:parameter/", handler200) // with trailing slash
		router.RedirectBehavior = httptreemux.Redirect308

		url := "/api/paramvalue" // without trailing slash
		r := httptest.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)

		assert.Equal(http.StatusPermanentRedirect, w.Code)
		assert.Contains(w.Body.String(), "Permanent Redirect")

		spans := mt.FinishedSpans()
		assert.Equal(1, len(spans))

		s := spans[0]
		assert.Equal("http.request", s.OperationName())
		assert.Equal("my-service", s.Tag(ext.ServiceName))
		assert.Equal("GET /api/:parameter", s.Tag(ext.ResourceName))
		assert.Equal("308", s.Tag(ext.HTTPCode))
		assert.Equal("GET", s.Tag(ext.HTTPMethod))
		assert.Equal("http://example.com/api/paramvalue", s.Tag(ext.HTTPURL))
		assert.Equal("testvalue", s.Tag("testkey"))
		assert.Nil(s.Tag(ext.Error))
		assert.Contains(s.Tags(), ext.HTTPRoute)
	})
}

func TestTrailingSlashRoutesWithBehaviorUseHandler(t *testing.T) {
	t.Run("GET unknown", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := New(
			WithServiceName("my-service"),
			WithSpanOptions(tracer.Tag("testkey", "testvalue")),
		)
		router.RedirectBehavior = httptreemux.UseHandler

		// Note that the router has no handlers since we expect a 404

		url := "/api/paramvalue"
		r := httptest.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)

		assert.Equal(http.StatusNotFound, w.Code)
		assert.Contains(w.Body.String(), "404 page not found")

		spans := mt.FinishedSpans()
		assert.Equal(1, len(spans))

		s := spans[0]
		assert.Equal("http.request", s.OperationName())
		assert.Equal("my-service", s.Tag(ext.ServiceName))
		assert.Equal("GET unknown", s.Tag(ext.ResourceName))
		assert.Equal("404", s.Tag(ext.HTTPCode))
		assert.Equal("GET", s.Tag(ext.HTTPMethod))
		assert.Equal("http://example.com/api/paramvalue", s.Tag(ext.HTTPURL))
		assert.Equal("testvalue", s.Tag("testkey"))
		assert.Nil(s.Tag(ext.Error))
		assert.NotContains(s.Tags(), ext.HTTPRoute)
	})

	t.Run("GET /api/:parameter", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := New(
			WithServiceName("my-service"),
			WithSpanOptions(tracer.Tag("testkey", "testvalue")),
		)
		router.GET("/api/:parameter", handler200) // without trailing slash
		router.RedirectBehavior = httptreemux.UseHandler

		url := "/api/paramvalue/" // with trailing slash
		r := httptest.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)

		assert.Equal(http.StatusOK, w.Code)
		assert.Contains(w.Body.String(), "OK\n")

		spans := mt.FinishedSpans()
		assert.Equal(1, len(spans))

		s := spans[0]
		assert.Equal("http.request", s.OperationName())
		assert.Equal("my-service", s.Tag(ext.ServiceName))
		assert.Equal("GET /api/:parameter/", s.Tag(ext.ResourceName))
		assert.Equal("200", s.Tag(ext.HTTPCode))
		assert.Equal("GET", s.Tag(ext.HTTPMethod))
		assert.Equal("http://example.com/api/paramvalue/", s.Tag(ext.HTTPURL))
		assert.Equal("testvalue", s.Tag("testkey"))
		assert.Nil(s.Tag(ext.Error))
		assert.Contains(s.Tags(), ext.HTTPRoute)
	})

	t.Run("GET /api/:parameter/", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := New(
			WithServiceName("my-service"),
			WithSpanOptions(tracer.Tag("testkey", "testvalue")),
		)
		router.GET("/api/:parameter/", handler200) // with trailing slash
		router.RedirectBehavior = httptreemux.UseHandler

		url := "/api/paramvalue" // without trailing slash
		r := httptest.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)

		assert.Equal(http.StatusOK, w.Code)
		assert.Contains(w.Body.String(), "OK\n")

		spans := mt.FinishedSpans()
		assert.Equal(1, len(spans))

		s := spans[0]
		assert.Equal("http.request", s.OperationName())
		assert.Equal("my-service", s.Tag(ext.ServiceName))
		assert.Equal("GET /api/:parameter", s.Tag(ext.ResourceName))
		assert.Equal("200", s.Tag(ext.HTTPCode))
		assert.Equal("GET", s.Tag(ext.HTTPMethod))
		assert.Equal("http://example.com/api/paramvalue", s.Tag(ext.HTTPURL))
		assert.Equal("testvalue", s.Tag("testkey"))
		assert.Nil(s.Tag(ext.Error))
		assert.Contains(s.Tags(), ext.HTTPRoute)
	})
}

func TestIsSupportedRedirectStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   bool
	}{
		{
			name:   "Test with status 301",
			status: 301,
			want:   true,
		},
		{
			name:   "Test with status 302",
			status: 302,
			want:   false,
		},
		{
			name:   "Test with status 303",
			status: 303,
			want:   false,
		},
		{
			name:   "Test with status 307",
			status: 307,
			want:   true,
		},
		{
			name:   "Test with status 308",
			status: 308,
			want:   true,
		},
		{
			name:   "Test with status 400",
			status: 400,
			want:   false,
		},
		{
			name:   "Test with status 0",
			status: 0,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSupportedRedirectStatus(tt.status); got != tt.want {
				t.Errorf("isSupportedRedirectStatus() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRouterRedirectEnabled(t *testing.T) {
	tests := []struct {
		name              string
		cleanPath         bool
		trailingSlash     bool
		redirectBehaviour httptreemux.RedirectBehavior

		want bool
	}{
		// httptreemux.Redirect301
		{
			name:              "Test Redirect301 with clean path and trailing slash",
			cleanPath:         true,
			trailingSlash:     true,
			redirectBehaviour: httptreemux.Redirect301,
			want:              true,
		},
		{
			name:              "Test Redirect301 with clean path and no trailing slash",
			cleanPath:         true,
			trailingSlash:     false,
			redirectBehaviour: httptreemux.Redirect301,
			want:              true,
		},
		{
			name:              "Test Redirect301 with no clean path and trailing slash",
			cleanPath:         false,
			trailingSlash:     true,
			redirectBehaviour: httptreemux.Redirect301,
			want:              true,
		},
		{
			name:              "Test Redirect301 with no clean path and no trailing slash",
			cleanPath:         false,
			trailingSlash:     false,
			redirectBehaviour: httptreemux.Redirect301,
			want:              false,
		},
		// httptreemux.Redirect307
		{
			name:              "Test Redirect307 with clean path and trailing slash",
			cleanPath:         true,
			trailingSlash:     true,
			redirectBehaviour: httptreemux.Redirect307,
			want:              true,
		},
		{
			name:              "Test Redirect307 with clean path and no trailing slash",
			cleanPath:         true,
			trailingSlash:     false,
			redirectBehaviour: httptreemux.Redirect307,
			want:              true,
		},
		{
			name:              "Test Redirect307 with no clean path and trailing slash",
			cleanPath:         false,
			trailingSlash:     true,
			redirectBehaviour: httptreemux.Redirect307,
			want:              true,
		},
		{
			name:              "Test Redirect307 with no clean path and no trailing slash",
			cleanPath:         false,
			trailingSlash:     false,
			redirectBehaviour: httptreemux.Redirect307,
			want:              false,
		},
		// httptreemux.Redirect308
		{
			name:              "Test Redirect308 with clean path and trailing slash",
			cleanPath:         true,
			trailingSlash:     true,
			redirectBehaviour: httptreemux.Redirect308,
			want:              true,
		},
		{
			name:              "Test Redirect308 with clean path and no trailing slash",
			cleanPath:         true,
			trailingSlash:     false,
			redirectBehaviour: httptreemux.Redirect308,
			want:              true,
		},
		{
			name:              "Test Redirect308 with no clean path and trailing slash",
			cleanPath:         false,
			trailingSlash:     true,
			redirectBehaviour: httptreemux.Redirect308,
			want:              true,
		},
		{
			name:              "Test Redirect308 with no clean path and no trailing slash",
			cleanPath:         false,
			trailingSlash:     false,
			redirectBehaviour: httptreemux.Redirect308,
			want:              false,
		},
		// httptreemux.UseHandler
		{
			name:              "Test UseHandler with clean path and trailing slash",
			cleanPath:         true,
			trailingSlash:     true,
			redirectBehaviour: httptreemux.UseHandler,
			want:              false,
		},
		{
			name:              "Test UseHandler with clean path and no trailing slash",
			cleanPath:         true,
			trailingSlash:     false,
			redirectBehaviour: httptreemux.UseHandler,
			want:              false,
		},
		{
			name:              "Test UseHandler with no clean path and trailing slash",
			cleanPath:         false,
			trailingSlash:     true,
			redirectBehaviour: httptreemux.UseHandler,
			want:              false,
		},
		{
			name:              "Test UseHandler with no clean path and no trailing slash",
			cleanPath:         false,
			trailingSlash:     false,
			redirectBehaviour: httptreemux.UseHandler,
			want:              false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := httptreemux.New()
			router.RedirectCleanPath = tt.cleanPath
			router.RedirectTrailingSlash = tt.trailingSlash
			router.RedirectBehavior = tt.redirectBehaviour

			if got := routerRedirectEnabled(router); got != tt.want {
				t.Errorf("routerRedirectEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func router() http.Handler {
	router := New(
		WithServiceName("my-service"),
		WithSpanOptions(tracer.Tag("testkey", "testvalue")),
	)

	router.GET("/200", handler200)
	router.GET("/500", handler500)

	router.GET("/api/:parameter", handler200)
	router.GET("/api/:param1/:param2/:param3", handler200)

	return router
}

func handler200(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
	w.Write([]byte("OK\n"))
}

func handler500(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
	http.Error(w, "500!", http.StatusInternalServerError)
}
