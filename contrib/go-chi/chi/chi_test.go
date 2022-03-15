// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	pappsec "gopkg.in/DataDog/dd-trace-go.v1/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/go-chi/chi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChildSpan(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := chi.NewRouter()
	router.Use(Middleware(WithServiceName("foobar")))
	router.Get("/user/{id}", func(w http.ResponseWriter, r *http.Request) {
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
		assert.Equal("/user/123", span.Tag(ext.HTTPURL))
	}

	t.Run("response written", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		router := chi.NewRouter()
		router.Use(Middleware(WithServiceName("foobar")))
		router.Get("/user/{id}", func(w http.ResponseWriter, r *http.Request) {
			span, ok := tracer.SpanFromContext(r.Context())
			assert.True(ok)
			assert.Equal(span.(mocktracer.Span).Tag(ext.ServiceName), "foobar")
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
		router.Use(Middleware(WithServiceName("foobar")))
		router.Get("/user/{id}", func(w http.ResponseWriter, r *http.Request) {
			span, ok := tracer.SpanFromContext(r.Context())
			assert.True(ok)
			assert.Equal(span.(mocktracer.Span).Tag(ext.ServiceName), "foobar")
		})
		assertDoRequest(assert, mt, router)
	})
}

func TestError(t *testing.T) {
	assertSpan := func(assert *assert.Assertions, spans []mocktracer.Span, code int) {
		assert.Len(spans, 1)
		if len(spans) < 1 {
			t.Fatalf("no spans")
		}
		span := spans[0]
		assert.Equal("http.request", span.OperationName())
		assert.Equal("foobar", span.Tag(ext.ServiceName))

		assert.Equal(strconv.Itoa(code), span.Tag(ext.HTTPCode))

		wantErr := fmt.Sprintf("%d: %s", code, http.StatusText(code))
		assert.Equal(wantErr, span.Tag(ext.Error).(error).Error())
	}

	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		// setup
		router := chi.NewRouter()
		router.Use(Middleware(WithServiceName("foobar")))
		code := 500

		// a handler with an error and make the requests
		router.Get("/err", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, fmt.Sprintf("%d!", code), code)
		})
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
		router := chi.NewRouter()
		router.Use(Middleware(
			WithServiceName("foobar"),
			WithStatusCheck(func(statusCode int) bool {
				return statusCode >= 400
			}),
		))
		code := 404
		// a handler with an error and make the requests
		router.Get("/err", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, fmt.Sprintf("%d!", code), code)
		})
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
	router.Use(Middleware(WithServiceName("foobar")))
	router.Get("/user/{id}", func(w http.ResponseWriter, r *http.Request) {
		span, ok := tracer.SpanFromContext(r.Context())
		assert.True(ok)
		assert.Equal(span.(mocktracer.Span).ParentID(), pspan.(mocktracer.Span).SpanID())
	})

	router.ServeHTTP(w, r)
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		router := chi.NewRouter()
		router.Use(Middleware(opts...))
		router.Get("/user/{id}", func(w http.ResponseWriter, r *http.Request) {
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

func TestIgnoreRequest(t *testing.T) {
	router := chi.NewRouter()
	router.Use(Middleware(
		WithIgnoreRequest(func(r *http.Request) bool {
			return strings.HasPrefix(r.URL.Path, "/skip")
		}),
	))

	router.Get("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	router.Get("/skip", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("skip"))
	})

	for path, shouldSkip := range map[string]bool{
		"/ok":      false,
		"/skip":    true,
		"/skipfoo": true,
	} {
		mt := mocktracer.Start()
		defer mt.Reset()

		r := httptest.NewRequest("GET", "http://localhost"+path, nil)
		router.ServeHTTP(httptest.NewRecorder(), r)
		assert.Equal(t, shouldSkip, len(mt.FinishedSpans()) == 0)
	}
}

func TestAppSec(t *testing.T) {
	appsec.Start()
	defer appsec.Stop()

	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	// Start and trace an HTTP server with some testing routes
	router := chi.NewRouter().With(Middleware())
	router.HandleFunc("/path0.0/{myPathParam0}/path0.1/{myPathParam1}/path0.2/{myPathParam2}/path0.3/*", func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte("Hello World!\n"))
		require.NoError(t, err)
	})
	router.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte("Hello World!\n"))
		require.NoError(t, err)
	})
	router.HandleFunc("/body", func(w http.ResponseWriter, r *http.Request) {
		pappsec.MonitorParsedHTTPBody(r.Context(), "$globals")
		_, err := w.Write([]byte("Hello Body!\n"))
		require.NoError(t, err)
	})

	srv := httptest.NewServer(router)
	defer srv.Close()

	// Test an LFI attack via path parameters
	t.Run("request-uri", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		// Send an LFI attack (according to appsec rule id crs-930-110)
		req, err := http.NewRequest("POST", srv.URL+"/../../../secret.txt", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		// Check that the server behaved as intended
		require.Equal(t, http.StatusOK, res.StatusCode)
		b, err := ioutil.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(b))
		// The span should contain the security event
		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// The first 301 redirection should contain the attack via the request uri
		event := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "server.request.uri.raw"))
		require.True(t, strings.Contains(event, "crs-930-110"))
	})

	// Test a security scanner attack via path parameters
	t.Run("path-params", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		// Send a security scanner attack (according to appsec rule id crs-913-120)
		req, err := http.NewRequest("POST", srv.URL+"/path0.0/param0/path0.1/param1/path0.2/appscan_fingerprint/path0.3/param3", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		// Check that the handler was properly called
		b, err := ioutil.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(b))
		require.Equal(t, http.StatusOK, res.StatusCode)
		// The span should contain the security event
		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		event := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "crs-913-120"))
		require.True(t, strings.Contains(event, "myPathParam2"))
		require.True(t, strings.Contains(event, "server.request.path_params"))
	})

	// Test a PHP injection attack via request parsed body
	t.Run("SDK-body", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("POST", srv.URL+"/body", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)

		// Check that the handler was properly called
		b, err := ioutil.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello Body!\n", string(b))

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		event := finished[0].Tag("_dd.appsec.json")
		require.NotNil(t, event)
		require.True(t, strings.Contains(event.(string), "crs-933-130"))
	})
}
