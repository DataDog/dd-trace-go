// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package echo

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			setEchoHTTPConfig(t, tt.value)
			t.Setenv("DD_TRACE_RESOURCE_RENAMING_ENABLED", "true")
			httptrace.ResetCfg()

			matched := traceEchoRequest(t, http.MethodGet, "http://example.com/users/123", http.StatusOK)
			assert.Equal(t, "GET /users/:id", matched.Tag(ext.ResourceName))
			assert.Equal(t, "/users/:id", matched.Tag(ext.HTTPRoute))
			assert.Equal(t, "GET", matched.Tag(ext.HTTPMethod))
			assert.Equal(t, "http://example.com/users/123", matched.Tag(ext.HTTPURL))
			assert.Equal(t, "200", matched.Tag(ext.HTTPCode))
			assert.Nil(t, matched.Tag(ext.HTTPEndpoint))
			assert.Nil(t, matched.Tag(ext.HTTPRequestMethod))
			assert.Nil(t, matched.Tag(ext.URLPath))
			assert.Nil(t, matched.Tag(ext.HTTPResponseStatusCode))

			unmatched := traceEchoRequestWithRoute(t, http.MethodGet, "http://example.com/missing", http.StatusOK, "", "", nil)
			assert.Equal(t, "GET ", unmatched.Tag(ext.ResourceName))
			assert.Contains(t, unmatched.Tags(), ext.HTTPRoute)
			assert.Equal(t, "", unmatched.Tag(ext.HTTPRoute))
		})
	}
}

