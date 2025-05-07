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
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

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

func TestWithHeaderTags(t *testing.T) {
	setupReq := func(opts ...Option) *http.Request {
		mux := http.NewServeMux()
		mux.HandleFunc("/test", func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte("test"))
		})
		router := negroni.New()
		router.Use(Middleware(opts...))
		router.UseHandler(mux)
		r := httptest.NewRequest("GET", "/test", nil)
		r.Header.Set("h!e@a-d.e*r", "val")
		r.Header.Add("h!e@a-d.e*r", "val2")
		r.Header.Set("2header", "2val")
		r.Header.Set("3header", "3val")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return r
	}
	t.Run("default-off", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		headerTags := instrumentation.NewHeaderTags([]string{"h!e@a-d.e*r", "2header", "3header", "x-datadog-header"})
		setupReq()
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]
		headerTags.Iter(func(header string, tag string) {
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
		assert.NotContains(s.Tags(), "http.headers.x-datadog-header")
	})

	t.Run("global", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		htArgs := []string{"3header"}
		testutils.SetGlobalHeaderTags(t, htArgs...)
		headerTags := instrumentation.NewHeaderTags(htArgs)

		r := setupReq()
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]

		headerTags.Iter(func(header string, tag string) {
			assert.Equal(strings.Join(r.Header.Values(header), ","), s.Tags()[tag])
		})
		assert.NotContains(s.Tags(), "http.headers.x-datadog-header")
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		testutils.SetGlobalHeaderTags(t, "3header")
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
		assert.NotContains(s.Tags(), "http.headers.x-datadog-header")
		assert.NotContains(s.Tags(), "3header")
	})
}

func TestTrace200(t *testing.T) {
	assertDoRequest := func(assert *assert.Assertions, mt mocktracer.Tracer, router *negroni.Negroni, resourceName string) {
		r := httptest.NewRequest("GET", "/user", nil)
		w := httptest.NewRecorder()

		// do and verify the request
		router.ServeHTTP(w, r)
		response := w.Result()
		defer response.Body.Close()
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
		assert.Equal("urfave/negroni", span.Tag(ext.Component))
		assert.Equal(string(instrumentation.PackageUrfaveNegroni), span.Integration())
		assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
	}

	t.Run("response", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		mux := http.NewServeMux()
		mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
			span, ok := tracer.SpanFromContext(r.Context())
			assert.True(ok)
			assert.Equal(mocktracer.MockSpan(span).Tag(ext.ServiceName), "foobar")
			w.WriteHeader(200)
			w.Write([]byte("hi!"))
		})

		router := negroni.New()
		router.Use(Middleware(WithService("foobar")))
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
			assert.Equal(mocktracer.MockSpan(span).Tag(ext.ServiceName), "foobar")
			w.WriteHeader(200)
		})

		router := negroni.New()
		router.Use(Middleware(WithService("foobar")))
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
			assert.Equal(mocktracer.MockSpan(span).Tag(ext.ServiceName), "foobar")
			w.WriteHeader(200)
		})

		router := negroni.New()
		router.Use(Middleware(WithService("foobar"), WithResourceNamer(func(r *http.Request) string {
			return fmt.Sprintf("%s %s", r.Method, r.URL.Path)
		})))
		router.UseHandler(mux)
		assertDoRequest(assert, mt, router, "GET /user")
	})
}

func TestError(t *testing.T) {
	assertSpan := func(assert *assert.Assertions, span *mocktracer.Span, code int) {
		assert.Equal("http.request", span.OperationName())
		assert.Equal("negroni.router", span.Tag(ext.ServiceName))
		assert.Equal(strconv.Itoa(code), span.Tag(ext.HTTPCode))
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
		mux.HandleFunc("/err", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, fmt.Sprintf("%d!", code), code)
		})
		router.UseHandler(mux)

		r := httptest.NewRequest("GET", "/err", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		response := w.Result()
		defer response.Body.Close()
		assert.Equal(response.StatusCode, code)

		// verify the errors and status are correct
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assertSpan(assert, span, code)
		wantErr := fmt.Sprintf("%d: %s", code, http.StatusText(code))
		assert.Equal(wantErr, span.Tag(ext.ErrorMsg))
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
		mux.HandleFunc("/err", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, fmt.Sprintf("%d!", code), code)
		})
		router.UseHandler(mux)
		r := httptest.NewRequest("GET", "/err", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		response := w.Result()
		defer response.Body.Close()
		assert.Equal(response.StatusCode, code)

		// verify the errors and status are correct
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assertSpan(assert, span, code)
		wantErr := fmt.Sprintf("%d: %s", code, http.StatusText(code))
		assert.Equal(wantErr, span.Tag(ext.ErrorMsg))
	})

	t.Run("integration overrides global", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		t.Setenv("DD_TRACE_HTTP_SERVER_ERROR_STATUSES", "500")

		// setup
		router := negroni.New()
		code := 404
		router.Use(Middleware(WithStatusCheck(func(statusCode int) bool {
			return statusCode == 404
		})))

		// a handler with an error and make the requests
		mux := http.NewServeMux()
		mux.HandleFunc("/404", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, fmt.Sprintf("%d!", code), code)
		})
		router.UseHandler(mux)
		r := httptest.NewRequest("GET", "/404", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		response := w.Result()
		defer response.Body.Close()
		assert.Equal(response.StatusCode, code)

		// verify the errors and status are correct
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assertSpan(assert, span, code)
		wantErr := fmt.Sprintf("%d: %s", code, http.StatusText(code))
		assert.Equal(wantErr, span.Tag(ext.ErrorMsg))

		mt.Reset()

		code = 500
		mux.HandleFunc("/500", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, fmt.Sprintf("%d!", code), code)
		})
		r = httptest.NewRequest("GET", "/500", nil)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, r)
		response = w.Result()
		defer response.Body.Close()
		assert.Equal(response.StatusCode, 500)

		// verify that span does not have error tag
		spans = mt.FinishedSpans()
		assert.Len(spans, 1)
		span = spans[0]
		assertSpan(assert, span, 500)
		assert.Empty(span.Tag(ext.ErrorMsg))
	})
}

func TestGetSpanNotInstrumented(t *testing.T) {
	assert := assert.New(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(_ http.ResponseWriter, _ *http.Request) {
	})

	router := negroni.New()
	router.Use(Middleware())
	router.UseHandler(mux)

	r := httptest.NewRequest("GET", "/user", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	response := w.Result()
	defer response.Body.Close()
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
		assert.Equal(mocktracer.MockSpan(span).ParentID(), mocktracer.MockSpan(pspan).SpanID())
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
		mux.HandleFunc("/user", func(_ http.ResponseWriter, r *http.Request) {
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

		testutils.SetGlobalAnalyticsRate(t, 0.4)
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

		testutils.SetGlobalAnalyticsRate(t, 0.4)
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
			assert.Equal(mocktracer.MockSpan(span).Tag(ext.ServiceName), servicename)
			w.WriteHeader(200)
		})

		router.UseHandler(mux)

		r := httptest.NewRequest("GET", "/user", nil)
		w := httptest.NewRecorder()

		// do and verify the request
		router.ServeHTTP(w, r)
		response := w.Result()
		defer response.Body.Close()
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
		testutils.SetGlobalServiceName(t, "global-service")

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
		router.Use(Middleware(WithService("my-service")))
		assertServiceName(t, mt, router, "my-service")
	})
}
