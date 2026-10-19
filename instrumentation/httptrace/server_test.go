// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package httptrace

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/clientip"
)

func TestServerSpanName(t *testing.T) {
	for _, tt := range []struct {
		name   string
		method string
		route  string
		want   string
	}{
		{name: "method", method: http.MethodGet, want: "GET"},
		{name: "route", method: http.MethodPost, route: "/users/{id}", want: "POST /users/{id}"},
		{name: "case variant", method: "gEt", route: "/users", want: "GET /users"},
		{name: "unknown", method: "CUSTOM", route: "/users", want: "HTTP /users"},
		{name: "empty", route: "/users", want: "HTTP /users"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ServerSpanName(tt.method, tt.route))
		})
	}
}

func TestServerAddressPort(t *testing.T) {
	for _, tt := range []struct {
		name    string
		host    string
		urlHost string
		tls     bool
		address string
		port    int
	}{
		{name: "hostname", host: "example.com", address: "example.com", port: -1},
		{name: "IPv4", host: "192.0.2.1:8080", address: "192.0.2.1", port: 8080},
		{name: "IPv6", host: "[2001:db8::1]:8080", address: "2001:db8::1", port: 8080},
		{name: "scoped IPv6", host: "[fe80::1%eth0]:8080", address: "fe80::1%eth0", port: 8080},
		{name: "bracketed IPv6 without port", host: "[2001:db8::1]", address: "2001:db8::1", port: -1},
		{name: "empty port", host: "example.com:", address: "example.com", port: -1},
		{name: "invalid port", host: "example.com:http", address: "example.com", port: -1},
		{name: "out-of-range port", host: "example.com:65536", address: "example.com", port: -1},
		{name: "malformed bracketed IPv6", host: "[2001:db8::1", port: -1},
		{name: "HTTP default port", host: "example.com:80", address: "example.com", port: -1},
		{name: "HTTPS default port", host: "example.com:443", tls: true, address: "example.com", port: -1},
		{name: "HTTP non-default HTTPS port", host: "example.com:443", address: "example.com", port: 443},
		{name: "absolute-form authority", host: "ignored.example.com:9000", urlHost: "target.example.com:8443", tls: true, address: "target.example.com", port: 8443},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{Host: tt.host, URL: &url.URL{Host: tt.urlHost}}
			if tt.tls {
				r.TLS = new(tls.ConnectionState)
			}
			address, port := serverAddressPort(r)
			assert.Equal(t, tt.address, address)
			assert.Equal(t, tt.port, port)
		})
	}
}

func TestStartAndFinishRequestSpanOTelSemantics(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })
	cfg.otelSemanticsEnabled = true
	cfg.queryString = true
	cfg.serverQueryStringAllowlist = map[string]struct{}{"keep": {}}

	r := httptest.NewRequest("gEt", "https://example.com:8443/a%2Fb?token=secret&keep=value", nil)
	r.Header.Set("User-Agent", "test-agent")
	r.RemoteAddr = "192.0.2.1:1234"

	span := finishedSpan(t, func() {
		_, _, finish := startRequestSpan(r, map[string]string{
			ext.HTTPClientIP:    "203.0.113.10",
			ext.NetworkClientIP: "192.0.2.1",
		})
		finish(http.StatusInternalServerError, nil)
	})

	tags := span.Tags()
	assert.Equal(t, "GET", tags[ext.HTTPRequestMethod])
	assert.Equal(t, "gEt", tags[ext.HTTPRequestMethodOriginal])
	assert.Equal(t, "/a/b", tags[ext.URLPath])
	assert.Equal(t, "https", tags[ext.URLScheme])
	assert.Equal(t, "keep=value", tags[ext.URLQuery])
	assert.Equal(t, "example.com", tags[ext.ServerAddress])
	assert.Equal(t, float64(8443), tags[ext.ServerPort])
	assert.Equal(t, "test-agent", tags[ext.UserAgentOriginal])
	assert.Equal(t, "203.0.113.10", tags[ext.ClientAddress])
	assert.Equal(t, "192.0.2.1", tags[ext.NetworkPeerAddress])
	assert.Equal(t, "500", tags[ext.HTTPResponseStatusCode])
	assert.Equal(t, "500", tags[ext.ErrorType])
	assert.Equal(t, ext.SpanKindServer, tags[ext.SpanKind])
	assert.Equal(t, "GET", tags[ext.ResourceName])
	for _, key := range []string{
		ext.HTTPMethod,
		ext.HTTPURL,
		ext.HTTPCode,
		ext.HTTPUserAgent,
		ext.HTTPClientIP,
		ext.NetworkClientIP,
		"http.host",
	} {
		assert.NotContains(t, tags, key)
	}
}

