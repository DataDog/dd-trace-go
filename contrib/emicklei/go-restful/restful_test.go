// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package restful

import (
	"errors"
	"math"
	"net/http/httptest"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

	"github.com/emicklei/go-restful"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrace200(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	ws := new(restful.WebService)
	ws.Filter(FilterFunc(WithServiceName("my-service")))
	ws.Route(ws.GET("/user/{id}").Param(restful.PathParameter("id", "user ID")).
		To(func(request *restful.Request, response *restful.Response) {
			_, ok := tracer.SpanFromContext(request.Request.Context())
			assert.True(ok)
			id := request.PathParameter("id")
			response.Write([]byte(id))
		}))

	container := restful.NewContainer()
	container.Add(ws)

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	container.ServeHTTP(w, r)
	response := w.Result()
	assert.Equal(response.StatusCode, 200)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
	assert.Contains(span.Tag(ext.ResourceName), "/user/{id}")
	assert.Equal("my-service", span.Tag(ext.ServiceName))
	assert.Equal("200", span.Tag(ext.HTTPCode))
	assert.Equal("GET", span.Tag(ext.HTTPMethod))
	assert.Equal("http://example.com/user/123", span.Tag(ext.HTTPURL))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
	assert.Equal("emicklei/go-restful", span.Tag(ext.Component))
}

func TestError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	wantErr := errors.New("oh no")

	ws := new(restful.WebService)
	ws.Filter(FilterFunc())
	ws.Route(ws.GET("/err").To(func(request *restful.Request, response *restful.Response) {
		response.WriteError(500, wantErr)
	}))

	container := restful.NewContainer()
	container.Add(ws)

	r := httptest.NewRequest("GET", "/err", nil)
	w := httptest.NewRecorder()

	container.ServeHTTP(w, r)
	response := w.Result()
	assert.Equal(response.StatusCode, 500)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal("500", span.Tag(ext.HTTPCode))
	assert.Equal(wantErr.Error(), span.Tag(ext.Error).(error).Error())
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
	assert.Equal("emicklei/go-restful", span.Tag(ext.Component))
}

func TestPropagation(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	pspan := tracer.StartSpan("test")
	tracer.Inject(pspan.Context(), tracer.HTTPHeadersCarrier(r.Header))

	ws := new(restful.WebService)
	ws.Filter(FilterFunc())
	ws.Route(ws.GET("/user/{id}").To(func(request *restful.Request, response *restful.Response) {
		span, ok := tracer.SpanFromContext(request.Request.Context())
		assert.True(ok)
		assert.Equal(span.(mocktracer.Span).ParentID(), pspan.(mocktracer.Span).SpanID())
	}))

	container := restful.NewContainer()
	container.Add(ws)

	container.ServeHTTP(w, r)
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate float64, opts ...Option) {
		ws := new(restful.WebService)
		ws.Filter(FilterFunc(opts...))
		ws.Route(ws.GET("/user/{id}").To(func(request *restful.Request, response *restful.Response) {}))

		container := restful.NewContainer()
		container.Add(ws)
		r := httptest.NewRequest("GET", "/user/123", nil)
		w := httptest.NewRecorder()
		container.ServeHTTP(w, r)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		s := spans[0]
		if !math.IsNaN(rate) {
			assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
		}
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, globalconfig.AnalyticsRate())
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

		assertRate(t, mt, math.NaN(), WithAnalytics(false))
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

func TestNamingSchema(t *testing.T) {
	genSpans := namingschematest.GenSpansFn(func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []Option
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		ws := new(restful.WebService)
		ws.Filter(FilterFunc(opts...))
		ws.Route(ws.GET("/user/{id}").Param(restful.PathParameter("id", "user ID")).
			To(func(request *restful.Request, response *restful.Response) {
				_, err := response.Write([]byte(request.PathParameter("id")))
				require.NoError(t, err)
			}))
		container := restful.NewContainer()
		container.Add(ws)

		r := httptest.NewRequest("GET", "/user/200", nil)
		w := httptest.NewRecorder()
		container.ServeHTTP(w, r)

		return mt.FinishedSpans()
	})
	wantServiceNameV0 := namingschematest.ServiceNameAssertions{
		WithDefaults:             []string{"go-restful"},
		WithDDService:            []string{"go-restful"},
		WithDDServiceAndOverride: []string{namingschematest.TestServiceOverride},
	}
	namingschematest.NewHTTPServerTest(
		genSpans,
		"go-restful",
		namingschematest.WithServiceNameAssertions(namingschema.SchemaV0, wantServiceNameV0),
	)(t)
}
