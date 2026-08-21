// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func TestDatadogSemantics(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value string
	}{
		{name: "unset"},
		{name: "disabled", value: "false"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setGinHTTPConfig(t, tt.value)
			t.Setenv("DD_TRACE_RESOURCE_RENAMING_ENABLED", "true")
			httptrace.ResetCfg()

			matched := traceGinRequest(t, http.MethodGet, "http://example.com/users/123", http.StatusOK)
			assert.Equal(t, "GET /users/:id", matched.Tag(ext.ResourceName))
			assert.Equal(t, "/users/:id", matched.Tag(ext.HTTPRoute))
			assert.Equal(t, "GET", matched.Tag(ext.HTTPMethod))
			assert.Equal(t, "http://example.com/users/123", matched.Tag(ext.HTTPURL))
			assert.Equal(t, "200", matched.Tag(ext.HTTPCode))
			assert.Nil(t, matched.Tag(ext.HTTPEndpoint))
			assert.Nil(t, matched.Tag(ext.HTTPRequestMethod))
			assert.Nil(t, matched.Tag(ext.URLPath))
			assert.Nil(t, matched.Tag(ext.HTTPResponseStatusCode))

			unmatched := traceGinRequestWithRoute(t, http.MethodGet, "http://example.com/missing", http.StatusOK, "", "", nil)
			assert.Equal(t, "GET ", unmatched.Tag(ext.ResourceName))
			assert.Contains(t, unmatched.Tags(), ext.HTTPRoute)
			assert.Equal(t, "", unmatched.Tag(ext.HTTPRoute))
		})
	}
}

func TestDatadogSemanticsPreserveGinErrorLifecycle(t *testing.T) {
	responseErr := errors.New("oh no")
	for _, tt := range []struct {
		name  string
		value string
	}{
		{name: "unset"},
		{name: "disabled", value: "false"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setGinHTTPConfig(t, tt.value)
			calls := 0
			span := traceGinError(t, http.StatusInternalServerError, responseErr, WithUseGinErrors(), WithStatusCheck(func(int) bool {
				calls++
				return true
			}))

			assert.Equal(t, 3, calls)
			assert.Equal(t, "500", span.Tag(ext.HTTPCode))
			assert.Equal(t, "Error #01: oh no\n", span.Tag("gin.errors"))
			assert.Equal(t, "Error #01: oh no\n", span.Tag(ext.ErrorMsg))
		})
	}
}

