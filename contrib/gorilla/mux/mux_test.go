// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mux

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
)

func TestHttpTracer(t *testing.T) {
	for _, ht := range []struct {
		name         string
		code         int
		method       string
		url          string
		wantResource string
		wantErr      string
		wantRoute    string
	}{
		{
			name:         "200",
			code:         http.StatusOK,
			method:       "GET",
			url:          "/200",
			wantResource: "GET /200",
			wantRoute:    "/200",
		},
		{
			name:         "users/{id}",
			code:         http.StatusOK,
			method:       "GET",
			url:          "/users/123",
			wantResource: "GET /users/{id}",
			wantRoute:    "/users/{id}",
		},
		{
			name:         "404",
			code:         http.StatusNotFound,
			method:       "GET",
			url:          "/not_a_real_route",
			wantResource: "GET unknown",
			wantRoute:    "",
		},
		{
			name:         "405",
			code:         http.StatusMethodNotAllowed,
			method:       "POST",
			url:          "/405",
			wantResource: "POST unknown",
			wantRoute:    "",
		},
		{
			name:         "500",
			code:         http.StatusInternalServerError,
			method:       "GET",
			url:          "/500",
			wantResource: "GET /500",
			wantErr:      "500: Internal Server Error",
			wantRoute:    "/500",
		},
	} {
		t.Run(ht.name, func(t *testing.T) {
			assert := assert.New(t)
			mt := mocktracer.Start()
			defer mt.Stop()
			codeStr := strconv.Itoa(ht.code)

			// Send and verify a request
			r := httptest.NewRequest(ht.method, ht.url, nil)
			w := httptest.NewRecorder()
			router().ServeHTTP(w, r)
			assert.Equal(ht.code, w.Code)
			assert.Equal(codeStr+"!\n", w.Body.String())

			spans := mt.FinishedSpans()
			assert.Equal(1, len(spans))

			s := spans[0]
			assert.Equal("http.request", s.OperationName())
			assert.Equal("my-service", s.Tag(ext.ServiceName))
			assert.Equal(codeStr, s.Tag(ext.HTTPCode))
			assert.Equal(ht.method, s.Tag(ext.HTTPMethod))
			assert.Equal("http://example.com"+ht.url, s.Tag(ext.HTTPURL))
			assert.Equal(ht.wantResource, s.Tag(ext.ResourceName))
			assert.Equal(ext.SpanKindServer, s.Tag(ext.SpanKind))
			assert.Equal("gorilla/mux", s.Tag(ext.Component))
			assert.Equal("gorilla/mux", s.Integration())
			if ht.wantRoute != "" {
				assert.Equal(ht.wantRoute, s.Tag(ext.HTTPRoute))
			} else {
				assert.NotContains(s.Tags(), ext.HTTPRoute)
			}

			if ht.wantErr != "" {
				assert.Equal(ht.wantErr, s.Tag(ext.Error).(error).Error())
			}
		})
	}
}

