// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestDatadogSemantics(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value string
	}{
		{name: "unset"},
		{name: "disabled", value: "false"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setChiHTTPConfig(t, tt.value)
			t.Setenv("DD_TRACE_RESOURCE_RENAMING_ENABLED", "true")
			httptrace.ResetCfg()

			matched := traceChiRequest(t, http.MethodGet, "http://example.com/users/123", http.StatusOK, http.MethodGet, "/users/{id}", nil)
			assert.Equal(t, "GET /users/{id}", matched.Tag(ext.ResourceName))
			assert.Equal(t, "/users/{id}", matched.Tag(ext.HTTPRoute))
			assert.Equal(t, "GET", matched.Tag(ext.HTTPMethod))
			assert.Equal(t, "http://example.com/users/123", matched.Tag(ext.HTTPURL))
			assert.Equal(t, "200", matched.Tag(ext.HTTPCode))
			assert.Nil(t, matched.Tag(ext.HTTPEndpoint))
			assert.Nil(t, matched.Tag(ext.HTTPRequestMethod))
			assert.Nil(t, matched.Tag(ext.URLPath))
			assert.Nil(t, matched.Tag(ext.HTTPResponseStatusCode))

			unmatched := traceChiRequest(t, http.MethodGet, "http://example.com/missing", http.StatusOK, "", "", nil)
			assert.Equal(t, "GET unknown", unmatched.Tag(ext.ResourceName))
			assert.Contains(t, unmatched.Tags(), ext.HTTPRoute)
			assert.Equal(t, "", unmatched.Tag(ext.HTTPRoute))
		})
	}
}

func TestOTelSemantics(t *testing.T) {
	setChiHTTPConfig(t, "true")
	t.Setenv("DD_TRACE_CLIENT_IP_ENABLED", "true")
	httptrace.ResetCfg()

	t.Run("route and attributes", func(t *testing.T) {
		span := traceChiRequest(t, http.MethodGet, "http://example.com:8080/users/123?password=secret&keep=value", http.StatusOK, http.MethodGet, "/users/{id}", nil,
			WithService("semantic-service"),
			WithSpanOptions(tracer.Tag("chi.custom", "value")),
		)
		assert.Equal(t, "GET /users/{id}", span.Tag(ext.ResourceName))
		assert.Equal(t, "/users/{id}", span.Tag(ext.HTTPRoute))
		assert.Equal(t, "GET", span.Tag(ext.HTTPRequestMethod))
		assert.Nil(t, span.Tag(ext.HTTPRequestMethodOriginal))
		assert.Equal(t, "/users/123", span.Tag(ext.URLPath))
		assert.Equal(t, "http", span.Tag(ext.URLScheme))
		assert.Equal(t, "<redacted>&keep=value", span.Tag(ext.URLQuery))
		assert.Equal(t, "example.com", span.Tag(ext.ServerAddress))
		assert.Equal(t, float64(8080), span.Tag(ext.ServerPort))
		assert.Equal(t, "semantic-agent", span.Tag(ext.UserAgentOriginal))
		assert.Equal(t, "203.0.113.10", span.Tag(ext.ClientAddress))
		assert.Equal(t, "192.0.2.1", span.Tag(ext.NetworkPeerAddress))
		assert.Equal(t, "200", span.Tag(ext.HTTPResponseStatusCode))
		assert.Equal(t, "semantic-service", span.Tag(ext.ServiceName))
		assert.Equal(t, ext.SpanKindServer, span.Tag(ext.SpanKind))
		assert.Equal(t, componentName, span.Tag(ext.Component))
		assert.Equal(t, string(instrumentation.PackageChiV5), span.Integration())
		assert.Equal(t, "http.request", span.OperationName())
		assert.Equal(t, ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Equal(t, "value", span.Tag("chi.custom"))
		assert.Nil(t, span.Tag(ext.HTTPMethod))
		assert.Nil(t, span.Tag(ext.HTTPURL))
		assert.Nil(t, span.Tag(ext.HTTPCode))
		assert.Nil(t, span.Tag(ext.HTTPUserAgent))
		assert.Nil(t, span.Tag(ext.HTTPClientIP))
		assert.Nil(t, span.Tag(ext.NetworkClientIP))
	})

	t.Run("route is invariant across parameters", func(t *testing.T) {
		first := traceChiRequest(t, http.MethodGet, "http://example.com/users/123", http.StatusOK, http.MethodGet, "/users/{id}", nil)
		second := traceChiRequest(t, http.MethodGet, "http://example.com/users/456", http.StatusOK, http.MethodGet, "/users/{id}", nil)
		assert.Equal(t, "GET /users/{id}", first.Tag(ext.ResourceName))
		assert.Equal(t, first.Tag(ext.ResourceName), second.Tag(ext.ResourceName))
	})

	for _, tt := range []struct {
		name         string
		method       string
		target       string
		routeMethod  string
		route        string
		wantResource string
		wantRoute    any
		wantMethod   string
		wantOriginal any
		wantPath     string
		wantStatus   string
	}{
		{name: "not found", method: http.MethodGet, target: "http://example.com/actual/path", routeMethod: http.MethodGet, route: "/registered", wantResource: "GET", wantMethod: "GET", wantPath: "/actual/path", wantStatus: "404"},
		{name: "case variant method", method: "gEt", target: "http://example.com/users/123", routeMethod: http.MethodGet, route: "/users/{id}", wantResource: "GET", wantMethod: "GET", wantOriginal: "gEt", wantPath: "/users/123", wantStatus: "405"},
		{name: "method not allowed", method: http.MethodPost, target: "http://example.com/users/123", routeMethod: http.MethodGet, route: "/users/{id}", wantResource: "POST", wantMethod: "POST", wantPath: "/users/123", wantStatus: "405"},
		{name: "unknown method", method: "PROPFIND", target: "http://example.com/users/123", routeMethod: http.MethodGet, route: "/users/{id}", wantResource: "HTTP", wantMethod: "_OTHER", wantOriginal: "PROPFIND", wantPath: "/users/123", wantStatus: "405"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			span := traceChiRequest(t, tt.method, tt.target, http.StatusOK, tt.routeMethod, tt.route, nil)
			assert.Equal(t, tt.wantResource, span.Tag(ext.ResourceName))
			assert.Equal(t, tt.wantRoute, span.Tag(ext.HTTPRoute))
			if tt.wantRoute == nil {
				assert.NotContains(t, span.Tags(), ext.HTTPRoute)
			}
			assert.Equal(t, tt.wantMethod, span.Tag(ext.HTTPRequestMethod))
			assert.Equal(t, tt.wantOriginal, span.Tag(ext.HTTPRequestMethodOriginal))
			assert.Equal(t, tt.wantPath, span.Tag(ext.URLPath))
			assert.Equal(t, tt.wantStatus, span.Tag(ext.HTTPResponseStatusCode))
		})
	}
}