func TestOTelSemantics(t *testing.T) {
	setEchoHTTPConfig(t, "true")
	t.Setenv("DD_TRACE_CLIENT_IP_ENABLED", "true")
	httptrace.ResetCfg()

	t.Run("route and attributes", func(t *testing.T) {
		span := traceEchoRequest(t, http.MethodGet, "http://example.com/users/123?password=secret&keep=value", http.StatusOK, WithCustomTag("echo.custom", "value"))
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
		assert.Equal(t, "labstack/echo.v4", span.Tag(ext.Component))
		assert.Equal(t, string(instrumentation.PackageLabstackEchoV4), span.Integration())
		assert.Equal(t, "http.request", span.OperationName())
		assert.Equal(t, ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Equal(t, "value", span.Tag("echo.custom"))
		assert.Nil(t, span.Tag(ext.HTTPMethod))
		assert.Nil(t, span.Tag(ext.HTTPURL))
		assert.Nil(t, span.Tag(ext.HTTPCode))
		assert.Nil(t, span.Tag(ext.HTTPUserAgent))
		assert.Nil(t, span.Tag(ext.HTTPClientIP))
		assert.Nil(t, span.Tag(ext.NetworkClientIP))
	})

	t.Run("route is invariant across parameters", func(t *testing.T) {
		first := traceEchoRequest(t, http.MethodGet, "http://example.com/users/123", http.StatusOK)
		second := traceEchoRequest(t, http.MethodGet, "http://example.com/users/456", http.StatusOK)
		assert.Equal(t, "GET /users/:id", first.Tag(ext.ResourceName))
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
		{name: "not found", method: "gEt", target: "http://example.com/actual/path", wantResource: "GET", wantMethod: "GET", wantOriginal: "gEt", wantPath: "/actual/path", wantStatus: "404"},
		{name: "method not allowed", method: http.MethodPost, target: "http://example.com/users/123", routeMethod: http.MethodGet, route: "/users/:id", wantResource: "POST /users/:id", wantRoute: "/users/:id", wantMethod: "POST", wantPath: "/users/123", wantStatus: "405"},
		{name: "unknown method with route", method: "PROPFIND", target: "http://example.com/users/123", routeMethod: "PROPFIND", route: "/users/:id", wantResource: "HTTP /users/:id", wantRoute: "/users/:id", wantMethod: "_OTHER", wantOriginal: "PROPFIND", wantPath: "/users/123", wantStatus: "200"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			span := traceEchoRequestWithRoute(t, tt.method, tt.target, http.StatusOK, tt.routeMethod, tt.route, nil)
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
	setEchoHTTPConfig(t, "true")

	for _, tt := range []struct {
		name          string
		status        int
		isStatusError func(int) bool
		errCheck      func(error) bool
		wantErrorType any
	}{
		{name: "success", status: http.StatusOK},
		{name: "client error", status: http.StatusBadRequest},
		{name: "server error", status: http.StatusInternalServerError, wantErrorType: "500"},
		{name: "custom client error inclusion", status: http.StatusBadRequest, isStatusError: func(status int) bool { return status == http.StatusBadRequest }, wantErrorType: "400"},
		{name: "custom success inclusion", status: http.StatusCreated, isStatusError: func(status int) bool { return status == http.StatusCreated }, wantErrorType: "201"},
		{name: "custom exclusion", status: http.StatusInternalServerError, isStatusError: func(int) bool { return false }},
		{name: "error check exclusion", status: http.StatusInternalServerError, errCheck: func(error) bool { return false }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.isStatusError != nil {
				opts = append(opts, WithStatusCheck(tt.isStatusError))
			}
			if tt.errCheck != nil {
				opts = append(opts, WithErrorCheck(tt.errCheck))
			}
			span := traceEchoRequest(t, http.MethodGet, "http://example.com/users/123", tt.status, opts...)
			assert.Equal(t, tt.wantErrorType, span.Tag(ext.ErrorType))
		})
	}
}

func TestOTelSemanticsErrors(t *testing.T) {
	setEchoHTTPConfig(t, "true")
	responseErr := errors.New("oh no")

	t.Run("retained real error", func(t *testing.T) {
		span := traceEchoError(t, responseErr)
		require.NotNil(t, span.Tag(ext.ErrorType))
		assert.NotEqual(t, "500", span.Tag(ext.ErrorType))
		assert.Equal(t, responseErr.Error(), span.Tag(ext.ErrorMsg))
		assert.Equal(t, "500", span.Tag(ext.HTTPResponseStatusCode))
	})

	t.Run("ignored real error", func(t *testing.T) {
		span := traceEchoError(t, responseErr, WithErrorCheck(func(error) bool { return false }))
		assert.Nil(t, span.Tag(ext.ErrorType))
		assert.Nil(t, span.Tag(ext.ErrorMsg))
	})

	t.Run("translator and status inclusion", func(t *testing.T) {
		err := &testCustomError{TestCode: http.StatusBadRequest}
		span := traceEchoError(t, err,
			WithErrorTranslator(func(err error) (*echo.HTTPError, bool) {
				return echo.NewHTTPError(err.(*testCustomError).TestCode), true
			}),
			WithStatusCheck(func(status int) bool { return status == http.StatusBadRequest }),
		)
		require.NotNil(t, span.Tag(ext.ErrorType))
		assert.NotEqual(t, "400", span.Tag(ext.ErrorType))
		assert.Equal(t, "400", span.Tag(ext.HTTPResponseStatusCode))
	})

	t.Run("no debug stack", func(t *testing.T) {
		span := traceEchoError(t, responseErr, NoDebugStack())
		assert.Empty(t, span.Tag(ext.ErrorStack))
		assert.Equal(t, responseErr.Error(), span.Tag(ext.ErrorMsg))
	})
}

func TestOTelSemanticsContextPropagationAndWrap(t *testing.T) {
	setEchoHTTPConfig(t, "true")
	mt := mocktracer.Start()
	defer mt.Stop()

	var handlerSpan *tracer.Span
	router := Wrap(echo.New(), WithService("semantic-service"))
	router.GET("/users/:id", func(c echo.Context) error {
		handlerSpan, _ = tracer.SpanFromContext(c.Request().Context())
		return c.NoContent(http.StatusOK)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/123", nil))

	require.NotNil(t, handlerSpan)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /users/:id", spans[0].Tag(ext.ResourceName))
}

func TestOTelSemanticsAppSecRouteParams(t *testing.T) {
	setEchoHTTPConfig(t, "true")
	testutils.StartAppSec(t)
	httptrace.ResetCfg()

	mt := mocktracer.Start()
	defer mt.Stop()
	router := Wrap(echo.New(), WithService("semantic-service"))
	router.GET("/users/:id", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
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

func traceEchoRequest(t *testing.T, method, target string, status int, opts ...Option) *mocktracer.Span {
	t.Helper()
	return traceEchoRequestWithRoute(t, method, target, status, method, "/users/:id", nil, opts...)
}

func traceEchoRequestWithRoute(t *testing.T, method, target string, status int, routeMethod, route string, handler echo.HandlerFunc, opts ...Option) *mocktracer.Span {
	t.Helper()
	mt := mocktracer.Start()
	defer mt.Stop()

	router := echo.New()
	router.Use(Middleware(append([]Option{WithService("semantic-service")}, opts...)...))
	if route != "" {
		if handler == nil {
			handler = func(c echo.Context) error { return c.NoContent(status) }
		}
		router.Add(routeMethod, route, handler)
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

func traceEchoError(t *testing.T, responseErr error, opts ...Option) *mocktracer.Span {
	t.Helper()
	return traceEchoRequestWithRoute(t, http.MethodGet, "http://example.com/error", http.StatusOK, http.MethodGet, "/error", func(echo.Context) error {
		return responseErr
	}, opts...)
}

func setEchoHTTPConfig(t *testing.T, otel string) {
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
