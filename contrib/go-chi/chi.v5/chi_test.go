// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChildSpan(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := chi.NewRouter()
	router.Use(Middleware(WithService("foobar")))
	router.Get("/user/{id}", func(_ http.ResponseWriter, r *http.Request) {
		_, ok := tracer.SpanFromContext(r.Context())
		assert.True(ok)
	})

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, r)
}

func TestTrace200(t *testing.T) {
	assertDoRequest := func(assert *assert.Assertions, mt mocktracer.Tracer, router *chi.Mux) {
		r := httptest.NewRequest("GET", "/user/123", nil)
		w := httptest.NewRecorder()

		// do and verify the request
		router.ServeHTTP(w, r)
		response := w.Result()
		defer response.Body.Close()
		assert.Equal(response.StatusCode, 200)

		// verify traces look good
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		if len(spans) < 1 {
			t.Fatalf("no spans")
		}
		span := spans[0]
		assert.Equal("http.request", span.OperationName())
		assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Equal("foobar", span.Tag(ext.ServiceName))
		assert.Equal("GET /user/{id}", span.Tag(ext.ResourceName))
		assert.Equal("200", span.Tag(ext.HTTPCode))
		assert.Equal("GET", span.Tag(ext.HTTPMethod))
		assert.Equal("http://example.com/user/123", span.Tag(ext.HTTPURL))
		assert.Equal("go-chi/chi.v5", span.Tag(ext.Component))
		assert.Equal(componentName, span.Integration())
		assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
	}

	t.Run("response written", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := chi.NewRouter()
		router.Use(Middleware(WithService("foobar")))
		router.Get("/user/{id}", func(w http.ResponseWriter, r *http.Request) {
			span, ok := tracer.SpanFromContext(r.Context())
			assert.True(ok)
			assert.Equal(mocktracer.MockSpan(span).Tag(ext.ServiceName), "foobar")
			id := chi.URLParam(r, "id")
			_, err := w.Write([]byte(id))
			assert.NoError(err)
		})
		assertDoRequest(assert, mt, router)
	})

	t.Run("no response written", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := chi.NewRouter()
		router.Use(Middleware(WithService("foobar")))
		router.Get("/user/{id}", func(_ http.ResponseWriter, r *http.Request) {
			span, ok := tracer.SpanFromContext(r.Context())
			assert.True(ok)
			assert.Equal(mocktracer.MockSpan(span).Tag(ext.ServiceName), "foobar")
		})
		assertDoRequest(assert, mt, router)
	})
}

func TestWithModifyResourceName(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	router := chi.NewRouter()
	router.Use(Middleware(WithModifyResourceName(func(r string) string { return strings.TrimSuffix(r, "/") })))
	router.Get("/user/{id}/", func(_ http.ResponseWriter, _ *http.Request) {})

	r := httptest.NewRequest("GET", "/user/123/", nil)
	w := httptest.NewRecorder()

	// do and verify the request
	router.ServeHTTP(w, r)
	response := w.Result()
	defer response.Body.Close()
	assert.Equal(t, response.StatusCode, 200)

	// verify traces look good
	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)
	if len(spans) < 1 {
		t.Fatalf("no spans")
	}
	span := spans[0]
	assert.Equal(t, "GET /user/{id}", span.Tag(ext.ResourceName))
}

