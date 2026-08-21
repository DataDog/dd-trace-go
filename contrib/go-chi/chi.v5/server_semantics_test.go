// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
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

	gochi "github.com/go-chi/chi/v5"
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

	span := traceChiRequest(t, http.MethodGet, "http://example.com/no_such_route_xyz", http.StatusOK, http.MethodGet, "/registered", nil)
	assert.Equal(t, "GET", span.Tag(ext.ResourceName))
	assert.NotContains(t, span.Tags(), ext.HTTPRoute)
	assert.NotEmpty(t, span.Tag(ext.HTTPEndpoint))
}

func TestOTelSemanticsFinalRoutePattern(t *testing.T) {
	setChiHTTPConfig(t, "true")
	mt := mocktracer.Start()
	defer mt.Stop()

	var initialResource any
	router := gochi.NewRouter()
	router.Use(Middleware())
	router.Route("/api", func(r gochi.Router) {
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
	router := gochi.NewRouter()
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
	router := gochi.NewRouter().With(Middleware())
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

	router := gochi.NewRouter()
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
