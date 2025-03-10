// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package restful

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/emicklei/go-restful"
	"github.com/stretchr/testify/assert"
)

func TestWithHeaderTags(t *testing.T) {
	setupReq := func(opts ...Option) *http.Request {
		ws := new(restful.WebService)
		ws.Filter(FilterFunc(opts...))
		ws.Route(ws.GET("/test").To(func(request *restful.Request, response *restful.Response) {
			response.Write([]byte("test"))
		}))

		container := restful.NewContainer()
		container.Add(ws)

		r := httptest.NewRequest("GET", "/test", nil)
		r.Header.Set("h!e@a-d.e*r", "val")
		r.Header.Add("h!e@a-d.e*r", "val2")
		r.Header.Set("2header", "2val")
		r.Header.Set("3header", "3val")
		w := httptest.NewRecorder()

		container.ServeHTTP(w, r)
		return r
	}

	t.Run("default-off", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		headerTags := instrumentation.NewHeaderTags([]string{"h!e@a-d.e*r", "2header", "3header"})
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
		assert.NotContains(s.Tags(), "http.headers.x-datadog-header")
	})

	t.Run("global", func(t *testing.T) {
		htArgs := []string{"3header"}
		testutils.SetGlobalHeaderTags(t, htArgs...)
		headerTags := instrumentation.NewHeaderTags(htArgs)

		mt := mocktracer.Start()
		defer mt.Stop()

		r := setupReq()
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]

		headerTags.Iter(func(header string, tag string) {
			assert.Equal(strings.Join(r.Header.Values(header), ","), s.Tags()[tag])
		})
	})

	t.Run("override", func(t *testing.T) {
		htArgsGlobal := []string{"3header"}
		testutils.SetGlobalHeaderTags(t, htArgsGlobal...)
		headerTagsGlobal := instrumentation.NewHeaderTags(htArgsGlobal)

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
		headerTagsGlobal.Iter(func(_ string, tag string) {
			assert.NotContains(s.Tags(), tag)
		})
	})
}

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
	defer response.Body.Close()
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
	assert.Equal(componentName, span.Integration())
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
	defer response.Body.Close()
	assert.Equal(response.StatusCode, 500)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal("500", span.Tag(ext.HTTPCode))
	assert.Equal(wantErr.Error(), span.Tag(ext.ErrorMsg))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
	assert.Equal("emicklei/go-restful", span.Tag(ext.Component))
	assert.Equal(componentName, span.Integration())
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
		assert.Equal(span.(mocktracer.MockspanV2Adapter).ParentID(), pspan.Context().SpanID())
	}))

	container := restful.NewContainer()
	container.Add(ws)

	container.ServeHTTP(w, r)
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
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