func TestAssignedSubRouter(t *testing.T) {
	for _, ht := range []struct {
		name         string
		code         int
		method       string
		url          string
		wantResource string
		wantErr      string
		wantRoute    string
	}{
		{
			name:         "200",
			code:         http.StatusOK,
			method:       "GET",
			url:          "/200",
			wantResource: "GET /200",
			wantRoute:    "/200",
		},
		{
			name:         "users/{id}",
			code:         http.StatusOK,
			method:       "GET",
			url:          "/users/123",
			wantResource: "GET /users/{id}",
			wantRoute:    "/users/{id}",
		},
		{
			name:         "404",
			code:         http.StatusNotFound,
			method:       "GET",
			url:          "/not_a_real_route",
			wantResource: "GET unknown",
			wantRoute:    "",
		},
		{
			name:         "405",
			code:         http.StatusMethodNotAllowed,
			method:       "POST",
			url:          "/405",
			wantResource: "POST unknown",
			wantRoute:    "",
		},
		{
			name:         "500",
			code:         http.StatusInternalServerError,
			method:       "GET",
			url:          "/500",
			wantResource: "GET /500",
			wantErr:      "500: Internal Server Error",
			wantRoute:    "/500",
		},
	} {
		t.Run(ht.name, func(t *testing.T) {
			assert := assert.New(t)
			mt := mocktracer.Start()
			defer mt.Stop()
			codeStr := strconv.Itoa(ht.code)

			// Send and verify a request
			r := httptest.NewRequest(ht.method, ht.url, nil)
			w := httptest.NewRecorder()
			subRouter().ServeHTTP(w, r)
			assert.Equal(ht.code, w.Code)
			assert.Equal(codeStr+"!\n", w.Body.String())

			spans := mt.FinishedSpans()
			assert.Equal(1, len(spans))

			s := spans[0]
			assert.Equal("http.request", s.OperationName())
			assert.Equal("my-service", s.Tag(ext.ServiceName))
			assert.Equal(codeStr, s.Tag(ext.HTTPCode))
			assert.Equal(ht.method, s.Tag(ext.HTTPMethod))
			assert.Equal("http://example.com"+ht.url, s.Tag(ext.HTTPURL))
			assert.Equal(ht.wantResource, s.Tag(ext.ResourceName))
			assert.Equal(ext.SpanKindServer, s.Tag(ext.SpanKind))
			assert.Equal("gorilla/mux", s.Tag(ext.Component))
			assert.Equal("gorilla/mux", s.Integration())
			if ht.wantRoute != "" {
				assert.Equal(ht.wantRoute, s.Tag(ext.HTTPRoute))
			} else {
				assert.NotContains(s.Tags(), ext.HTTPRoute)
			}

			if ht.wantErr != "" {
				assert.Equal(ht.wantErr, s.Tag(ext.Error).(error).Error())
			}
		})
	}
}

func TestDomain(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	mux := NewRouter(WithServiceName("my-service"))
	mux.Handle("/200", okHandler()).Host("localhost")
	r := httptest.NewRequest("GET", "http://localhost/200", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))
	assert.Equal("my-service", spans[0].Tag(ext.ServiceName))
	assert.Equal("localhost", spans[0].Tag("mux.host"))
}

func TestWithHeaderTags(t *testing.T) {
	setupReq := func(opts ...RouterOption) *http.Request {
		mux := NewRouter(opts...)
		mux.Handle("/test", okHandler())

		r := httptest.NewRequest("GET", "/test", nil)
		r.Header.Set("h!e@a-d.e*r", "val")
		r.Header.Add("h!e@a-d.e*r", "val2")
		r.Header.Set("2header", "2val")
		r.Header.Set("3header", "3val")
		mux.ServeHTTP(httptest.NewRecorder(), r)
		return r
	}
	t.Run("default-off", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		htArgs := []string{"h!e@a-d.e*r", "2header", "3header"}
		headerTags := instrumentation.NewHeaderTags(htArgs)

		setupReq()
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]
		headerTags.Iter(func(_ string, tag string) {
			assert.NotContains(s.Tags(), tag)
		})
	})
	t.Run("integration", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		htArgs := []string{"h!e@a-d.e*r", "2header:tag"}
		headerTags := instrumentation.NewHeaderTags(htArgs)
		r := setupReq(WithHeaderTags(htArgs))
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]

		headerTags.Iter(func(header string, tag string) {
			assert.Equal(strings.Join(r.Header.Values(header), ","), s.Tags()[tag])
		})
	})
}

func TestWithQueryParams(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	mux := NewRouter(WithQueryParams())
	mux.Handle("/200", okHandler()).Host("localhost")
	r := httptest.NewRequest("GET", "http://localhost/200?token=value&id=3&name=5", nil)

	mux.ServeHTTP(httptest.NewRecorder(), r)

	assert.Equal("http://localhost/200?<redacted>&id=3&name=5", mt.FinishedSpans()[0].Tags()[ext.HTTPURL])
}

func TestWithStatusCheck(t *testing.T) {
	for _, ht := range []struct {
		name          string
		hasErr        bool
		isStatusError func(statusCode int) bool
	}{
		{
			name:          "without-statuscheck",
			hasErr:        true,
			isStatusError: nil,
		},
		{
			name:          "with-statuscheck",
			hasErr:        false,
			isStatusError: func(statusCode int) bool { return false },
		},
	} {
		t.Run(ht.name, func(t *testing.T) {
			assert := assert.New(t)
			mt := mocktracer.Start()
			defer mt.Stop()

			r := httptest.NewRequest("GET", "/500", nil)
			w := httptest.NewRecorder()
			mux := NewRouter(WithStatusCheck(ht.isStatusError))
			mux.Handle("/500", errorHandler(http.StatusInternalServerError))
			mux.ServeHTTP(w, r)
			assert.Equal(http.StatusInternalServerError, w.Code)

			spans := mt.FinishedSpans()
			assert.Equal(1, len(spans))

			s := spans[0]
			_, ok := s.Tag(ext.Error).(error)
			assert.Equal(ht.hasErr, ok)
		})
	}
}

