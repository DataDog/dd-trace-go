// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package wrap

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

func TestServeMuxDatadogSemanticsPreserveHostQualifiedRoute(t *testing.T) {
	for _, value := range []string{"", "false"} {
		name := "unset"
		if value != "" {
			name = "false"
		}
		t.Run(name, func(t *testing.T) {
			setOTelSemantics(t, value)
			span := serverSpan(t, func() {
				mux := NewServeMux()
				mux.HandleFunc("GET example.com/users/{id}", func(http.ResponseWriter, *http.Request) {})
				mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://example.com/users/123", nil))
			})
			assert.Equal(t, "GET example.com/users/{id}", span.Tag(ext.ResourceName))
			assert.Equal(t, "example.com/users/{id}", span.Tag(ext.HTTPRoute))
		})
	}
}

func TestServeMuxOTelSemantics(t *testing.T) {
	setOTelSemantics(t, "true")
	t.Setenv("DD_TRACE_CLIENT_IP_ENABLED", "true")
	httptrace.ResetCfg()

	t.Run("route and attributes", func(t *testing.T) {
		span := serverSpan(t, func() {
			mux := NewServeMux(withService("semantic-service"))
			mux.HandleFunc("/users/{id}", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			r := httptest.NewRequest("gEt", "http://example.com:8080/users/123?password=secret&keep=value", nil)
			r.RemoteAddr = "192.0.2.1:1234"
			r.Header.Set("User-Agent", "semantic-agent")
			r.Header.Set("X-Forwarded-For", "203.0.113.10")
			mux.ServeHTTP(httptest.NewRecorder(), r)
		})
		assert.Equal(t, "GET /users/{id}", span.Tag(ext.ResourceName))
		assert.Equal(t, "/users/{id}", span.Tag(ext.HTTPRoute))
		assert.Equal(t, "GET", span.Tag(ext.HTTPRequestMethod))
		assert.Equal(t, "gEt", span.Tag(ext.HTTPRequestMethodOriginal))
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
		assert.Equal(t, "net/http", span.Tag(ext.Component))
		assert.Nil(t, span.Tag(ext.HTTPMethod))
		assert.Nil(t, span.Tag(ext.HTTPURL))
		assert.Nil(t, span.Tag(ext.HTTPCode))
		assert.Nil(t, span.Tag(ext.HTTPUserAgent))
		assert.Nil(t, span.Tag(ext.HTTPClientIP))
		assert.Nil(t, span.Tag(ext.NetworkClientIP))
	})

	t.Run("route is invariant across parameters", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		mux := NewServeMux()
		mux.HandleFunc("/users/{id}", func(http.ResponseWriter, *http.Request) {})
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/123", nil))
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/456", nil))

		spans := mt.FinishedSpans()
		require.Len(t, spans, 2)
		assert.Equal(t, "GET /users/{id}", spans[0].Tag(ext.ResourceName))
		assert.Equal(t, "GET /users/{id}", spans[1].Tag(ext.ResourceName))
	})

	t.Run("unknown method without route", func(t *testing.T) {
		span := serverSpan(t, func() {
			mux := NewServeMux()
			mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PROPFIND", "/actual/path", nil))
		})
		assert.Equal(t, "HTTP", span.Tag(ext.ResourceName))
		assert.Nil(t, span.Tag(ext.HTTPRoute))
		assert.Equal(t, "/actual/path", span.Tag(ext.URLPath))
		assert.Equal(t, "_OTHER", span.Tag(ext.HTTPRequestMethod))
		assert.Equal(t, "PROPFIND", span.Tag(ext.HTTPRequestMethodOriginal))
	})

	t.Run("host-qualified route", func(t *testing.T) {
		for _, pattern := range []string{"example.com/users/{id}", "GET example.com/users/{id}"} {
			t.Run(pattern, func(t *testing.T) {
				span := serverSpan(t, func() {
					mux := NewServeMux()
					mux.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
					mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://example.com/users/123", nil))
				})
				assert.Equal(t, "GET /users/{id}", span.Tag(ext.ResourceName))
				assert.Equal(t, "/users/{id}", span.Tag(ext.HTTPRoute))
			})
		}
	})

	t.Run("custom resource namer", func(t *testing.T) {
		span := serverSpan(t, func() {
			mux := NewServeMux(withResourceNamer(func(*http.Request) string { return "custom-resource" }))
			mux.HandleFunc("/users/{id}", func(http.ResponseWriter, *http.Request) {})
			mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/123", nil))
		})
		assert.Equal(t, "custom-resource", span.Tag(ext.ResourceName))
		assert.Equal(t, "/users/{id}", span.Tag(ext.HTTPRoute))
	})

	t.Run("caller resource option", func(t *testing.T) {
		span := serverSpan(t, func() {
			mux := NewServeMux(withSpanOptions(tracer.ResourceName("caller-resource")))
			mux.HandleFunc("/users/{id}", func(http.ResponseWriter, *http.Request) {})
			mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/123", nil))
		})
		assert.Equal(t, "caller-resource", span.Tag(ext.ResourceName))
		assert.Equal(t, "/users/{id}", span.Tag(ext.HTTPRoute))
	})
}

