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
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/stretchr/testify/assert"
)

func TestHttpTracer(t *testing.T) {
	for _, ht := range []struct {
		code         int
		method       string
		url          string
		resourceName string
		errorStr     string
	}{
		{
			code:         http.StatusOK,
			method:       "GET",
			url:          "/200",
			resourceName: "GET /200",
		},
		{
			code:         http.StatusNotFound,
			method:       "GET",
			url:          "/not_a_real_route",
			resourceName: "GET unknown",
		},
		{
			code:         http.StatusMethodNotAllowed,
			method:       "POST",
			url:          "/405",
			resourceName: "POST unknown",
		},
		{
			code:         http.StatusInternalServerError,
			method:       "GET",
			url:          "/500",
			resourceName: "GET /500",
			errorStr:     "500: Internal Server Error",
		},
	} {
		t.Run(http.StatusText(ht.code), func(t *testing.T) {
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
			assert.Equal(ht.url, s.Tag(ext.HTTPURL))
			assert.Equal(ht.resourceName, s.Tag(ext.ResourceName))
			if ht.errorStr != "" {
				assert.Equal(ht.errorStr, s.Tag(ext.Error).(error).Error())
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
	assert.Equal("localhost", spans[0].Tag("mux.host"))
}

func TestWithHeaderTags(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	mux := NewRouter(WithServiceName("my-service"), WithHeaderTags())
	mux.Handle("/200", okHandler()).Host("localhost")
	r := httptest.NewRequest("GET", "http://localhost/200", nil)
	r.Header.Set("header", "header-value")
	r.Header.Set("x-datadog-header", "value")
	mux.ServeHTTP(httptest.NewRecorder(), r)

	spans := mt.FinishedSpans()
	assert.Equal("header-value", spans[0].Tags()["http.request.headers.Header"])
	assert.NotContains(spans[0].Tags(), "http.headers.X-Datadog-Header")
}

func TestWithQueryParams(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	mux := NewRouter(WithQueryParams())
	mux.Handle("/200", okHandler()).Host("localhost")
	r := httptest.NewRequest("GET", "http://localhost/200?token=value&id=3&name=5", nil)

	mux.ServeHTTP(httptest.NewRecorder(), r)

	assert.Equal("/200?token=value&id=3&name=5", mt.FinishedSpans()[0].Tags()[ext.HTTPURL])
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
func TestImplementingMethods(t *testing.T) {
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

	t.Run("global", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.4)
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

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})
}

func TestIgnoreRequestOption(t *testing.T) {
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
			mt := mocktracer.Start()
			defer mt.Stop()
			r := httptest.NewRequest("GET", "http://localhost"+test.url, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)

			spans := mt.FinishedSpans()
			assert.Equal(t, test.spanCount, len(spans))
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
	mux := NewRouter(WithServiceName("my-service"))
	mux.Handle("/200", okHandler())
	mux.Handle("/500", errorHandler(http.StatusInternalServerError))
	mux.Handle("/405", okHandler()).Methods("GET")
	mux.NotFoundHandler = errorHandler(http.StatusNotFound)
	mux.MethodNotAllowedHandler = errorHandler(http.StatusMethodNotAllowed)
	return mux
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
