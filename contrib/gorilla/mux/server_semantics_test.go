// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mux

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

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
			setMuxHTTPConfig(t, tt.value)

			matched := traceMuxRequest(t, http.MethodGet, "http://example.com/users/123", http.StatusOK)
			assert.Equal(t, "GET /users/{id}", matched.Tag(ext.ResourceName))
			assert.Equal(t, "/users/{id}", matched.Tag(ext.HTTPRoute))
			assert.Equal(t, "GET", matched.Tag(ext.HTTPMethod))
			assert.Equal(t, "http://example.com/users/123", matched.Tag(ext.HTTPURL))
			assert.Equal(t, "200", matched.Tag(ext.HTTPCode))
			assert.Nil(t, matched.Tag(ext.HTTPRequestMethod))
			assert.Nil(t, matched.Tag(ext.URLPath))
			assert.Nil(t, matched.Tag(ext.HTTPResponseStatusCode))

			unmatched := traceMuxRequest(t, http.MethodGet, "http://example.com/missing", http.StatusOK)
			assert.Equal(t, "GET unknown", unmatched.Tag(ext.ResourceName))
			assert.Nil(t, unmatched.Tag(ext.HTTPRoute))
		})
	}
}

func TestOTelSemantics(t *testing.T) {
	setMuxHTTPConfig(t, "true")
	t.Setenv("DD_TRACE_CLIENT_IP_ENABLED", "true")
	httptrace.ResetCfg()

	t.Run("route and attributes", func(t *testing.T) {
		span := traceMuxRequest(t, "gEt", "http://example.com/users/123?password=secret&keep=value", http.StatusOK)
		assert.Equal(t, "GET /users/{id}", span.Tag(ext.ResourceName))
		assert.Equal(t, "/users/{id}", span.Tag(ext.HTTPRoute))
		assert.Equal(t, "GET", span.Tag(ext.HTTPRequestMethod))
		assert.Equal(t, "gEt", span.Tag(ext.HTTPRequestMethodOriginal))
		assert.Equal(t, "/users/123", span.Tag(ext.URLPath))
		assert.Equal(t, "http", span.Tag(ext.URLScheme))
		assert.Equal(t, "<redacted>&keep=value", span.Tag(ext.URLQuery))
		assert.Equal(t, "example.com", span.Tag(ext.ServerAddress))
		assert.Equal(t, "semantic-agent", span.Tag(ext.UserAgentOriginal))
		assert.Equal(t, "203.0.113.10", span.Tag(ext.ClientAddress))
		assert.Equal(t, "192.0.2.1", span.Tag(ext.NetworkPeerAddress))
		assert.Equal(t, "200", span.Tag(ext.HTTPResponseStatusCode))
		assert.Equal(t, "example.com", span.Tag("mux.host"))
		assert.Equal(t, "semantic-service", span.Tag(ext.ServiceName))
		assert.Equal(t, ext.SpanKindServer, span.Tag(ext.SpanKind))
		assert.Equal(t, "gorilla/mux", span.Tag(ext.Component))
		assert.Equal(t, string(instrumentation.PackageGorillaMux), span.Integration())
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
		first := traceMuxRequest(t, http.MethodGet, "http://example.com/users/123", http.StatusOK)
		second := traceMuxRequest(t, http.MethodGet, "http://example.com/users/456", http.StatusOK)
		assert.Equal(t, "GET /users/{id}", first.Tag(ext.ResourceName))
		assert.Equal(t, first.Tag(ext.ResourceName), second.Tag(ext.ResourceName))
	})

	for _, tt := range []struct {
		name         string
		method       string
		target       string
		methods      []string
		wantResource string
		wantMethod   string
		wantOriginal any
		wantPath     string
		wantStatus   string
	}{
		{name: "not found", method: "gEt", target: "http://example.com/actual/path", wantResource: "GET", wantMethod: "GET", wantOriginal: "gEt", wantPath: "/actual/path", wantStatus: "404"},
		{name: "method not allowed", method: http.MethodPost, target: "http://example.com/users/123", methods: []string{http.MethodGet}, wantResource: "POST", wantMethod: "POST", wantPath: "/users/123", wantStatus: "405"},
		{name: "unknown method", method: "PROPFIND", target: "http://example.com/actual/path", wantResource: "HTTP", wantMethod: "_OTHER", wantOriginal: "PROPFIND", wantPath: "/actual/path", wantStatus: "404"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			span := traceMuxRequestWithMethods(t, tt.method, tt.target, http.StatusOK, tt.methods)
			assert.Equal(t, tt.wantResource, span.Tag(ext.ResourceName))
			assert.Nil(t, span.Tag(ext.HTTPRoute))
			assert.Equal(t, tt.wantMethod, span.Tag(ext.HTTPRequestMethod))
			assert.Equal(t, tt.wantOriginal, span.Tag(ext.HTTPRequestMethodOriginal))
			assert.Equal(t, tt.wantPath, span.Tag(ext.URLPath))
			assert.Equal(t, tt.wantStatus, span.Tag(ext.HTTPResponseStatusCode))
		})
	}
}

