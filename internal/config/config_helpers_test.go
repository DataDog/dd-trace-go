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
	httpAgent := &url.URL{Scheme: "http", Host: "myhost:8126"}
	defaultWithAgent := "http://myhost:4318/v1/traces"
	defaultLocalhost := "http://localhost:4318/v1/traces"

	t.Run("valid http endpoint used when set", func(t *testing.T) {
		got := resolveOTLPTraceURL(httpAgent, "http://traces-collector:4318/v1/traces")
		assert.Equal(t, "http://traces-collector:4318/v1/traces", got)
	})

	t.Run("valid https endpoint used when set", func(t *testing.T) {
		got := resolveOTLPTraceURL(httpAgent, "https://traces-collector:4318/v1/traces")
		assert.Equal(t, "https://traces-collector:4318/v1/traces", got)
	})

	t.Run("unsupported scheme falls back to default", func(t *testing.T) {
		got := resolveOTLPTraceURL(httpAgent, "grpc://traces-collector:4317")
		assert.Equal(t, defaultWithAgent, got)
	})

	t.Run("missing scheme falls back to default", func(t *testing.T) {
		got := resolveOTLPTraceURL(httpAgent, "traces-collector:4318/v1/traces")
		assert.Equal(t, defaultWithAgent, got)
	})

	t.Run("default uses agent host with OTLP port", func(t *testing.T) {
		got := resolveOTLPTraceURL(httpAgent, "")
		assert.Equal(t, defaultWithAgent, got)
	})

	t.Run("default with nil agent URL uses localhost", func(t *testing.T) {
		got := resolveOTLPTraceURL(nil, "")
		assert.Equal(t, defaultLocalhost, got)
	})

	t.Run("default with unix socket agent uses localhost", func(t *testing.T) {
		unixAgent := &url.URL{Scheme: "unix", Path: "/var/run/datadog/apm.socket"}
		got := resolveOTLPTraceURL(unixAgent, "")
		assert.Equal(t, defaultLocalhost, got)
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

func TestParseGlobalTags(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]any
	}{
		{"empty string returns nil", "", nil},
		{"normal tag parsed", "k:v", map[string]any{"k": "v"}},
		{"only git metadata cleaned to nil", "git.repository_url:x", nil},
		{"git metadata stripped, normal tags kept", "k:v,git.commit.sha:abc", map[string]any{"k": "v"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseGlobalTags(tt.in))
		})
	}
}