func TestOTelServerQuery(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })

	tests := []struct {
		name  string
		raw   string
		setup func()
		want  any
	}{
		{name: "absent"},
		{name: "disabled", raw: "password=secret&keep=value", setup: func() { cfg.queryString = false }},
		{name: "unobfuscated", raw: "keep=value", want: "keep=value"},
		{name: "default obfuscation", raw: "password=secret&keep=value", setup: func() { cfg.useDefaultObfuscator = true }, want: "<redacted>&keep=value"},
		{name: "custom regexp", raw: "secret=value&keep=value", setup: func() { cfg.queryStringRegexp = regexp.MustCompile(`secret=[^&]+`) }, want: "<redacted>&keep=value"},
		{name: "server allowlist", raw: "drop=secret&keep=value", setup: func() { cfg.serverQueryStringAllowlist = map[string]struct{}{"keep": {}} }, want: "keep=value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg = oldCfg
			cfg.queryString = true
			cfg.queryStringRegexp = nil
			cfg.useDefaultObfuscator = false
			cfg.serverQueryStringAllowlist = nil
			if tt.setup != nil {
				tt.setup()
			}

			r := httptest.NewRequest(http.MethodGet, "/test", nil)
			r.URL.RawQuery = tt.raw
			tags := make(map[string]any)
			setOTelServerRequestTags(tags, r, nil)
			assert.Equal(t, tt.want, tags[ext.URLQuery])
		})
	}
}

func TestOTelServerQueryGlobalAllowlist(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })
	t.Setenv(envQueryStringAllowlist, "keep")
	t.Setenv(envServerQueryStringAllowlist, "")
	ResetCfg()
	cfg.otelSemanticsEnabled = true

	r := httptest.NewRequest(http.MethodGet, "/test?drop=secret&keep=value", nil)
	tags := make(map[string]any)
	setOTelServerRequestTags(tags, r, nil)
	assert.Equal(t, "keep=value", tags[ext.URLQuery])
}

func TestOTelServerOptionalTagsAbsent(t *testing.T) {
	tags := make(map[string]any)
	setOTelServerRequestTags(tags, &http.Request{
		Method: http.MethodGet,
		Host:   ":8080",
		URL:    &url.URL{},
		Header: make(http.Header),
	}, nil)

	assert.NotContains(t, tags, ext.URLPath)
	assert.NotContains(t, tags, ext.URLQuery)
	assert.NotContains(t, tags, ext.UserAgentOriginal)
	assert.NotContains(t, tags, ext.ServerAddress)
	assert.NotContains(t, tags, ext.ServerPort)
	assert.NotContains(t, tags, ext.ClientAddress)
	assert.NotContains(t, tags, ext.NetworkPeerAddress)
}

func TestOTelServerClientAddresses(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })
	t.Cleanup(clientip.ResetConfig)
	t.Setenv("DD_TRACE_CLIENT_IP_ENABLED", "true")
	t.Setenv("DD_TRACE_CLIENT_IP_HEADER", "CF-Connecting-IP")
	clientip.ResetConfig()
	ResetCfg()
	cfg.otelSemanticsEnabled = true

	r := httptest.NewRequest(http.MethodGet, "https://example.com/test", nil)
	r.RemoteAddr = "192.0.2.1:1234"
	r.Header.Set("CF-Connecting-IP", "203.0.113.10")
	r.Header.Set("X-Forwarded-For", "198.51.100.20")
	span := finishedSpan(t, func() {
		_, _, finish := StartRequestSpan(r)
		finish(http.StatusOK, nil)
	})

	assert.Equal(t, "203.0.113.10", span.Tag(ext.ClientAddress))
	assert.Equal(t, "192.0.2.1", span.Tag(ext.NetworkPeerAddress))
	assert.Nil(t, span.Tag(ext.HTTPClientIP))
	assert.Nil(t, span.Tag(ext.NetworkClientIP))
}