func TestError(t *testing.T) {
	assertSpan := func(assert *assert.Assertions, span mocktracer.Span, code int) {
		assert.Equal("http.request", span.OperationName())
		assert.Equal("foobar", span.Tag(ext.ServiceName))
		assert.Equal(strconv.Itoa(code), span.Tag(ext.HTTPCode))
	}

	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		// setup
		router := chi.NewRouter()
		router.Use(Middleware(WithService("foobar")))
		code := 500

		// a handler with an error and make the requests
		router.Get("/err", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, fmt.Sprintf("%d!", code), code)
		})
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
		assertSpan(assert, *span, code)
		wantErr := fmt.Sprintf("%d: %s", code, http.StatusText(code))
		assert.Equal(wantErr, span.Tag(ext.ErrorMsg))
	})

	t.Run("custom", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		// setup
		router := chi.NewRouter()
		router.Use(Middleware(
			WithService("foobar"),
			WithStatusCheck(func(statusCode int) bool {
				return statusCode >= 400
			}),
		))
		code := 404
		// a handler with an error and make the requests
		router.Get("/err", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, fmt.Sprintf("%d!", code), code)
		})
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
		assertSpan(assert, *span, code)
		wantErr := fmt.Sprintf("%d: %s", code, http.StatusText(code))
		assert.Equal(wantErr, span.Tag(ext.ErrorMsg))
	})
	t.Run("envvar", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_HTTP_SERVER_ERROR_STATUSES", "200")
		mt := mocktracer.Start()
		defer mt.Stop()

		// re-run config defaults based on new DD_TRACE_HTTP_SERVER_ERROR_STATUSES value
		httptrace.ResetCfg()

		router := chi.NewRouter()
		router.Use(Middleware(
			WithService("foobar")))
		code := 200
		// a handler with an error and make the requests
		router.Get("/err", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, fmt.Sprintf("%d!", code), code)
		})
		r := httptest.NewRequest("GET", "/err", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		response := w.Result()
		defer response.Body.Close()
		assert.Equal(response.StatusCode, code)

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assertSpan(assert, *span, code)
		wantErr := fmt.Sprintf("%d: %s", code, http.StatusText(code))
		assert.Equal(wantErr, span.Tag(ext.ErrorMsg))

	})
	t.Run("integration overrides global", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		t.Setenv("DD_TRACE_HTTP_SERVER_ERROR_STATUSES", "500")

		// setup
		router := chi.NewRouter()
		router.Use(Middleware(
			WithService("foobar"),
			WithStatusCheck(func(statusCode int) bool {
				return statusCode == 404
			}),
		))
		code := 404
		// a handler with an error and make the requests
		router.Get("/404", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, fmt.Sprintf("%d!", code), code)
		})
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
		assertSpan(assert, *span, code)
		wantErr := fmt.Sprintf("%d: %s", code, http.StatusText(code))
		assert.Equal(wantErr, span.Tag(ext.ErrorMsg))

		mt.Reset()

		code = 500
		router.Get("/500", func(w http.ResponseWriter, _ *http.Request) {
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
		assertSpan(assert, *span, 500)
		assert.Empty(span.Tag(ext.ErrorMsg))
	})
}

func TestGetSpanNotInstrumented(t *testing.T) {
	assert := assert.New(t)
	router := chi.NewRouter()
	router.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
		// Assert we don't have a span on the context.
		_, ok := tracer.SpanFromContext(r.Context())
		assert.False(ok)
		w.Write([]byte("ok"))
	})
	r := httptest.NewRequest("GET", "/ping", nil)
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

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	pspan := tracer.StartSpan("test")
	tracer.Inject(pspan.Context(), tracer.HTTPHeadersCarrier(r.Header))

	router := chi.NewRouter()
	router.Use(Middleware(WithService("foobar")))
	router.Get("/user/{id}", func(_ http.ResponseWriter, r *http.Request) {
		span, ok := tracer.SpanFromContext(r.Context())
		assert.True(ok)
		assert.Equal(mocktracer.MockSpan(span).ParentID(), mocktracer.MockSpan(pspan).SpanID())
	})

	router.ServeHTTP(w, r)
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		router := chi.NewRouter()
		router.Use(Middleware(opts...))
		router.Get("/user/{id}", func(_ http.ResponseWriter, r *http.Request) {
			_, ok := tracer.SpanFromContext(r.Context())
			assert.True(t, ok)
		})

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

func TestIgnoreRequest(t *testing.T) {
	router := chi.NewRouter()
	router.Use(Middleware(
		WithIgnoreRequest(func(r *http.Request) bool {
			return strings.HasPrefix(r.URL.Path, "/skip")
		}),
	))

	router.Get("/ok", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})

	router.Get("/skip", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("skip"))
	})

	for path, shouldSkip := range map[string]bool{
		"/ok":      false,
		"/skip":    true,
		"/skipfoo": true,
	} {
		mt := mocktracer.Start()
		r := httptest.NewRequest("GET", "http://localhost"+path, nil)
		router.ServeHTTP(httptest.NewRecorder(), r)
		assert.Equal(t, shouldSkip, len(mt.FinishedSpans()) == 0)
		mt.Stop()
	}
}

