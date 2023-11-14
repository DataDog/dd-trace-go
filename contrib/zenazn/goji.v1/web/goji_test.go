// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zenazn/goji/web"
)

func TestNoRouter(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	m := web.New()
	m.Use(Middleware(WithServiceName("my-router")))
	m.Get("/user/:id", func(c web.C, w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})
	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	if len(spans) < 1 {
		t.Fatalf("no spans")
	}
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
	assert.Equal("my-router", span.Tag(ext.ServiceName))
	assert.Equal("GET", span.Tag(ext.ResourceName))
	assert.Equal("200", span.Tag(ext.HTTPCode))
	assert.Equal("GET", span.Tag(ext.HTTPMethod))
	assert.Equal("http://example.com/user/123", span.Tag(ext.HTTPURL))
	assert.Equal("zenazn/goji.v1/web", span.Tag(ext.Component))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
	assert.NotContains(span.Tags(), ext.HTTPRoute)
}

func TestTraceWithRouter(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	m := web.New()
	m.Use(m.Router)
	m.Use(Middleware(WithServiceName("my-router")))
	m.Get("/user/:id", func(c web.C, w http.ResponseWriter, r *http.Request) {
		span, ok := tracer.SpanFromContext(r.Context())
		assert.True(ok)
		assert.Equal(span.(mocktracer.Span).Tag(ext.ServiceName), "my-router")
		id := c.URLParams["id"]
		w.Write([]byte(id))
	})
	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	response := w.Result()
	defer response.Body.Close()
	assert.Equal(response.StatusCode, 200)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	if len(spans) < 1 {
		t.Fatalf("no spans")
	}
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
	assert.Equal("my-router", span.Tag(ext.ServiceName))
	assert.Equal("GET /user/:id", span.Tag(ext.ResourceName))
	assert.Equal("200", span.Tag(ext.HTTPCode))
	assert.Equal("GET", span.Tag(ext.HTTPMethod))
	assert.Equal("http://example.com/user/123", span.Tag(ext.HTTPURL))
	assert.Equal("zenazn/goji.v1/web", span.Tag(ext.Component))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
	assert.Equal("/user/:id", span.Tag(ext.HTTPRoute))
}

func TestError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	m := web.New()
	m.Use(Middleware(WithServiceName("my-router")))
	code := 500
	wantErr := fmt.Sprintf("%d: %s", code, http.StatusText(code))
	m.Get("/err", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, fmt.Sprintf("%d!", code), code)
	})
	r := httptest.NewRequest("GET", "/err", nil)
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	response := w.Result()
	defer response.Body.Close()
	assert.Equal(response.StatusCode, 500)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	if len(spans) < 1 {
		t.Fatalf("no spans")
	}
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal("my-router", span.Tag(ext.ServiceName))
	assert.Equal("500", span.Tag(ext.HTTPCode))
	assert.Equal(wantErr, span.Tag(ext.Error).(error).Error())
	assert.Equal("zenazn/goji.v1/web", span.Tag(ext.Component))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
}

func TestPropagation(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()
	pspan := tracer.StartSpan("test")
	tracer.Inject(pspan.Context(), tracer.HTTPHeadersCarrier(r.Header))
	m := web.New()
	m.Use(Middleware(WithServiceName("my-router")))
	m.Get("/user/:id", func(w http.ResponseWriter, r *http.Request) {
		span, ok := tracer.SpanFromContext(r.Context())
		assert.True(ok)
		assert.Equal(span.(mocktracer.Span).ParentID(), pspan.(mocktracer.Span).SpanID())
	})

	m.ServeHTTP(w, r)
	resp := w.Result()
	defer resp.Body.Close()
	assert.Equal(200, resp.StatusCode)
}

func TestOptions(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		m := web.New()
		m.Use(Middleware(opts...))
		m.Get("/user/:id", func(w http.ResponseWriter, r *http.Request) {
			_, ok := tracer.SpanFromContext(r.Context())
			assert.True(t, ok)
		})

		r := httptest.NewRequest("GET", "/user/123", nil)
		w := httptest.NewRecorder()

		m.ServeHTTP(w, r)
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

func TestNoDebugStack(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	m := web.New()
	m.Use(Middleware(NoDebugStack()))
	m.Get("/user/:id", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "failed", http.StatusInternalServerError)
	})

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	m.ServeHTTP(w, r)
	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)
	s := spans[0]
	assert.EqualError(t, s.Tags()[ext.Error].(error), "500: Internal Server Error")
	assert.Equal(t, "<debug stack disabled>", spans[0].Tags()[ext.ErrorStack])
	assert.Equal(t, "zenazn/goji.v1/web", spans[0].Tag(ext.Component))
	assert.Equal(t, ext.SpanKindServer, spans[0].Tag(ext.SpanKind))
}

func TestNamingSchema(t *testing.T) {
	genSpans := namingschematest.GenSpansFn(func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []Option
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		mux := web.New()
		mux.Use(Middleware(opts...))
		mux.Get("/200", func(w http.ResponseWriter, r *http.Request) {
			_, err := w.Write([]byte("ok"))
			require.NoError(t, err)
		})
		r := httptest.NewRequest("GET", "/200", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)

		return mt.FinishedSpans()
	})
	wantServiceNameV0 := namingschematest.ServiceNameAssertions{
		WithDefaults:             []string{"http.router"},
		WithDDService:            []string{"http.router"},
		WithDDServiceAndOverride: []string{namingschematest.TestServiceOverride},
	}
	namingschematest.NewHTTPServerTest(
		genSpans,
		"http.router",
		namingschematest.WithServiceNameAssertions(namingschema.SchemaV0, wantServiceNameV0),
	)(t)
}
