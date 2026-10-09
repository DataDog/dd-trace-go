// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveOTLPTraceURL(t *testing.T) {
	agentDefault := "http://myhost:4318"

	t.Run("valid http endpoint used when set", func(t *testing.T) {
		got := resolveOTLPTraceURL("http://traces-collector:4318/v1/traces", agentDefault)
		assert.Equal(t, "http://traces-collector:4318/v1/traces", got)
	})

	t.Run("traces-specific endpoint without a path is used as-is", func(t *testing.T) {
		got := resolveOTLPTraceURL("http://traces-collector:4318", agentDefault)
		assert.Equal(t, "http://traces-collector:4318", got)
	})

	t.Run("valid https endpoint used when set", func(t *testing.T) {
		got := resolveOTLPTraceURL("https://traces-collector:4318/v1/traces", agentDefault)
		assert.Equal(t, "https://traces-collector:4318/v1/traces", got)
	})

	t.Run("unsupported scheme falls back to generic endpoint", func(t *testing.T) {
		got := resolveOTLPTraceURL("grpc://traces-collector:4317", agentDefault)
		assert.Equal(t, "http://myhost:4318/v1/traces", got)
	})

	t.Run("missing scheme falls back to generic endpoint", func(t *testing.T) {
		got := resolveOTLPTraceURL("traces-collector:4318/v1/traces", agentDefault)
		assert.Equal(t, "http://myhost:4318/v1/traces", got)
	})

	t.Run("unset uses generic endpoint", func(t *testing.T) {
		got := resolveOTLPTraceURL("", agentDefault)
		assert.Equal(t, "http://myhost:4318/v1/traces", got)
	})

	t.Run("IPv6 generic endpoint is bracketed in URL", func(t *testing.T) {
		got := resolveOTLPTraceURL("", "http://[::1]:4318")
		assert.Equal(t, "http://[::1]:4318/v1/traces", got)
	})

	t.Run("generic endpoint always has the signal path appended", func(t *testing.T) {
		got := resolveOTLPTraceURL("", "http://general-collector:4318/v1/traces")
		assert.Equal(t, "http://general-collector:4318/v1/traces/v1/traces", got)
	})

	t.Run("general OTLP endpoint with trailing slash", func(t *testing.T) {
		got := resolveOTLPTraceURL("", "http://general-collector:4318/")
		assert.Equal(t, "http://general-collector:4318/v1/traces", got)
	})

	t.Run("traces-specific endpoint takes priority over general endpoint", func(t *testing.T) {
		got := resolveOTLPTraceURL("http://traces-collector:4318/v1/traces", "http://general-collector:4318")
		assert.Equal(t, "http://traces-collector:4318/v1/traces", got)
	})

	t.Run("escaped path segment stays intact in the generic endpoint", func(t *testing.T) {
		got := resolveOTLPTraceURL("", "https://collector/tenant%2Fblue")
		assert.Equal(t, "https://collector/tenant%2Fblue/v1/traces", got)
	})
}

func TestResolveOTLPEndpoint(t *testing.T) {
	httpAgent := &url.URL{Scheme: "http", Host: "myhost:8126"}

	t.Run("IPv6 agent host is bracketed in default URL", func(t *testing.T) {
		ipv6Agent := &url.URL{Scheme: "http", Host: "[::1]:8126"}
		got := resolveOTLPEndpoint(ipv6Agent, "")
		assert.Equal(t, "http://[::1]:4318", got)
	})

	t.Run("custom endpoint returned as-is", func(t *testing.T) {
		got := resolveOTLPEndpoint(httpAgent, "http://custom:4317")
		assert.Equal(t, "http://custom:4317", got)
	})

	t.Run("unsupported scheme falls back to default", func(t *testing.T) {
		got := resolveOTLPEndpoint(httpAgent, "grpc://custom:4317")
		assert.Equal(t, "http://myhost:4318", got)
	})

	t.Run("nil agent URL uses the default hostname", func(t *testing.T) {
		got := resolveOTLPEndpoint(nil, "")
		assert.Equal(t, "http://localhost:4318", got)
	})

	t.Run("unix socket agent uses the default hostname", func(t *testing.T) {
		unixAgent := &url.URL{Scheme: "unix", Path: "/var/run/datadog/apm.socket"}
		got := resolveOTLPEndpoint(unixAgent, "")
		assert.Equal(t, "http://localhost:4318", got)
	})
}

func TestResolveOTLPMetricsURL(t *testing.T) {
	agentDefault := "http://myhost:4318"

	t.Run("IPv6 generic endpoint is bracketed in URL", func(t *testing.T) {
		got := resolveOTLPMetricsURL("", "http://[::1]:4318")
		assert.Equal(t, "http://[::1]:4318/v1/metrics", got)
	})

	t.Run("signal endpoint appends /v1/metrics when path absent", func(t *testing.T) {
		got := resolveOTLPMetricsURL("http://collector:4318", agentDefault)
		assert.Equal(t, "http://collector:4318/v1/metrics", got)
	})

	t.Run("escaped path segment stays intact in the generic endpoint", func(t *testing.T) {
		got := resolveOTLPMetricsURL("", "https://collector/tenant%2Fblue")
		assert.Equal(t, "https://collector/tenant%2Fblue/v1/metrics", got)
	})
}

func TestValidateSendRetries(t *testing.T) {
	tests := []struct {
		name    string
		retries int
		want    bool
	}{
		{"negative rejected", -1, false},
		{"zero accepted", 0, true},
		{"positive accepted", 3, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, validateSendRetries(tt.retries))
		})
	}
}

func TestGlobalTags(t *testing.T) {
	tests := []struct {
		name string
		in   string
		otel string
		want map[string]any
	}{
		{"empty string returns nil", "", "", nil},
		{"normal tag parsed", "k:v", "", map[string]any{"k": "v"}},
		{"only git metadata cleaned to nil", "git.repository_url:x", "", nil},
		{"git metadata stripped, normal tags kept", "k:v,git.commit.sha:abc", "", map[string]any{"k": "v"}},
		{"OTel git metadata stripped, reserved names mapped", "", "k=v,git.commit.sha=abc,service.name=svc", map[string]any{"k": "v", "service": "svc"}},
		{"DD_TAGS wins over OTel", "k:dd", "k=otel,other=v", map[string]any{"k": "dd"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DD_TAGS", tt.in)
			t.Setenv("OTEL_RESOURCE_ATTRIBUTES", tt.otel)
			assert.Equal(t, tt.want, CreateNew().GlobalTags())
		})
	}
}