func TestOTelSemanticsStatus(t *testing.T) {
	setMuxHTTPConfig(t, "true")

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
			var opts []RouterOption
			if tt.isStatusError != nil {
				opts = append(opts, WithStatusCheck(tt.isStatusError))
			}
			span := traceMuxRequest(t, http.MethodGet, "http://example.com/users/123", tt.status, opts...)
			assert.Equal(t, tt.wantErrorType, span.Tag(ext.ErrorType))
		})
	}
}

func TestOTelSemanticsResourceNamer(t *testing.T) {
	setMuxHTTPConfig(t, "true")

	span := traceMuxRequest(t, http.MethodGet, "http://example.com/users/123", http.StatusOK, WithResourceNamer(func(*Router, *http.Request) string {
		return "custom-resource"
	}))
	assert.Equal(t, "custom-resource", span.Tag(ext.ResourceName))
	assert.Equal(t, "/users/{id}", span.Tag(ext.HTTPRoute))
}

func TestOTelSemanticsContextPropagation(t *testing.T) {
	setMuxHTTPConfig(t, "true")
	mt := mocktracer.Start()
	defer mt.Stop()

	var handlerSpan *tracer.Span
	router := NewRouter()
	router.HandleFunc("/users/{id}", func(_ http.ResponseWriter, req *http.Request) {
		handlerSpan, _ = tracer.SpanFromContext(req.Context())
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/123", nil))

	require.NotNil(t, handlerSpan)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /users/{id}", spans[0].Tag(ext.ResourceName))
}

func TestOTelSemanticsAppSecRouteParams(t *testing.T) {
	setMuxHTTPConfig(t, "true")
	testutils.StartAppSec(t)
	httptrace.ResetCfg()

	mt := mocktracer.Start()
	defer mt.Stop()
	router := NewRouter()
	router.HandleFunc("/users/{id}", func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte("ok"))
		require.NoError(t, err)
	})
	req := httptest.NewRequest(http.MethodGet, "/users/appscan_fingerprint", nil)
	router.ServeHTTP(httptest.NewRecorder(), req)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /users/{id}", spans[0].Tag(ext.ResourceName))
	assert.Equal(t, "/users/{id}", spans[0].Tag(ext.HTTPRoute))
	assert.Equal(t, "/users/{id}", spans[0].Tag(ext.HTTPEndpoint))
	event, ok := spans[0].Tag("_dd.appsec.json").(string)
	require.True(t, ok)
	assert.True(t, strings.Contains(event, "server.request.path_params"))
	assert.True(t, strings.Contains(event, "appscan_fingerprint"))
}

func traceMuxRequest(t *testing.T, method, target string, status int, opts ...RouterOption) *mocktracer.Span {
	t.Helper()
	return traceMuxRequestWithMethods(t, method, target, status, nil, opts...)
}

func traceMuxRequestWithMethods(t *testing.T, method, target string, status int, methods []string, opts ...RouterOption) *mocktracer.Span {
	t.Helper()
	mt := mocktracer.Start()
	defer mt.Stop()

	router := NewRouter(append([]RouterOption{WithService("semantic-service")}, opts...)...)
	route := router.HandleFunc("/users/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}).Host("example.com")
	if len(methods) > 0 {
		route.Methods(methods...)
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

func setMuxHTTPConfig(t *testing.T, otel string) {
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