func TestOTelSemanticsHTTPEndpoint(t *testing.T) {
	setChiHTTPConfig(t, "true")
	t.Setenv("DD_TRACE_RESOURCE_RENAMING_ENABLED", "true")
	httptrace.ResetCfg()

	matched := traceChiRequest(t, http.MethodGet, "http://example.com/users/alice", http.StatusOK, http.MethodGet, "/users/{id}", nil)
	assert.Equal(t, "/users/{id}", matched.Tag(ext.HTTPEndpoint))

	t.Setenv("DD_TRACE_RESOURCE_RENAMING_ALWAYS_SIMPLIFIED_ENDPOINT", "true")
	httptrace.ResetCfg()
	alwaysSimplified := traceChiRequest(t, http.MethodGet, "http://example.com/users/alice", http.StatusOK, http.MethodGet, "/users/{id}", nil)
	assert.Equal(t, "/users/alice", alwaysSimplified.Tag(ext.HTTPEndpoint))

	unmatched := traceChiRequest(t, http.MethodGet, "http://example.com/no_such_route_xyz", http.StatusOK, http.MethodGet, "/registered", nil)
	assert.Equal(t, "GET", unmatched.Tag(ext.ResourceName))
	assert.NotContains(t, unmatched.Tags(), ext.HTTPRoute)
	assert.Equal(t, "/no_such_route_xyz", unmatched.Tag(ext.HTTPEndpoint))
}

func TestOTelSemanticsFinalRoutePattern(t *testing.T) {
	setChiHTTPConfig(t, "true")
	mt := mocktracer.Start()
	defer mt.Stop()

	var initialResource any
	router := chi.NewRouter()
	router.Use(Middleware())
	router.Route("/api", func(r chi.Router) {
		r.Get("/users/{id}", func(_ http.ResponseWriter, req *http.Request) {
			span, ok := tracer.SpanFromContext(req.Context())
			require.True(t, ok)
			initialResource = mocktracer.MockSpan(span).Tag(ext.ResourceName)
		})
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/users/123", nil))

	assert.Equal(t, "GET", initialResource)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "/api/users/{id}", spans[0].Tag(ext.HTTPRoute))
	assert.Equal(t, "GET /api/users/{id}", spans[0].Tag(ext.ResourceName))
}

func TestOTelSemanticsStatus(t *testing.T) {
	setChiHTTPConfig(t, "true")

	for _, tt := range []struct {
		name          string
		status        int
		isStatusError func(int) bool
		wantErrorType any
	}{
		{name: "success", status: http.StatusOK},
		{name: "client error", status: http.StatusBadRequest},
		{name: "server error", status: http.StatusInternalServerError, wantErrorType: "500"},
		{name: "custom client error inclusion", status: http.StatusBadRequest, isStatusError: func(status int) bool { return status == http.StatusBadRequest }, wantErrorType: "400"},
		{name: "custom success inclusion", status: http.StatusCreated, isStatusError: func(status int) bool { return status == http.StatusCreated }, wantErrorType: "201"},
		{name: "custom exclusion", status: http.StatusInternalServerError, isStatusError: func(int) bool { return false }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.isStatusError != nil {
				opts = append(opts, WithStatusCheck(tt.isStatusError))
			}
			span := traceChiRequest(t, http.MethodGet, "http://example.com/status", tt.status, http.MethodGet, "/status", nil, opts...)
			assert.Equal(t, tt.wantErrorType, span.Tag(ext.ErrorType))
		})
	}
}

