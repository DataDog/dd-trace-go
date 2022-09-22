// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package negroni provides helper functions for tracing the urfave/negroni package (https://github.com/urfave/negroni).
package negroni

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
	"github.com/urfave/negroni"
)

func TestChildSpan(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		_, ok := tracer.SpanFromContext(r.Context())
		assert.True(ok)
		w.WriteHeader(200)
	})

	router := negroni.New()
	router.Use(Middleware())
	router.UseHandler(mux)
	r := httptest.NewRequest("GET", "/user", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
}

func TestTrace200(t *testing.T) {
	assertDoRequest := func(assert *assert.Assertions, mt mocktracer.Tracer, router *negroni.Negroni, resourceName string) {
		r := httptest.NewRequest("GET", "/user", nil)
		w := httptest.NewRecorder()

		// do and verify the request
		router.ServeHTTP(w, r)
		response := w.Result()
		assert.Equal(response.StatusCode, 200)

		// verify traces look good
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal("http.request", span.OperationName())
		assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Equal("foobar", span.Tag(ext.ServiceName))
		assert.Equal(resourceName, span.Tag(ext.ResourceName))
		assert.Equal("200", span.Tag(ext.HTTPCode))
		assert.Equal("GET", span.Tag(ext.HTTPMethod))
		assert.Equal("http://example.com/user", span.Tag(ext.HTTPURL))
	}

	t.Run("response", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		mux := http.NewServeMux()
		mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
			span, ok := tracer.SpanFromContext(r.Context())
			assert.True(ok)
			assert.Equal(span.(mocktracer.Span).Tag(ext.ServiceName), "foobar")
			w.WriteHeader(200)
			w.Write([]byte("hi!"))
		})

		router := negroni.New()
		router.Use(Middleware(WithServiceName("foobar")))
		router.UseHandler(mux)
		assertDoRequest(assert, mt, router, "")
	})

	t.Run("no-response", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		mux := http.NewServeMux()
		mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
			span, ok := tracer.SpanFromContext(r.Context())
			assert.True(ok)
			assert.Equal(span.(mocktracer.Span).Tag(ext.ServiceName), "foobar")
			w.WriteHeader(200)
		})

		router := negroni.New()
		router.Use(Middleware(WithServiceName("foobar")))
		router.UseHandler(mux)
		assertDoRequest(assert, mt, router, "")
	})
	t.Run("resourcename", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		mux := http.NewServeMux()
		mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
			span, ok := tracer.SpanFromContext(r.Context())
			assert.True(ok)
			assert.Equal(span.(mocktracer.Span).Tag(ext.ServiceName), "foobar")
			w.WriteHeader(200)
		})

		router := negroni.New()
		router.Use(Middleware(WithServiceName("foobar"), WithResourceNamer(func(r *http.Request) string {
			return fmt.Sprintf("%s %s", r.Method, r.URL.Path)
		})))
		router.UseHandler(mux)
		assertDoRequest(assert, mt, router, "GET /user")
	})
}

func TestError(t *testing.T) {
	assertSpan := func(assert *assert.Assertions, spans []mocktracer.Span, code int) {
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal("http.request", span.OperationName())
		assert.Equal(strconv.Itoa(code), span.Tag(ext.HTTPCode))
		wantErr := fmt.Sprintf("%d: %s", code, http.StatusText(code))
		assert.Equal(wantErr, span.Tag(ext.Error).(error).Error())
	}

	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		// setup
		router := negroni.New()
		router.Use(Middleware())

		code := 500

		// a handler with an error and make the requests
		mux := http.NewServeMux()
		mux.HandleFunc("/err", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, fmt.Sprintf("%d!", code), code)
		})
		router.UseHandler(mux)

		r := httptest.NewRequest("GET", "/err", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		response := w.Result()
		assert.Equal(response.StatusCode, code)

		// verify the errors and status are correct
		spans := mt.FinishedSpans()
		assertSpan(assert, spans, code)
	})

	t.Run("custom", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		// setup
		router := negroni.New()
		router.Use(Middleware(WithStatusCheck(func(statusCode int) bool {
			return statusCode >= 400
		}),
			WithSpanOptions(tracer.Tag("foo", "bar")),
		))
		code := 404
		// a handler with an error and make the requests
		mux := http.NewServeMux()
		mux.HandleFunc("/err", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, fmt.Sprintf("%d!", code), code)
		})
		router.UseHandler(mux)
		r := httptest.NewRequest("GET", "/err", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		response := w.Result()
		assert.Equal(response.StatusCode, code)

		// verify the errors and status are correct
		spans := mt.FinishedSpans()
		assertSpan(assert, spans, code)
	})
}

func TestGetSpanNotInstrumented(t *testing.T) {
	assert := assert.New(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
	})

	router := negroni.New()
	router.Use(Middleware())
	router.UseHandler(mux)

	r := httptest.NewRequest("GET", "/user", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	response := w.Result()
	assert.Equal(response.StatusCode, 200)
}

func TestPropagation(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	r := httptest.NewRequest("GET", "/user", nil)
	w := httptest.NewRecorder()

	pspan := tracer.StartSpan("test")
	tracer.Inject(pspan.Context(), tracer.HTTPHeadersCarrier(r.Header))

	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		span, ok := tracer.SpanFromContext(r.Context())
		assert.True(ok)
		assert.Equal(span.(mocktracer.Span).ParentID(), pspan.(mocktracer.Span).SpanID())
		w.WriteHeader(200)
	})

	router := negroni.New()
	router.Use(Middleware())
	router.UseHandler(mux)
	router.ServeHTTP(w, r)
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		router := negroni.New()
		router.Use(Middleware(opts...))

		mux := http.NewServeMux()
		mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
			_, ok := tracer.SpanFromContext(r.Context())
			assert.True(t, ok)
		})
		router.UseHandler(mux)

		r := httptest.NewRequest("GET", "/user/123", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, r)
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

func TestServiceName(t *testing.T) {
	assertServiceName := func(t *testing.T, mt mocktracer.Tracer, router *negroni.Negroni, servicename string) {
		assert := assert.New(t)
		mux := http.NewServeMux()
		mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
			span, ok := tracer.SpanFromContext(r.Context())
			assert.True(ok)
			assert.Equal(span.(mocktracer.Span).Tag(ext.ServiceName), servicename)
			w.WriteHeader(200)
		})

		router.UseHandler(mux)

		r := httptest.NewRequest("GET", "/user", nil)
		w := httptest.NewRecorder()

		// do and verify the request
		router.ServeHTTP(w, r)
		response := w.Result()
		assert.Equal(response.StatusCode, 200)

		// verify traces look good
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal(servicename, span.Tag(ext.ServiceName))
	}

	t.Run("default", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		router := negroni.New()
		router.Use(Middleware())
		assertServiceName(t, mt, router, "negroni.router")
	})

	t.Run("global", func(t *testing.T) {
		globalconfig.SetServiceName("global-service")
		defer globalconfig.SetServiceName("")

		mt := mocktracer.Start()
		defer mt.Stop()

		router := negroni.New()
		router.Use(Middleware())
		assertServiceName(t, mt, router, "global-service")
	})

	t.Run("custom", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		router := negroni.New()
		router.Use(Middleware(WithServiceName("my-service")))
		assertServiceName(t, mt, router, "my-service")
	})
}
