// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package restful

import (
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/emicklei/go-restful/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func TestWithHeaderTags(t *testing.T) {
	setupReq := func(opts ...Option) *http.Request {
		ws := new(restful.WebService)
		ws.Filter(FilterFunc(opts...))
		ws.Route(ws.GET("/test").To(func(_ *restful.Request, response *restful.Response) {
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

		testutils.SetGlobalHeaderTags(t, "3header", "other")

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

func TestTrace200(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	ws := new(restful.WebService)
	ws.Filter(FilterFunc(WithService("my-service")))
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
	assert.Equal("/user/{id}", span.Tag(ext.ResourceName))
	assert.Equal("my-service", span.Tag(ext.ServiceName))
	assert.Equal("200", span.Tag(ext.HTTPCode))
	assert.Equal("GET", span.Tag(ext.HTTPMethod))
	assert.Equal("http://example.com/user/123", span.Tag(ext.HTTPURL))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
	assert.Equal("emicklei/go-restful.v3", span.Tag(ext.Component))
	assert.Equal(componentName, span.Integration())
	assert.Equal("/user/{id}", span.Tag(ext.HTTPRoute))
}

func TestError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	wantErr := errors.New("oh no")

	ws := new(restful.WebService)
	ws.Filter(FilterFunc())
	ws.Route(ws.GET("/err").To(func(_ *restful.Request, response *restful.Response) {
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
	assert.Equal("emicklei/go-restful.v3", span.Tag(ext.Component))
	assert.Equal(componentName, span.Integration())
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
			setHTTPConfig(t, tt.value, "")
			span := traceRoute(t, http.MethodGet, "/user/123", http.StatusOK, nil)
			assert.Equal(t, "/user/{id}", span.Tag(ext.ResourceName))
			assert.Equal(t, "/user/{id}", span.Tag(ext.HTTPRoute))
			assert.Equal(t, "GET", span.Tag(ext.HTTPMethod))
			assert.Equal(t, "http://example.com/user/123", span.Tag(ext.HTTPURL))
			assert.Equal(t, "200", span.Tag(ext.HTTPCode))
			assert.Nil(t, span.Tag(ext.HTTPRequestMethod))
			assert.Nil(t, span.Tag(ext.URLPath))
			assert.Nil(t, span.Tag(ext.HTTPResponseStatusCode))
		})
	}
}

func TestOTelSemantics(t *testing.T) {
	setHTTPConfig(t, "true", "")
	t.Setenv("DD_TRACE_CLIENT_IP_ENABLED", "true")
	httptrace.ResetCfg()

	t.Run("route and attributes", func(t *testing.T) {
		span := traceRoute(t, "GET", "/user/123?password=secret&keep=value", http.StatusOK, nil)
		assert.Equal(t, "GET /user/{id}", span.Tag(ext.ResourceName))
		assert.Equal(t, "/user/{id}", span.Tag(ext.HTTPRoute))
		assert.Equal(t, "GET", span.Tag(ext.HTTPRequestMethod))
		assert.Equal(t, "/user/123", span.Tag(ext.URLPath))
		assert.Equal(t, "http", span.Tag(ext.URLScheme))
		assert.Equal(t, "<redacted>&keep=value", span.Tag(ext.URLQuery))
		assert.Equal(t, "example.com", span.Tag(ext.ServerAddress))
		assert.Equal(t, "semantic-agent", span.Tag(ext.UserAgentOriginal))
		assert.Equal(t, "203.0.113.10", span.Tag(ext.ClientAddress))
		assert.Equal(t, "192.0.2.1", span.Tag(ext.NetworkPeerAddress))
		assert.Equal(t, "200", span.Tag(ext.HTTPResponseStatusCode))
		assert.Equal(t, ext.SpanKindServer, span.Tag(ext.SpanKind))
		assert.Equal(t, componentName, span.Tag(ext.Component))
		assert.Equal(t, componentName, span.Integration())
		assert.Equal(t, "http.request", span.OperationName())
		assert.Equal(t, ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Nil(t, span.Tag(ext.HTTPMethod))
		assert.Nil(t, span.Tag(ext.HTTPURL))
		assert.Nil(t, span.Tag(ext.HTTPCode))
		assert.Nil(t, span.Tag(ext.HTTPUserAgent))
		assert.Nil(t, span.Tag(ext.HTTPClientIP))
		assert.Nil(t, span.Tag(ext.NetworkClientIP))
	})

	t.Run("route is invariant across parameters", func(t *testing.T) {
		first := traceRoute(t, http.MethodGet, "/user/123", http.StatusOK, nil)
		second := traceRoute(t, http.MethodGet, "/user/456", http.StatusOK, nil)
		assert.Equal(t, "GET /user/{id}", first.Tag(ext.ResourceName))
		assert.Equal(t, first.Tag(ext.ResourceName), second.Tag(ext.ResourceName))
	})

	for _, tt := range []struct {
		name         string
		method       string
		wantResource string
		wantMethod   string
		wantOriginal any
	}{
		{name: "case variant", method: "gEt", wantResource: "GET", wantMethod: "GET", wantOriginal: "gEt"},
		{name: "unknown", method: "PROPFIND", wantResource: "HTTP", wantMethod: "_OTHER", wantOriginal: "PROPFIND"},
	} {
		t.Run(tt.name+" without route", func(t *testing.T) {
			span := traceWithoutRoute(t, tt.method, "/actual/path")
			assert.Equal(t, tt.wantResource, span.Tag(ext.ResourceName))
			assert.Nil(t, span.Tag(ext.HTTPRoute))
			assert.Equal(t, tt.wantMethod, span.Tag(ext.HTTPRequestMethod))
			assert.Equal(t, tt.wantOriginal, span.Tag(ext.HTTPRequestMethodOriginal))
			assert.Equal(t, "/actual/path", span.Tag(ext.URLPath))
		})
	}
}

func TestOTelSemanticsStatus(t *testing.T) {
	for _, tt := range []struct {
		name          string
		status        int
		statuses      string
		responseError error
		wantErrorType any
	}{
		{name: "client error", status: http.StatusBadRequest},
		{name: "server error", status: http.StatusInternalServerError, wantErrorType: "500"},
		{name: "custom inclusion", status: http.StatusBadRequest, statuses: "400", wantErrorType: "400"},
		{name: "custom exclusion", status: http.StatusInternalServerError, statuses: "400-499"},
		{name: "real error", status: http.StatusInternalServerError, responseError: errors.New("oh no"), wantErrorType: "*errors.errorString"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setHTTPConfig(t, "true", tt.statuses)
			span := traceRoute(t, http.MethodGet, "/user/123", tt.status, tt.responseError)
			assert.Equal(t, tt.wantErrorType, span.Tag(ext.ErrorType))
			if tt.responseError != nil {
				assert.Equal(t, tt.responseError.Error(), span.Tag(ext.ErrorMsg))
			}
		})
	}
}

func traceRoute(t *testing.T, method, target string, status int, responseError error) *mocktracer.Span {
	t.Helper()
	mt := mocktracer.Start()
	defer mt.Stop()

	ws := new(restful.WebService)
	ws.Filter(FilterFunc(WithService("semantic-service")))
	ws.Route(ws.GET("/user/{id}").To(func(_ *restful.Request, response *restful.Response) {
		if responseError != nil {
			response.WriteError(status, responseError)
			return
		}
		response.WriteHeader(status)
	}))
	container := restful.NewContainer()
	container.Add(ws)

	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = "192.0.2.1:1234"
	r.Header.Set("User-Agent", "semantic-agent")
	r.Header.Set("X-Forwarded-For", "203.0.113.10")
	container.ServeHTTP(httptest.NewRecorder(), r)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	return spans[0]
}

func traceWithoutRoute(t *testing.T, method, target string) *mocktracer.Span {
	t.Helper()
	mt := mocktracer.Start()
	defer mt.Stop()

	ws := new(restful.WebService)
	ws.Route(ws.GET("/known").To(func(*restful.Request, *restful.Response) {}))
	container := restful.NewContainer()
	container.Add(ws)
	container.Filter(FilterFunc())
	container.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, target, nil))

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	return spans[0]
}

func setHTTPConfig(t *testing.T, otel, statuses string) {
	t.Helper()
	oldOTel, hadOTel := os.LookupEnv("DD_TRACE_OTEL_SEMANTICS_ENABLED")
	oldStatuses, hadStatuses := os.LookupEnv("DD_TRACE_HTTP_SERVER_ERROR_STATUSES")
	if otel == "" {
		require.NoError(t, os.Unsetenv("DD_TRACE_OTEL_SEMANTICS_ENABLED"))
	} else {
		require.NoError(t, os.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", otel))
	}
	if statuses == "" {
		require.NoError(t, os.Unsetenv("DD_TRACE_HTTP_SERVER_ERROR_STATUSES"))
	} else {
		require.NoError(t, os.Setenv("DD_TRACE_HTTP_SERVER_ERROR_STATUSES", statuses))
	}
	require.NoError(t, tracer.Start(tracer.WithTraceEnabled(false)))
	httptrace.ResetCfg()
	t.Cleanup(func() {
		if hadOTel {
			require.NoError(t, os.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", oldOTel))
		} else {
			require.NoError(t, os.Unsetenv("DD_TRACE_OTEL_SEMANTICS_ENABLED"))
		}
		if hadStatuses {
			require.NoError(t, os.Setenv("DD_TRACE_HTTP_SERVER_ERROR_STATUSES", oldStatuses))
		} else {
			require.NoError(t, os.Unsetenv("DD_TRACE_HTTP_SERVER_ERROR_STATUSES"))
		}
		require.NoError(t, tracer.Start(tracer.WithTraceEnabled(false)))
		httptrace.ResetCfg()
		tracer.Stop()
	})
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
	ws.Route(ws.GET("/user/{id}").To(func(request *restful.Request, _ *restful.Response) {
		span, ok := tracer.SpanFromContext(request.Request.Context())
		assert.True(ok)
		assert.Equal(mocktracer.MockSpan(span).ParentID(), mocktracer.MockSpan(pspan).SpanID())
	}))

	container := restful.NewContainer()
	container.Add(ws)

	container.ServeHTTP(w, r)
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate float64, opts ...Option) {
		ws := new(restful.WebService)
		ws.Filter(FilterFunc(opts...))
		ws.Route(ws.GET("/user/{id}").To(func(_ *restful.Request, _ *restful.Response) {}))

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

		assertRate(t, mt, math.NaN())
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

		assertRate(t, mt, math.NaN(), WithAnalytics(false))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		testutils.SetGlobalAnalyticsRate(t, 0.4)
		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})
}