func TestHandlerOTelSemantics(t *testing.T) {
	setOTelSemantics(t, "true")

	for _, tt := range []struct {
		name         string
		resource     string
		opts         []config.Option
		wantResource string
	}{
		{name: "route default", wantResource: "GET /wrapped/{id}"},
		{name: "caller resource option", opts: []config.Option{withSpanOptions(tracer.ResourceName("caller-resource"))}, wantResource: "caller-resource"},
		{name: "explicit resource", resource: "explicit-resource", wantResource: "explicit-resource"},
		{name: "resource namer overrides explicit", resource: "explicit-resource", opts: []config.Option{withResourceNamer(func(*http.Request) string { return "named-resource" })}, wantResource: "named-resource"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			span := serverSpan(t, func() {
				wrapped := Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), "service", tt.resource, tt.opts...)
				mux := http.NewServeMux()
				mux.Handle("GET /wrapped/{id}", wrapped)
				mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/wrapped/123", nil))
			})
			assert.Equal(t, tt.wantResource, span.Tag(ext.ResourceName))
			assert.Equal(t, "/wrapped/{id}", span.Tag(ext.HTTPRoute))
		})
	}
}

func TestTraceAndServeOTelSemantics(t *testing.T) {
	setOTelSemantics(t, "true")

	t.Run("request pattern", func(t *testing.T) {
		span := serverSpan(t, func() {
			r := httptest.NewRequest(http.MethodGet, "/trace/123", nil)
			r.Pattern = "GET /trace/{id}"
			TraceAndServe(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), httptest.NewRecorder(), r, nil)
		})
		assert.Equal(t, "GET /trace/{id}", span.Tag(ext.ResourceName))
		assert.Equal(t, "/trace/{id}", span.Tag(ext.HTTPRoute))
	})

	t.Run("caller resource option", func(t *testing.T) {
		span := serverSpan(t, func() {
			r := httptest.NewRequest(http.MethodGet, "/trace/123", nil)
			r.Pattern = "GET /trace/{id}"
			traceAndServe(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), httptest.NewRecorder(), r, &httptrace.ServeConfig{
				SpanOpts: []tracer.StartSpanOption{tracer.ResourceName("caller-resource")},
			}, true)
		})
		assert.Equal(t, "caller-resource", span.Tag(ext.ResourceName))
		assert.Equal(t, "/trace/{id}", span.Tag(ext.HTTPRoute))
	})

	t.Run("explicit resource", func(t *testing.T) {
		span := serverSpan(t, func() {
			r := httptest.NewRequest(http.MethodGet, "/trace/123", nil)
			r.Pattern = "GET /trace/{id}"
			TraceAndServe(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), httptest.NewRecorder(), r, &httptrace.ServeConfig{Resource: "explicit-resource"})
		})
		assert.Equal(t, "explicit-resource", span.Tag(ext.ResourceName))
		assert.Equal(t, "/trace/{id}", span.Tag(ext.HTTPRoute))
	})
}

func TestServerOTelSemanticStatusChecks(t *testing.T) {
	setOTelSemantics(t, "true")

	for _, tt := range []struct {
		name        string
		status      int
		statusCheck func(int) bool
		wantError   bool
	}{
		{name: "success", status: http.StatusOK},
		{name: "client error", status: http.StatusBadRequest},
		{name: "server error", status: http.StatusInternalServerError, wantError: true},
		{name: "custom inclusion", status: http.StatusBadRequest, statusCheck: func(status int) bool { return status == http.StatusBadRequest }, wantError: true},
		{name: "custom exclusion", status: http.StatusInternalServerError, statusCheck: func(int) bool { return false }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			span := serverSpan(t, func() {
				opts := []config.Option(nil)
				if tt.statusCheck != nil {
					opts = append(opts, withStatusCheck(tt.statusCheck))
				}
				mux := NewServeMux(opts...)
				mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tt.status) })
				mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/status", nil))
			})
			assert.Equal(t, strconv.Itoa(tt.status), span.Tag(ext.HTTPResponseStatusCode))
			assert.Equal(t, tt.wantError, span.Tag(ext.ErrorMsg) != nil)
			if tt.wantError {
				assert.Equal(t, span.Tag(ext.HTTPResponseStatusCode), span.Tag(ext.ErrorType))
			} else {
				assert.Nil(t, span.Tag(ext.ErrorType))
			}
		})
	}
}

func setOTelSemantics(t *testing.T, value string) {
	t.Helper()
	oldValue, wasSet := os.LookupEnv("DD_TRACE_OTEL_SEMANTICS_ENABLED")
	if value == "" {
		require.NoError(t, os.Unsetenv("DD_TRACE_OTEL_SEMANTICS_ENABLED"))
	} else {
		require.NoError(t, os.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", value))
	}
	require.NoError(t, tracer.Start(tracer.WithTraceEnabled(false)))
	httptrace.ResetCfg()
	t.Cleanup(func() {
		if wasSet {
			require.NoError(t, os.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", oldValue))
		} else {
			require.NoError(t, os.Unsetenv("DD_TRACE_OTEL_SEMANTICS_ENABLED"))
		}
		require.NoError(t, tracer.Start(tracer.WithTraceEnabled(false)))
		httptrace.ResetCfg()
		tracer.Stop()
	})
}

func withService(name string) config.Option {
	return config.OptionFn(func(cfg *config.CommonConfig) {
		cfg.ServiceName = name
		cfg.ServiceSource = "test"
	})
}

func withResourceNamer(namer func(*http.Request) string) config.Option {
	return config.OptionFn(func(cfg *config.CommonConfig) {
		cfg.ResourceNamer = namer
	})
}

func withSpanOptions(opts ...tracer.StartSpanOption) config.Option {
	return config.OptionFn(func(cfg *config.CommonConfig) {
		cfg.SpanOpts = append(cfg.SpanOpts, opts...)
	})
}

func withStatusCheck(check func(int) bool) config.Option {
	return config.OptionFn(func(cfg *config.CommonConfig) {
		cfg.IsStatusError = check
	})
}

func serverSpan(t *testing.T, serve func()) *mocktracer.Span {
	t.Helper()
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	serve()
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	return spans[0]
}