func TestOTelSemanticsResourceCustomization(t *testing.T) {
	setChiHTTPConfig(t, "true")

	modified := traceChiRequest(t, http.MethodGet, "http://example.com/users/123/", http.StatusOK, http.MethodGet, "/users/{id}/", nil,
		WithModifyResourceName(func(string) string { return "/modified/{id}" }),
	)
	assert.Equal(t, "/modified/{id}", modified.Tag(ext.HTTPRoute))
	assert.Equal(t, "GET /modified/{id}", modified.Tag(ext.ResourceName))

	custom := traceChiRequest(t, http.MethodGet, "http://example.com/users/123", http.StatusOK, http.MethodGet, "/users/{id}", nil,
		WithResourceNamer(func(*http.Request) string { return "custom-resource" }),
	)
	assert.Equal(t, "/users/{id}", custom.Tag(ext.HTTPRoute))
	assert.Equal(t, "custom-resource", custom.Tag(ext.ResourceName))
}

func TestOTelSemanticsContextPropagation(t *testing.T) {
	setChiHTTPConfig(t, "true")
	mt := mocktracer.Start()
	defer mt.Stop()

	var handlerSpan *tracer.Span
	router := chi.NewRouter()
	router.Use(Middleware())
	router.Get("/users/{id}", func(_ http.ResponseWriter, r *http.Request) {
		handlerSpan, _ = tracer.SpanFromContext(r.Context())
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/123", nil))

	require.NotNil(t, handlerSpan)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /users/{id}", spans[0].Tag(ext.ResourceName))
}

func TestOTelSemanticsAppSecRouteParams(t *testing.T) {
	setChiHTTPConfig(t, "true")
	testutils.StartAppSec(t)
	httptrace.ResetCfg()

	mt := mocktracer.Start()
	defer mt.Stop()
	router := chi.NewRouter().With(Middleware())
	router.Get("/users/{id}", func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte("ok"))
		require.NoError(t, err)
	})
	req := httptest.NewRequest(http.MethodGet, "/users/appscan_fingerprint", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	router.ServeHTTP(httptest.NewRecorder(), req)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /users/{id}", spans[0].Tag(ext.ResourceName))
	assert.Equal(t, "/users/{id}", spans[0].Tag(ext.HTTPRoute))
	assert.NotEmpty(t, spans[0].Tag(ext.HTTPEndpoint))
	assert.Equal(t, "203.0.113.10", spans[0].Tag(ext.ClientAddress))
	assert.Equal(t, "192.0.2.1", spans[0].Tag(ext.NetworkPeerAddress))
	assert.Nil(t, spans[0].Tag(ext.HTTPClientIP))
	assert.Nil(t, spans[0].Tag(ext.NetworkClientIP))
	event, ok := spans[0].Tag("_dd.appsec.json").(string)
	require.Truef(t, ok, "span tags: %#v", spans[0].Tags())
	assert.Contains(t, event, "server.request.path_params")
	assert.Contains(t, event, "appscan_fingerprint")
}

func traceChiRequest(t *testing.T, method, target string, status int, routeMethod, route string, handler http.HandlerFunc, opts ...Option) *mocktracer.Span {
	t.Helper()
	mt := mocktracer.Start()
	defer mt.Stop()

	router := chi.NewRouter()
	router.Use(Middleware(opts...))
	if route != "" {
		if handler == nil {
			handler = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }
		}
		router.MethodFunc(routeMethod, route, handler)
	} else {
		router.Get("/registered", func(http.ResponseWriter, *http.Request) {})
	}

	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("User-Agent", "semantic-agent")
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	router.ServeHTTP(httptest.NewRecorder(), req)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	return spans[0]
}

func setChiHTTPConfig(t *testing.T, otel string) {
	t.Helper()
	oldOTel, hadOTel := os.LookupEnv("DD_TRACE_OTEL_SEMANTICS_ENABLED")
	if otel == "" {
		require.NoError(t, os.Unsetenv("DD_TRACE_OTEL_SEMANTICS_ENABLED"))
	} else {
		require.NoError(t, os.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", otel))
	}
	require.NoError(t, tracer.Start(tracer.WithTraceEnabled(false)))
	httptrace.ResetCfg()
	t.Cleanup(func() {
		if hadOTel {
			require.NoError(t, os.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", oldOTel))
		} else {
			require.NoError(t, os.Unsetenv("DD_TRACE_OTEL_SEMANTICS_ENABLED"))
		}
		require.NoError(t, tracer.Start(tracer.WithTraceEnabled(false)))
		httptrace.ResetCfg()
		tracer.Stop()
	})
}