func TestSpanOptions(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	mux := NewRouter(WithSpanOptions(tracer.Tag(ext.SamplingPriority, 2)))
	mux.Handle("/200", okHandler()).Host("localhost")
	r := httptest.NewRequest("GET", "http://localhost/200", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))
	assert.Equal(2, spans[0].Tag(ext.SamplingPriority))
}

func TestNoDebugStack(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	mux := NewRouter(NoDebugStack())
	mux.Handle("/500", errorHandler(http.StatusInternalServerError)).Host("localhost")
	r := httptest.NewRequest("GET", "http://localhost/500", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	assert.Equal(http.StatusInternalServerError, w.Code)
	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))
	s := spans[0]
	assert.EqualError(s.Tags()[ext.Error].(error), "500: Internal Server Error")
	assert.Equal("<debug stack disabled>", spans[0].Tags()[ext.ErrorStack])
}

// TestImplementingMethods is a regression tests asserting that all the mux.Router methods
// returning the router will return the modified traced version of it and not the original
// router.
func TestImplementingMethods(_ *testing.T) {
	r := NewRouter()
	_ = (*Router)(r.StrictSlash(false))
	_ = (*Router)(r.SkipClean(false))
	_ = (*Router)(r.UseEncodedPath())
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...RouterOption) {
		mux := NewRouter(opts...)
		mux.Handle("/200", okHandler()).Host("localhost")
		r := httptest.NewRequest("GET", "http://localhost/200", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		s := spans[0]
		assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil)
	})

	t.Run("enabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 1.0, WithAnalytics(true))
	})

	t.Run("disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil, WithAnalytics(false))
	})
}

func TestIgnoreRequestOption(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tests := []struct {
		url       string
		spanCount int
	}{
		{
			url:       "/skip",
			spanCount: 0,
		},
		{
			url:       "/200",
			spanCount: 1,
		},
	}
	mux := NewRouter(WithIgnoreRequest(func(req *http.Request) bool {
		return req.URL.Path == "/skip"
	}))
	mux.Handle("/skip", okHandler()).Host("localhost")
	mux.Handle("/200", okHandler()).Host("localhost")

	for _, test := range tests {
		t.Run(test.url, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://localhost"+test.url, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)

			spans := mt.FinishedSpans()
			assert.Equal(t, test.spanCount, len(spans))
			mt.Reset()
		})
	}
}

func TestResourceNamer(t *testing.T) {
	staticName := "static resource name"
	staticNamer := func(*Router, *http.Request) string {
		return staticName
	}

	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	mux := NewRouter(WithResourceNamer(staticNamer))
	mux.Handle("/200", okHandler()).Host("localhost")
	r := httptest.NewRequest("GET", "http://localhost/200", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))
	assert.Equal(staticName, spans[0].Tag(ext.ResourceName))
}

func router() http.Handler {
	r := NewRouter(WithServiceName("my-service"))
	r.Handle("/200", okHandler())
	r.Handle("/500", errorHandler(http.StatusInternalServerError))
	r.Handle("/405", okHandler()).Methods("GET")
	r.Handle("/users/{id}", okHandler())
	r.NotFoundHandler = errorHandler(http.StatusNotFound)
	r.MethodNotAllowedHandler = errorHandler(http.StatusMethodNotAllowed)
	return r
}

func subRouter() http.Handler {
	r := NewRouter(WithServiceName("my-service"))
	sub := mux.NewRouter()
	sub.Handle("/200", okHandler())
	sub.Handle("/500", errorHandler(http.StatusInternalServerError))
	sub.Handle("/405", okHandler()).Methods("GET")
	sub.Handle("/users/{id}", okHandler())
	sub.NotFoundHandler = errorHandler(http.StatusNotFound)
	sub.MethodNotAllowedHandler = errorHandler(http.StatusMethodNotAllowed)
	r.Router = sub
	return r
}

func errorHandler(code int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, fmt.Sprintf("%d!", code), code)
	})
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("200!\n"))
	})
}