func TestOTelServerClientAddressesAbsent(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })
	t.Cleanup(clientip.ResetConfig)
	t.Setenv("DD_TRACE_CLIENT_IP_HEADER", "")
	clientip.ResetConfig()

	for _, tt := range []struct {
		name          string
		traceClientIP bool
		remoteAddr    string
		forwardedFor  string
	}{
		{name: "collection disabled", remoteAddr: "192.0.2.1:1234", forwardedFor: "203.0.113.10"},
		{name: "invalid addresses", traceClientIP: true, remoteAddr: "invalid", forwardedFor: "invalid"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg = oldCfg
			cfg.otelSemanticsEnabled = true
			cfg.traceClientIP = tt.traceClientIP

			r := httptest.NewRequest(http.MethodGet, "https://example.com/test", nil)
			r.RemoteAddr = tt.remoteAddr
			r.Header.Set("X-Forwarded-For", tt.forwardedFor)
			span := finishedSpan(t, func() {
				_, _, finish := StartRequestSpan(r)
				finish(http.StatusOK, nil)
			})

			assert.Nil(t, span.Tag(ext.ClientAddress))
			assert.Nil(t, span.Tag(ext.NetworkPeerAddress))
		})
	}
}

func TestFinishRequestSpanOTelStatusContract(t *testing.T) {
	for _, tt := range []struct {
		name         string
		status       int
		errorCheck   func(int) bool
		finishOpts   []tracer.FinishOption
		wantStatus   string
		wantError    bool
		wantErrType  string
		noDebugStack bool
	}{
		{name: "zero", status: 0, wantStatus: "200"},
		{name: "success", status: http.StatusOK, wantStatus: "200"},
		{name: "redirect", status: http.StatusFound, wantStatus: "302"},
		{name: "client error", status: http.StatusBadRequest, wantStatus: "400"},
		{name: "invalid status", status: 700, wantStatus: "700"},
		{name: "server error", status: http.StatusInternalServerError, wantStatus: "500", wantError: true, wantErrType: "500"},
		{name: "server error without debug stack", status: http.StatusInternalServerError, finishOpts: []tracer.FinishOption{tracer.NoDebugStack()}, wantStatus: "500", wantError: true, wantErrType: "500", noDebugStack: true},
		{name: "custom inclusion", status: http.StatusBadRequest, errorCheck: func(int) bool { return true }, wantStatus: "400", wantError: true, wantErrType: "400"},
		{name: "custom exclusion", status: http.StatusInternalServerError, errorCheck: func(int) bool { return false }, wantStatus: "500"},
		{name: "custom zero", status: 0, errorCheck: func(status int) bool { return status == 0 }, wantStatus: "0", wantError: true, wantErrType: "0"},
		{name: "real error", status: http.StatusInternalServerError, finishOpts: []tracer.FinishOption{tracer.WithError(errors.New("request failed"))}, wantStatus: "500", wantError: true, wantErrType: "*errors.errorString"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			oldCfg := cfg
			t.Cleanup(func() { cfg = oldCfg })
			cfg.otelSemanticsEnabled = true

			r := httptest.NewRequest(http.MethodGet, "/test", nil)
			span := finishedSpan(t, func() {
				_, _, finish := StartRequestSpan(r)
				finish(tt.status, tt.errorCheck, tt.finishOpts...)
			})

			assert.Equal(t, tt.wantStatus, span.Tag(ext.HTTPResponseStatusCode))
			assert.Equal(t, tt.wantError, span.Tag(ext.ErrorMsg) != nil)
			if tt.wantErrType == "" {
				assert.Nil(t, span.Tag(ext.ErrorType))
			} else {
				assert.Equal(t, tt.wantErrType, span.Tag(ext.ErrorType))
			}
			if tt.noDebugStack {
				assert.Empty(t, span.Tag(ext.ErrorStack))
			}
		})
	}
}

func TestRequestSpanOTelCallerOptionsRetainPrecedence(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })
	cfg.otelSemanticsEnabled = true

	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	span := finishedSpan(t, func() {
		_, _, finish := StartRequestSpan(r,
			tracer.ResourceName("custom-resource"),
			tracer.Tag(ext.HTTPRequestMethod, "CUSTOM"),
		)
		finish(http.StatusOK, nil)
	})

	assert.Equal(t, "custom-resource", span.Tag(ext.ResourceName))
	assert.Equal(t, "CUSTOM", span.Tag(ext.HTTPRequestMethod))
	assert.Nil(t, span.Tag(ext.ErrorType))
}

func finishedSpan(t *testing.T, trace func()) *mocktracer.Span {
	t.Helper()
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	trace()
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	return spans[0]
}
