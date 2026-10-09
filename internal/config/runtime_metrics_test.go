// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResolveOTLPEndpoint_Default verifies that the default HTTP endpoint
// is localhost:4318 with /v1/metrics path and insecure connection.
func TestResolveOTLPEndpoint_Default(t *testing.T) {
	endpoint, path, insecure := loadConfig().RuntimeMetricsHTTPEndpoint()
	assert.Equal(t, "localhost:4318", endpoint)
	assert.Equal(t, "/v1/metrics", path)
	assert.True(t, insecure)
}

// TestResolveOTLPEndpoint_DDTraceAgentURL verifies that DD_TRACE_AGENT_URL is used
// to derive the OTLP endpoint by extracting the hostname and using port 4318.
func TestResolveOTLPEndpoint_DDTraceAgentURL(t *testing.T) {
	tests := []struct {
		name             string
		agentURL         string
		expectedEndpoint string
		expectedInsecure bool
	}{
		{
			name:             "http URL",
			agentURL:         "http://ddapm-test-agent-335a19:8126",
			expectedEndpoint: "ddapm-test-agent-335a19:4318",
			expectedInsecure: true,
		},
		{
			name:             "https URL",
			agentURL:         "https://agent.example.com:8126",
			expectedEndpoint: "agent.example.com:4318",
			expectedInsecure: false,
		},
		{
			name:             "URL with path",
			agentURL:         "http://agent.example.com:8126/v1.0/traces",
			expectedEndpoint: "agent.example.com:4318",
			expectedInsecure: true,
		},
		{
			name:             "URL without port",
			agentURL:         "http://agent.example.com",
			expectedEndpoint: "agent.example.com:4318",
			expectedInsecure: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DD_TRACE_AGENT_URL", tt.agentURL)

			endpoint, path, insecure := loadConfig().RuntimeMetricsHTTPEndpoint()
			assert.Equal(t, tt.expectedEndpoint, endpoint)
			assert.Equal(t, "/v1/metrics", path)
			assert.Equal(t, tt.expectedInsecure, insecure)
		})
	}
}

// TestResolveOTLPEndpoint_Priority verifies endpoint resolution priority:
// DD_TRACE_AGENT_URL > DD_AGENT_HOST > default (localhost:4318)
func TestResolveOTLPEndpoint_Priority(t *testing.T) {
	t.Run("DD_TRACE_AGENT_URL takes priority", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "http://priority-agent:8126")
		t.Setenv("DD_AGENT_HOST", "fallback-agent")

		endpoint, _, _ := loadConfig().RuntimeMetricsHTTPEndpoint()
		assert.Equal(t, "priority-agent:4318", endpoint)
	})

	t.Run("DD_AGENT_HOST as fallback", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "fallback-agent")

		endpoint, path, insecure := loadConfig().RuntimeMetricsHTTPEndpoint()
		assert.Equal(t, "fallback-agent:4318", endpoint)
		assert.Equal(t, "/v1/metrics", path)
		assert.True(t, insecure)
	})
}

// TestResolveOTLPEndpoint_InvalidURL verifies that when DD_TRACE_AGENT_URL is invalid,
// the endpoint resolution falls back to DD_AGENT_HOST.
func TestResolveOTLPEndpoint_InvalidURL(t *testing.T) {
	t.Setenv("DD_TRACE_AGENT_URL", "://invalid-url")
	t.Setenv("DD_AGENT_HOST", "fallback-agent")

	// Should fall back to DD_AGENT_HOST when URL parsing fails
	endpoint, _, _ := loadConfig().RuntimeMetricsHTTPEndpoint()
	assert.Equal(t, "fallback-agent:4318", endpoint)
}

// TestGetOTLPProtocol verifies protocol selection from environment variables:
// - OTEL_EXPORTER_OTLP_METRICS_PROTOCOL takes priority
// - OTEL_EXPORTER_OTLP_PROTOCOL as fallback
// - Default: http/protobuf
func TestGetOTLPProtocol(t *testing.T) {
	t.Run("Default to http/protobuf", func(t *testing.T) {
		protocol := loadConfig().RuntimeMetricsProtocol()
		assert.Equal(t, "http/protobuf", protocol)
	})

	t.Run("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL takes priority", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "grpc")
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http")

		protocol := loadConfig().RuntimeMetricsProtocol()
		assert.Equal(t, "grpc", protocol)
	})

	t.Run("OTEL_EXPORTER_OTLP_PROTOCOL as fallback", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")

		protocol := loadConfig().RuntimeMetricsProtocol()
		assert.Equal(t, "grpc", protocol)
	})

	t.Run("Case insensitive", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "GRPC")

		protocol := loadConfig().RuntimeMetricsProtocol()
		assert.Equal(t, "grpc", protocol)
	})

	t.Run("Trim whitespace", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "  http/protobuf  ")

		protocol := loadConfig().RuntimeMetricsProtocol()
		assert.Equal(t, "http/protobuf", protocol)
	})
}

