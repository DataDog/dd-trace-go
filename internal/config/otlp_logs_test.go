// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"net/url"
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

func TestResolveHeaders(t *testing.T) {
	t.Run("returns nil when no headers configured", func(t *testing.T) {
		headers := loadConfig().OTLPLogsHeaders()
		assert.Nil(t, headers)
	})

	t.Run("uses OTEL_EXPORTER_OTLP_HEADERS", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "key1=some%20value,key2=value2")
		headers := loadConfig().OTLPLogsHeaders()
		assert.Equal(t, map[string]string{
			"key1": "some value",
			"key2": "value2",
		}, headers)
	})

	t.Run("OTEL_EXPORTER_OTLP_LOGS_HEADERS wins over generic", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "generic=value")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "logs=specific%20value")
		headers := loadConfig().OTLPLogsHeaders()
		assert.Equal(t, map[string]string{
			"logs": "specific value",
		}, headers)
	})

	for _, raw := range []string{"invalid", "invalid,also-invalid", "=value", "   ", "key=%XX"} {
		t.Run("falls back to generic headers for "+raw, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "key=some%20value")
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", raw)
			assert.Equal(t, map[string]string{"key": "some value"}, loadConfig().OTLPLogsHeaders())
		})
	}

	t.Run("retains usable logs headers without merging generic headers", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "api-key=secret")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "invalid,logs=specific%20value,empty=,bad=%XX")
		assert.Equal(t, map[string]string{"logs": "specific value", "empty": ""}, loadConfig().OTLPLogsHeaders())
	})
}

func TestParseHeaders(t *testing.T) {
	t.Run("parses single header", func(t *testing.T) {
		headers := parseOTLPLogsHeaders("key=value")
		assert.Equal(t, map[string]string{"key": "value"}, headers)
	})

	t.Run("parses multiple headers", func(t *testing.T) {
		headers := parseOTLPLogsHeaders("key1=value1,key2=value2,key3=value3")
		assert.Equal(t, map[string]string{
			"key1": "value1",
			"key2": "value2",
			"key3": "value3",
		}, headers)
	})

	t.Run("trims spaces", func(t *testing.T) {
		headers := parseOTLPLogsHeaders("  key = value  ,  key2=value2  ")
		assert.Equal(t, map[string]string{
			"key":  "value",
			"key2": "value2",
		}, headers)
	})

	t.Run("ignores invalid entries without equals", func(t *testing.T) {
		headers := parseOTLPLogsHeaders("key1=value1,invalid,key2=value2")
		assert.Equal(t, map[string]string{
			"key1": "value1",
			"key2": "value2",
		}, headers)
	})

	t.Run("handles empty string", func(t *testing.T) {
		headers := parseOTLPLogsHeaders("")
		assert.Empty(t, headers)
	})

	t.Run("handles value with equals sign", func(t *testing.T) {
		headers := parseOTLPLogsHeaders("key=value=with=equals")
		assert.Equal(t, map[string]string{
			"key": "value=with=equals",
		}, headers)
	})

	t.Run("ignores entries with empty key", func(t *testing.T) {
		headers := parseOTLPLogsHeaders("=value,key=value2")
		assert.Equal(t, map[string]string{
			"key": "value2",
		}, headers)
	})

	t.Run("handles special characters in values", func(t *testing.T) {
		headers := parseOTLPLogsHeaders("Authorization=Bearer token123,Content-Type=application/json")
		assert.Equal(t, map[string]string{
			"Authorization": "Bearer token123",
			"Content-Type":  "application/json",
		}, headers)
	})

	t.Run("decodes values without decoding keys", func(t *testing.T) {
		headers := parseOTLPLogsHeaders("key=%20some%20value%20,other=a+b%2Bc%2520,key%20name=a%2Cb%3Dc")
		assert.Equal(t, map[string]string{
			"key":        "some value",
			"other":      "a+b+c%20",
			"key%20name": "a,b=c",
		}, headers)
	})

	t.Run("ignores malformed values without replacing valid duplicates", func(t *testing.T) {
		headers := parseOTLPLogsHeaders("key=some%20value,key=%XX,truncated=%2,incomplete=%")
		assert.Equal(t, map[string]string{"key": "some value"}, headers)
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

func TestOTLPLogsConfigCopies(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "key=value")
	cfg := loadConfig()
	headers := cfg.OTLPLogsHeaders()
	headers["key"] = "changed"
	assert.Equal(t, "value", cfg.OTLPLogsHeaders()["key"])

	agentURL := cfg.OTLPLogsAgentURL()
	agentURL.Host = "changed:8126"
	assert.NotEqual(t, agentURL.Host, cfg.OTLPLogsAgentURL().Host)

	cfg.SetAgentURL(&url.URL{Scheme: "https", Host: "custom-agent:8126"}, OriginCode)
	assert.Equal(t, "custom-agent", cfg.OTLPLogsAgentURL().Hostname())
	assert.Equal(t, "https", cfg.OTLPLogsAgentURL().Scheme)
}

func TestOTLPLogsAgentURLUnixFallback(t *testing.T) {
	t.Setenv("DD_TRACE_AGENT_URL", "unix:///var/run/datadog/apm.socket")
	t.Setenv("DD_AGENT_HOST", "log-agent")
	cfg := loadConfig()
	assert.Equal(t, "log-agent", cfg.OTLPLogsAgentURL().Hostname())
	assert.Equal(t, "http", cfg.OTLPLogsAgentURL().Scheme)
}