func TestOTelSemantics(t *testing.T) {
	setGinHTTPConfig(t, "true")
	t.Setenv("DD_TRACE_CLIENT_IP_ENABLED", "true")
	httptrace.ResetCfg()

	t.Run("route and attributes", func(t *testing.T) {
		span := traceGinRequest(t, http.MethodGet, "http://example.com/users/123?password=secret&keep=value", http.StatusOK)
		assert.Equal(t, "GET /users/:id", span.Tag(ext.ResourceName))
		assert.Equal(t, "/users/:id", span.Tag(ext.HTTPRoute))
		assert.Equal(t, "GET", span.Tag(ext.HTTPRequestMethod))
		assert.Nil(t, span.Tag(ext.HTTPRequestMethodOriginal))
		assert.Equal(t, "/users/123", span.Tag(ext.URLPath))
		assert.Equal(t, "http", span.Tag(ext.URLScheme))
		assert.Equal(t, "<redacted>&keep=value", span.Tag(ext.URLQuery))
		assert.Equal(t, "example.com", span.Tag(ext.ServerAddress))
		assert.Equal(t, "semantic-agent", span.Tag(ext.UserAgentOriginal))
		assert.Equal(t, "203.0.113.10", span.Tag(ext.ClientAddress))
		assert.Equal(t, "192.0.2.1", span.Tag(ext.NetworkPeerAddress))
		assert.Equal(t, "200", span.Tag(ext.HTTPResponseStatusCode))
		assert.Equal(t, "semantic-service", span.Tag(ext.ServiceName))
		assert.Equal(t, ext.SpanKindServer, span.Tag(ext.SpanKind))
		assert.Equal(t, "gin-gonic/gin", span.Tag(ext.Component))
		assert.Equal(t, string(instrumentation.PackageGin), span.Integration())
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
		first := traceGinRequest(t, http.MethodGet, "http://example.com/users/123", http.StatusOK)
		second := traceGinRequest(t, http.MethodGet, "http://example.com/users/456", http.StatusOK)
		assert.Equal(t, "GET /users/:id", first.Tag(ext.ResourceName))
		assert.Equal(t, first.Tag(ext.ResourceName), second.Tag(ext.ResourceName))
	})

	for _, tt := range []struct {
		name         string
		method       string
		target       string
		routeMethod  string
		route        string
		methodNotOK  bool
		wantResource string
		wantRoute    any
		wantMethod   string
		wantOriginal any
		wantPath     string
		wantStatus   string
	}{
		{name: "not found", method: "gEt", target: "http://example.com/actual/path", wantResource: "GET", wantMethod: "GET", wantOriginal: "gEt", wantPath: "/actual/path", wantStatus: "404"},
		{name: "method not allowed", method: http.MethodPost, target: "http://example.com/users/123", routeMethod: http.MethodGet, route: "/users/:id", methodNotOK: true, wantResource: "POST", wantMethod: "POST", wantPath: "/users/123", wantStatus: "405"},
		{name: "unknown method with route", method: "PROPFIND", target: "http://example.com/users/123", routeMethod: "PROPFIND", route: "/users/:id", wantResource: "HTTP /users/:id", wantRoute: "/users/:id", wantMethod: "_OTHER", wantOriginal: "PROPFIND", wantPath: "/users/123", wantStatus: "200"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			span := traceGinRequestWithRoute(t, tt.method, tt.target, http.StatusOK, tt.routeMethod, tt.route, func(router *gin.Engine) {
				router.HandleMethodNotAllowed = tt.methodNotOK
			})
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

func TestOTelSemanticsStatus(t *testing.T) {
	setGinHTTPConfig(t, "true")

	for _, tt := range []struct {
		name          string
		status        int
		isStatusError func(int) bool
		wantErrorType any
	}{
		{name: "success", status: http.StatusOK},
		{name: "client error", status: http.StatusBadRequest},
		{name: "server error", status: http.StatusInternalServerError, wantErrorType: "500"},
		{name: "custom inclusion", status: http.StatusBadRequest, isStatusError: func(status int) bool { return status == http.StatusBadRequest }, wantErrorType: "400"},
		{name: "custom exclusion", status: http.StatusInternalServerError, isStatusError: func(int) bool { return false }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.isStatusError != nil {
				opts = append(opts, WithStatusCheck(tt.isStatusError))
			}
			span := traceGinRequest(t, http.MethodGet, "http://example.com/users/123", tt.status, opts...)
			assert.Equal(t, tt.wantErrorType, span.Tag(ext.ErrorType))
		})
	}
}

func TestOTelSemanticsGinErrors(t *testing.T) {
	setGinHTTPConfig(t, "true")
	responseErr := errors.New("oh no")

	t.Run("status-only failure", func(t *testing.T) {
		span := traceGinError(t, http.StatusInternalServerError, nil, WithUseGinErrors())
		assert.Equal(t, "500", span.Tag(ext.ErrorType))
		assert.Nil(t, span.Tag("gin.errors"))
	})

	t.Run("retained Gin error", func(t *testing.T) {
		span := traceGinError(t, http.StatusInternalServerError, responseErr, WithUseGinErrors())
		require.NotNil(t, span.Tag(ext.ErrorType))
		assert.NotEqual(t, "500", span.Tag(ext.ErrorType))
		assert.Contains(t, span.Tag("gin.errors"), "oh no")
		assert.Contains(t, span.Tag(ext.ErrorMsg), "oh no")
	})

	t.Run("custom exclusion", func(t *testing.T) {
		span := traceGinError(t, http.StatusInternalServerError, responseErr, WithUseGinErrors(), WithStatusCheck(func(int) bool { return false }))
		assert.Nil(t, span.Tag(ext.ErrorType))
		assert.Contains(t, span.Tag("gin.errors"), "oh no")
	})

	t.Run("custom inclusion", func(t *testing.T) {
		span := traceGinError(t, http.StatusBadRequest, nil, WithUseGinErrors(), WithStatusCheck(func(status int) bool { return status == http.StatusBadRequest }))
		assert.Equal(t, "400", span.Tag(ext.ErrorType))
	})

	t.Run("status check is evaluated once", func(t *testing.T) {
		calls := 0
		span := traceGinError(t, http.StatusInternalServerError, responseErr, WithUseGinErrors(), WithStatusCheck(func(int) bool {
			calls++
			return true
		}))
		assert.Equal(t, 1, calls)
		assert.NotNil(t, span.Tag(ext.ErrorType))
	})
}

func TestOTelSemanticsResourceNamer(t *testing.T) {
	setGinHTTPConfig(t, "true")

	span := traceGinRequest(t, http.MethodGet, "http://example.com/users/123", http.StatusOK, WithResourceNamer(func(*gin.Context) string {
		return "custom-resource"
	}))
	assert.Equal(t, "custom-resource", span.Tag(ext.ResourceName))
	assert.Equal(t, "/users/:id", span.Tag(ext.HTTPRoute))
}

func TestOTelSemanticsContextPropagation(t *testing.T) {
	setGinHTTPConfig(t, "true")
	mt := mocktracer.Start()
	defer mt.Stop()

	var handlerSpan *tracer.Span
	router := gin.New()
	router.Use(Middleware("semantic-service"))
	router.GET("/users/:id", func(c *gin.Context) {
		handlerSpan, _ = tracer.SpanFromContext(c.Request.Context())
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/123", nil))

	require.NotNil(t, handlerSpan)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /users/:id", spans[0].Tag(ext.ResourceName))
}

func TestOTelSemanticsAppSecRouteParams(t *testing.T) {
	setGinHTTPConfig(t, "true")
	testutils.StartAppSec(t)
	httptrace.ResetCfg()

	mt := mocktracer.Start()
	defer mt.Stop()
	router := gin.New()
	router.Use(Middleware("semantic-service"))
	router.GET("/users/:id", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/users/appscan_fingerprint", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	router.ServeHTTP(httptest.NewRecorder(), req)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /users/:id", spans[0].Tag(ext.ResourceName))
	assert.Equal(t, "/users/:id", spans[0].Tag(ext.HTTPRoute))
	assert.Equal(t, "/users/:id", spans[0].Tag(ext.HTTPEndpoint))
	assert.Equal(t, "203.0.113.10", spans[0].Tag(ext.ClientAddress))
	assert.Equal(t, "192.0.2.1", spans[0].Tag(ext.NetworkPeerAddress))
	assert.Nil(t, spans[0].Tag(ext.HTTPClientIP))
	assert.Nil(t, spans[0].Tag(ext.NetworkClientIP))
	event, ok := spans[0].Tag("_dd.appsec.json").(string)
	require.True(t, ok)
	assert.Contains(t, event, "server.request.path_params")
	assert.Contains(t, event, "appscan_fingerprint")
}

func traceGinRequest(t *testing.T, method, target string, status int, opts ...Option) *mocktracer.Span {
	t.Helper()
	return traceGinRequestWithRoute(t, method, target, status, method, "/users/:id", nil, opts...)
}

func traceGinRequestWithRoute(t *testing.T, method, target string, status int, routeMethod, route string, configure func(*gin.Engine), opts ...Option) *mocktracer.Span {
	t.Helper()
	mt := mocktracer.Start()
	defer mt.Stop()

	router := gin.New()
	router.Use(Middleware("semantic-service", opts...))
	if configure != nil {
		configure(router)
	}
	if route != "" {
		router.Handle(routeMethod, route, func(c *gin.Context) {
			c.Status(status)
		})
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

func traceGinError(t *testing.T, status int, responseErr error, opts ...Option) *mocktracer.Span {
	t.Helper()
	mt := mocktracer.Start()
	defer mt.Stop()

	router := gin.New()
	router.Use(Middleware("semantic-service", opts...))
	router.GET("/error", func(c *gin.Context) {
		if responseErr != nil {
			_ = c.Error(responseErr)
		}
		c.Status(status)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/error", nil))

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	return spans[0]
}

func setGinHTTPConfig(t *testing.T, otel string) {
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
