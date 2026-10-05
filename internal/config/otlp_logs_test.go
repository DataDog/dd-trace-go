// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestResolveOTLPProtocol(t *testing.T) {
	t.Run("defaults to http/json", func(t *testing.T) {
		protocol := loadConfig().OTLPLogsProtocol()
		assert.Equal(t, "http/json", protocol)
	})

	t.Run("uses OTEL_EXPORTER_OTLP_PROTOCOL", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
		protocol := loadConfig().OTLPLogsProtocol()
		assert.Equal(t, "grpc", protocol)
	})

	t.Run("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL wins over generic", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/protobuf")
		protocol := loadConfig().OTLPLogsProtocol()
		assert.Equal(t, "http/protobuf", protocol)
	})

	t.Run("trims and lowercases protocol", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "  GRPC  ")
		protocol := loadConfig().OTLPLogsProtocol()
		assert.Equal(t, "grpc", protocol)
	})

	t.Run("supports http/json", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/json")
		protocol := loadConfig().OTLPLogsProtocol()
		assert.Equal(t, "http/json", protocol)
	})

	t.Run("supports http/protobuf", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/protobuf")
		protocol := loadConfig().OTLPLogsProtocol()
		assert.Equal(t, "http/protobuf", protocol)
	})
}

func TestResolveExportTimeout(t *testing.T) {
	t.Run("defaults to 30 seconds", func(t *testing.T) {
		timeout := loadConfig().OTLPLogsTimeout()
		assert.Equal(t, 30*time.Second, timeout)
	})

	t.Run("uses OTEL_EXPORTER_OTLP_TIMEOUT", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "5000")
		timeout := loadConfig().OTLPLogsTimeout()
		assert.Equal(t, 5*time.Second, timeout)
	})

	t.Run("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT wins over generic", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "5000")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", "10000")
		timeout := loadConfig().OTLPLogsTimeout()
		assert.Equal(t, 10*time.Second, timeout)
	})

	t.Run("falls back to default on invalid value", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", "invalid")
		timeout := loadConfig().OTLPLogsTimeout()
		assert.Equal(t, 30*time.Second, timeout)
	})
}

func TestOTLPLogsEndpoint(t *testing.T) {
	t.Run("returns false when no env vars set", func(t *testing.T) {
		assert.Empty(t, loadConfig().OTLPLogsEndpoint())
	})

	t.Run("returns true when OTEL_EXPORTER_OTLP_ENDPOINT set", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://custom:4318")
		assert.Equal(t, "http://custom:4318", loadConfig().OTLPLogsEndpoint())
	})

	t.Run("returns true when OTEL_EXPORTER_OTLP_LOGS_ENDPOINT set", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "http://custom:4318")
		assert.Equal(t, "http://custom:4318", loadConfig().OTLPLogsEndpoint())
	})
}

func TestOTLPLogsTimeoutFallback(t *testing.T) {
	for _, raw := range []string{"", "invalid", "1000.5"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "5000")
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", raw)
			assert.Equal(t, 5*time.Second, loadConfig().OTLPLogsTimeout())
		})
	}
	for _, raw := range []string{"0", "-1000"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", raw)
			expected := time.Duration(0)
			if raw == "-1000" {
				expected = -time.Second
			}
			assert.Equal(t, expected, loadConfig().OTLPLogsTimeout())
		})
	}
}

func TestOTLPLogsAgentURLUnixFallback(t *testing.T) {
	t.Setenv("DD_TRACE_AGENT_URL", "unix:///var/run/datadog/apm.socket")
	t.Setenv("DD_AGENT_HOST", "log-agent")
	cfg := loadConfig()
	assert.Equal(t, "log-agent", cfg.OTLPLogsAgentURL().Hostname())
	assert.Equal(t, "http", cfg.OTLPLogsAgentURL().Scheme)
}