func TestWithHeaderTags(t *testing.T) {
	setupReq := func(opts ...Option) *http.Request {
		router := chi.NewRouter()
		router.Use(Middleware(opts...))

		router.Get("/test", func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte("test"))
		})
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
		htArgs := []string{"h!e@a-d.e*r", "2header", "3header"}
		setupReq()
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]

		instrumentation.NewHeaderTags(htArgs).Iter(func(_ string, tag string) {
			assert.NotContains(s.Tags(), tag)
		})
	})

	t.Run("integration", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		htArgs := []string{"h!e@a-d.e*r", "2header:tag"}
		_ = setupReq(WithHeaderTags(htArgs))
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]

		assert.Equal("val,val2", s.Tags()["http.request.headers.h_e_a-d_e_r"])
		assert.Equal("2val", s.Tags()["tag"])
		assert.NotContains(s.Tags(), "http.headers.x-datadog-header")
	})

	t.Run("global", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		testutils.SetGlobalHeaderTags(t, "3header")

		_ = setupReq()
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]

		assert.Equal("3val", s.Tags()["http.request.headers.3header"])
		assert.NotContains(s.Tags(), "http.request.headers.other")
		assert.NotContains(s.Tags(), "http.headers.x-datadog-header")
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		testutils.SetGlobalHeaderTags(t, "3header")

		htArgs := []string{"h!e@a-d.e*r", "2header:tag"}
		_ = setupReq(WithHeaderTags(htArgs))
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]

		assert.Equal("val,val2", s.Tags()["http.request.headers.h_e_a-d_e_r"])
		assert.Equal("2val", s.Tags()["tag"])
		assert.NotContains(s.Tags(), "http.headers.x-datadog-header")
		assert.NotContains(s.Tags(), "http.request.headers.3header")
	})
}

func TestCustomResourceName(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := chi.NewRouter()
	router.Use(Middleware(WithService("service-name"), WithResourceNamer(func(_ *http.Request) string {
		return "custom-resource-name"
	})))
	router.Get("/user/{id}", func(_ http.ResponseWriter, r *http.Request) {
		_, ok := tracer.SpanFromContext(r.Context())
		assert.True(ok)
	})

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, r)
	spans := mt.FinishedSpans()
	require.Equal(t, "/user/{id}", spans[0].Tag(ext.HTTPRoute))
	require.Equal(t, "service-name", spans[0].Tag(ext.ServiceName))
	require.Equal(t, "custom-resource-name", spans[0].Tag(ext.ResourceName))
}

func TestUnknownResourceName(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := chi.NewRouter()
	router.Use(Middleware(WithService("service-name")))
	router.Get("/user/{id}", func(_ http.ResponseWriter, r *http.Request) {
		_, ok := tracer.SpanFromContext(r.Context())
		assert.True(ok)
	})

	r := httptest.NewRequest("GET", "/other/123", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, r)
	spans := mt.FinishedSpans()
	require.Equal(t, "", spans[0].Tag(ext.HTTPRoute))
	require.Equal(t, "service-name", spans[0].Tag(ext.ServiceName))
	require.Equal(t, "GET unknown", spans[0].Tag(ext.ResourceName))
}

// Highly concurrent test running many goroutines to try to uncover concurrency
// issues such as deadlocks, data races, etc.
func TestConcurrency(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	expectedCap := 10
	opts := make([]Option, 0, expectedCap)
	opts = append(opts, []Option{
		WithService("foobar"),
		WithSpanOptions(tracer.Tag("tag1", "value1")),
	}...)
	expectedLen := 2

	router := chi.NewRouter()
	require.Len(t, opts, expectedLen)
	require.True(t, cap(opts) == expectedCap)

	router.Use(Middleware(opts...))
	router.Get("/user/{id}", func(_ http.ResponseWriter, r *http.Request) {
		_, ok := tracer.SpanFromContext(r.Context())
		require.True(t, ok)
	})

	// Create a bunch of goroutines that will all try to use the same router using our middleware
	nbReqGoroutines := 1000
	var startBarrier, finishBarrier sync.WaitGroup
	startBarrier.Add(1)
	finishBarrier.Add(nbReqGoroutines)

	for n := 0; n < nbReqGoroutines; n++ {
		go func() {
			startBarrier.Wait()
			defer finishBarrier.Done()

			for i := 0; i < 100; i++ {
				r := httptest.NewRequest("GET", "/user/123", nil)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
			}
		}()
	}

	startBarrier.Done()
	finishBarrier.Wait()

	// Side effects on opts is not the main purpose of this test, but it's worth checking just in case.
	require.Len(t, opts, expectedLen)
	require.True(t, cap(opts) == expectedCap)
	// All the others config data are internal to the closures in Middleware and cannot be tested.
	// Running this test with -race is the best chance to find a concurrency issue.
}
