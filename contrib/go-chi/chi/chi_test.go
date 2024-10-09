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
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
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
		assert.Equal("go-chi/chi", span.Tag(ext.Component))
		assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
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
		assert.Equal(wantErr, span.Tag(ext.ErrorMsg))
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
		defer response.Body.Close()
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
		defer response.Body.Close()
		assert.Equal(response.StatusCode, code)

		// verify the errors and status are correct
		spans := mt.FinishedSpans()
		assertSpan(assert, spans, code)
	})
}

func TestWithHeaderTags(t *testing.T) {
	setupReq := func(opts ...Option) *http.Request {
		router := chi.NewRouter()
		router.Use(Middleware(opts...))

		router.Get("/test", func(w http.ResponseWriter, r *http.Request) {
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
	router.Use(Middleware(WithServiceName("foobar")))
	router.Get("/user/{id}", func(w http.ResponseWriter, r *http.Request) {
		span, ok := tracer.SpanFromContext(r.Context())
		assert.True(ok)
		assert.Equal(span.(mocktracer.MockspanV2Adapter).ParentID(), pspan.Context().SpanID())
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

func TestNamingSchema(t *testing.T) {
	genSpans := namingschematest.GenSpansFn(func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []Option
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		mux := chi.NewRouter().With(Middleware(opts...))
		mux.HandleFunc("/200", func(w http.ResponseWriter, r *http.Request) {
			_, err := w.Write([]byte("ok"))
			require.NoError(t, err)
		})
		r := httptest.NewRequest("GET", "/200", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)

		return mt.FinishedSpans()
	})
	namingschematest.NewHTTPServerTest(genSpans, "chi.router")(t)
}

func TestCustomResourceName(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	router := chi.NewRouter()
	router.Use(Middleware(WithServiceName("service-name"), WithResourceNamer(func(r *http.Request) string {
		return "custom-resource-name"
	})))
	router.Get("/user/{id}", func(w http.ResponseWriter, r *http.Request) {
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
	router.Use(Middleware(WithServiceName("service-name")))
	router.Get("/user/{id}", func(w http.ResponseWriter, r *http.Request) {
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