// TestResolveOTLPEndpointGRPC verifies gRPC endpoint resolution with default port 4317
// and proper handling of DD_TRACE_AGENT_URL and DD_AGENT_HOST.
func TestResolveOTLPEndpointGRPC(t *testing.T) {
	t.Run("Default to localhost:4317", func(t *testing.T) {
		endpoint, insecure := loadConfig().RuntimeMetricsGRPCEndpoint()
		assert.Equal(t, "localhost:4317", endpoint)
		assert.True(t, insecure)
	})

	t.Run("DD_TRACE_AGENT_URL", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "http://custom-agent:8126")

		endpoint, insecure := loadConfig().RuntimeMetricsGRPCEndpoint()
		assert.Equal(t, "custom-agent:4317", endpoint)
		assert.True(t, insecure)
	})

	t.Run("DD_AGENT_HOST", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "custom-host")

		endpoint, _ := loadConfig().RuntimeMetricsGRPCEndpoint()
		assert.Equal(t, "custom-host:4317", endpoint)
	})
}

func TestRuntimeMetricsEndpointFallback(t *testing.T) {
	for _, tc := range []struct {
		name, generic, signal, host, path, grpc string
		insecure                                bool
	}{
		{"generic prefix", "http://collector:4318/prefix/", "", "collector:4318", "/prefix/v1/metrics", "collector:4318/prefix", true},
		{"signal exact", "http://generic:4318/prefix", "https://signal:443/custom/", "signal:443", "/custom/", "signal:443/custom", false},
		{"signal root", "", "http://signal:4318", "signal:4318", "/", "signal:4318", true},
		{"invalid signal", "http://generic:4318", "http://%", "generic:4318", "/v1/metrics", "generic:4318", true},
		{"both invalid", "http://%", "http://%", "localhost:4318", "/v1/metrics", "localhost:4317", false},
		{"whitespace", "   ", "", "localhost:4318", "/v1/metrics", "localhost:4317", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", tc.generic)
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", tc.signal)
			cfg := loadConfig()
			host, path, insecure := cfg.RuntimeMetricsHTTPEndpoint()
			assert.Equal(t, tc.host, host)
			assert.Equal(t, tc.path, path)
			assert.Equal(t, tc.insecure, insecure)
			target, _ := cfg.RuntimeMetricsGRPCEndpoint()
			assert.Equal(t, tc.grpc, target)
		})
	}
}

func TestRuntimeMetricsHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, signal string
		expected     map[string]string
	}{
		{"generic", "", map[string]string{"Authorization": "Bearer token"}},
		{"blank signal", "  ", map[string]string{"Authorization": "Bearer token"}},
		{"signal replaces", "x-key=a%2Bb", map[string]string{"x-key": "a+b"}},
		{"invalid signal replaces", "invalid,bad key=value,bad=%XX,=value", map[string]string{}},
		{"mixed", "bad key=x,good=some%20value,empty=", map[string]string{"good": "some value", "empty": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=Bearer%20token")
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", tc.signal)
			cfg := loadConfig()
			assert.Equal(t, tc.expected, cfg.RuntimeMetricsHeaders())
			headers := cfg.RuntimeMetricsHeaders()
			headers["changed"] = "value"
			assert.NotContains(t, cfg.RuntimeMetricsHeaders(), "changed")
		})
	}
}

func TestRuntimeMetricsIndependentOfSpanMetrics(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "grpc")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=Bearer%20token")
	cfg := loadConfig()
	assert.Equal(t, "grpc", cfg.RuntimeMetricsProtocol())
	assert.Equal(t, "http/protobuf", cfg.OTLPMetricsProtocol())
	assert.Equal(t, "Bearer token", cfg.RuntimeMetricsHeaders()["Authorization"])
	assert.Equal(t, "Bearer%20token", cfg.OTLPMetricsHeaders()["Authorization"])
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "http/json")
	assert.Equal(t, "grpc", cfg.RuntimeMetricsProtocol())
}

func TestRuntimeMetricsInsecurePrecedence(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://collector:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")
	_, _, insecure := loadConfig().RuntimeMetricsHTTPEndpoint()
	assert.True(t, insecure)
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_INSECURE", "false")
	_, _, insecure = loadConfig().RuntimeMetricsHTTPEndpoint()
	assert.False(t, insecure)
}

func TestRuntimeMetricsEffectiveProtocol(t *testing.T) {
	for _, raw := range []string{"http", "http/json", "invalid", " "} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", raw)
			assert.Equal(t, "http/protobuf", loadConfig().RuntimeMetricsEffectiveProtocol())
		})
	}
}
