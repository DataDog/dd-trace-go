// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestResolveOTLPProtocol(t *testing.T) {
	t.Run("defaults to http/json", func(t *testing.T) {
		protocol := resolveOTLPProtocol()
		assert.Equal(t, "http/json", protocol)
	})

	t.Run("uses OTEL_EXPORTER_OTLP_PROTOCOL", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
		protocol := resolveOTLPProtocol()
		assert.Equal(t, "grpc", protocol)
	})

	t.Run("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL wins over generic", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/protobuf")
		protocol := resolveOTLPProtocol()
		assert.Equal(t, "http/protobuf", protocol)
	})

	t.Run("trims and lowercases protocol", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "  GRPC  ")
		protocol := resolveOTLPProtocol()
		assert.Equal(t, "grpc", protocol)
	})

	t.Run("supports http/json", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/json")
		protocol := resolveOTLPProtocol()
		assert.Equal(t, "http/json", protocol)
	})

	t.Run("supports http/protobuf", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/protobuf")
		protocol := resolveOTLPProtocol()
		assert.Equal(t, "http/protobuf", protocol)
	})
}

func TestHasOTLPEndpointInEnv(t *testing.T) {
	t.Run("returns false when no env vars set", func(t *testing.T) {
		assert.False(t, hasOTLPEndpointInEnv())
	})

	t.Run("returns true when OTEL_EXPORTER_OTLP_ENDPOINT set", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://custom:4318")
		assert.True(t, hasOTLPEndpointInEnv())
	})

	t.Run("returns true when OTEL_EXPORTER_OTLP_LOGS_ENDPOINT set", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "http://custom:4318")
		assert.True(t, hasOTLPEndpointInEnv())
	})
}

func TestResolveOTLPEndpointHTTP(t *testing.T) {
	t.Run("defaults to localhost:4318", func(t *testing.T) {
		endpoint, path, insecure := resolveOTLPEndpointHTTP()
		assert.Equal(t, "localhost:4318", endpoint)
		assert.Equal(t, "/v1/logs", path)
		assert.True(t, insecure)
	})

	t.Run("uses DD_AGENT_HOST", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "agent.example.com")
		endpoint, path, insecure := resolveOTLPEndpointHTTP()
		assert.Equal(t, "agent.example.com:4318", endpoint)
		assert.Equal(t, "/v1/logs", path)
		assert.True(t, insecure)
	})

	t.Run("uses DD_TRACE_AGENT_URL", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "http://trace-agent:8126")
		endpoint, path, insecure := resolveOTLPEndpointHTTP()
		assert.Equal(t, "trace-agent:4318", endpoint)
		assert.Equal(t, "/v1/logs", path)
		assert.True(t, insecure)
	})

	t.Run("DD_TRACE_AGENT_URL wins over DD_AGENT_HOST", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "agent-host")
		t.Setenv("DD_TRACE_AGENT_URL", "http://trace-agent:8126")
		endpoint, _, _ := resolveOTLPEndpointHTTP()
		assert.Equal(t, "trace-agent:4318", endpoint)
	})

	t.Run("preserves https scheme", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "https://secure-agent:8126")
		_, _, insecure := resolveOTLPEndpointHTTP()
		assert.False(t, insecure, "https should result in insecure=false")
	})

	t.Run("handles unix socket scheme", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "unix:///var/run/datadog/apm.socket")
		_, _, insecure := resolveOTLPEndpointHTTP()
		assert.True(t, insecure, "unix scheme should result in insecure=true")
	})

	t.Run("handles IPv6 addresses", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "http://[::1]:8126")
		endpoint, _, _ := resolveOTLPEndpointHTTP()
		assert.Equal(t, "[::1]:4318", endpoint)
	})
}

func TestResolveOTLPEndpointGRPC(t *testing.T) {
	t.Run("defaults to localhost:4317", func(t *testing.T) {
		endpoint, insecure := resolveOTLPEndpointGRPC()
		assert.Equal(t, "localhost:4317", endpoint)
		assert.True(t, insecure)
	})

	t.Run("uses DD_AGENT_HOST", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "agent.example.com")
		endpoint, insecure := resolveOTLPEndpointGRPC()
		assert.Equal(t, "agent.example.com:4317", endpoint)
		assert.True(t, insecure)
	})

	t.Run("uses DD_TRACE_AGENT_URL", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "http://trace-agent:8126")
		endpoint, insecure := resolveOTLPEndpointGRPC()
		assert.Equal(t, "trace-agent:4317", endpoint)
		assert.True(t, insecure)
	})

	t.Run("DD_TRACE_AGENT_URL wins over DD_AGENT_HOST", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "agent-host")
		t.Setenv("DD_TRACE_AGENT_URL", "http://trace-agent:8126")
		endpoint, _ := resolveOTLPEndpointGRPC()
		assert.Equal(t, "trace-agent:4317", endpoint)
	})

	t.Run("preserves https scheme", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "https://secure-agent:8126")
		_, insecure := resolveOTLPEndpointGRPC()
		assert.False(t, insecure, "https should result in insecure=false")
	})
}

func TestResolveHeaders(t *testing.T) {
	t.Run("returns nil when no headers configured", func(t *testing.T) {
		headers := resolveHeaders()
		assert.Nil(t, headers)
	})

	t.Run("uses OTEL_EXPORTER_OTLP_HEADERS", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "key1=value1,key2=value2")
		headers := resolveHeaders()
		assert.Equal(t, map[string]string{
			"key1": "value1",
			"key2": "value2",
		}, headers)
	})

	t.Run("OTEL_EXPORTER_OTLP_LOGS_HEADERS wins over generic", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "generic=value")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "logs=specific")
		headers := resolveHeaders()
		assert.Equal(t, map[string]string{
			"logs": "specific",
		}, headers)
	})
}

func TestParseHeaders(t *testing.T) {
	t.Run("parses single header", func(t *testing.T) {
		headers := parseHeaders("key=value")
		assert.Equal(t, map[string]string{"key": "value"}, headers)
	})

	t.Run("parses multiple headers", func(t *testing.T) {
		headers := parseHeaders("key1=value1,key2=value2,key3=value3")
		assert.Equal(t, map[string]string{
			"key1": "value1",
			"key2": "value2",
			"key3": "value3",
		}, headers)
	})

	t.Run("trims spaces", func(t *testing.T) {
		headers := parseHeaders("  key = value  ,  key2=value2  ")
		assert.Equal(t, map[string]string{
			"key":  "value",
			"key2": "value2",
		}, headers)
	})

	t.Run("ignores invalid entries without equals", func(t *testing.T) {
		headers := parseHeaders("key1=value1,invalid,key2=value2")
		assert.Equal(t, map[string]string{
			"key1": "value1",
			"key2": "value2",
		}, headers)
	})

	t.Run("handles empty string", func(t *testing.T) {
		headers := parseHeaders("")
		assert.Empty(t, headers)
	})

	t.Run("handles value with equals sign", func(t *testing.T) {
		headers := parseHeaders("key=value=with=equals")
		assert.Equal(t, map[string]string{
			"key": "value=with=equals",
		}, headers)
	})

	t.Run("ignores entries with empty key", func(t *testing.T) {
		headers := parseHeaders("=value,key=value2")
		assert.Equal(t, map[string]string{
			"key": "value2",
		}, headers)
	})

	t.Run("handles special characters in values", func(t *testing.T) {
		headers := parseHeaders("Authorization=Bearer token123,Content-Type=application/json")
		assert.Equal(t, map[string]string{
			"Authorization": "Bearer token123",
			"Content-Type":  "application/json",
		}, headers)
	})
}

func TestResolveExportTimeout(t *testing.T) {
	t.Run("defaults to 30 seconds", func(t *testing.T) {
		timeout := resolveExportTimeout()
		assert.Equal(t, 30*time.Second, timeout)
	})

	t.Run("uses OTEL_EXPORTER_OTLP_TIMEOUT", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "5000")
		timeout := resolveExportTimeout()
		assert.Equal(t, 5*time.Second, timeout)
	})

	t.Run("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT wins over generic", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "5000")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", "10000")
		timeout := resolveExportTimeout()
		assert.Equal(t, 10*time.Second, timeout)
	})

	t.Run("falls back to default on invalid value", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", "invalid")
		timeout := resolveExportTimeout()
		assert.Equal(t, 30*time.Second, timeout)
	})
}

func TestParseTimeout(t *testing.T) {
	t.Run("parses milliseconds", func(t *testing.T) {
		timeout, err := parseTimeout("1000")
		assert.NoError(t, err)
		assert.Equal(t, time.Second, timeout)
	})

	t.Run("handles zero", func(t *testing.T) {
		timeout, err := parseTimeout("0")
		assert.NoError(t, err)
		assert.Equal(t, time.Duration(0), timeout)
	})

	t.Run("returns error for invalid input", func(t *testing.T) {
		_, err := parseTimeout("invalid")
		assert.Error(t, err)
	})

	t.Run("returns error for float", func(t *testing.T) {
		_, err := parseTimeout("1000.5")
		assert.Error(t, err)
	})
}

func TestResolveBLRPMaxQueueSize(t *testing.T) {
	t.Run("defaults to 2048", func(t *testing.T) {
		size := resolveBLRPMaxQueueSize()
		assert.Equal(t, 2048, size)
	})

	t.Run("uses OTEL_BLRP_MAX_QUEUE_SIZE", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_QUEUE_SIZE", "4096")
		size := resolveBLRPMaxQueueSize()
		assert.Equal(t, 4096, size)
	})

	t.Run("falls back to default on invalid value", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_QUEUE_SIZE", "invalid")
		size := resolveBLRPMaxQueueSize()
		assert.Equal(t, 2048, size)
	})

	t.Run("falls back to default on zero", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_QUEUE_SIZE", "0")
		size := resolveBLRPMaxQueueSize()
		assert.Equal(t, 2048, size)
	})

	t.Run("falls back to default on negative", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_QUEUE_SIZE", "-100")
		size := resolveBLRPMaxQueueSize()
		assert.Equal(t, 2048, size)
	})
}

func TestResolveBLRPScheduleDelay(t *testing.T) {
	t.Run("defaults to 1000ms", func(t *testing.T) {
		delay := resolveBLRPScheduleDelay()
		assert.Equal(t, 1000*time.Millisecond, delay)
	})

	t.Run("uses OTEL_BLRP_SCHEDULE_DELAY", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_SCHEDULE_DELAY", "500")
		delay := resolveBLRPScheduleDelay()
		assert.Equal(t, 500*time.Millisecond, delay)
	})

	t.Run("falls back to default on invalid value", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_SCHEDULE_DELAY", "invalid")
		delay := resolveBLRPScheduleDelay()
		assert.Equal(t, 1000*time.Millisecond, delay)
	})
}

func TestResolveBLRPExportTimeout(t *testing.T) {
	t.Run("defaults to 30000ms", func(t *testing.T) {
		timeout := resolveBLRPExportTimeout()
		assert.Equal(t, 30000*time.Millisecond, timeout)
	})

	t.Run("uses OTEL_BLRP_EXPORT_TIMEOUT", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_EXPORT_TIMEOUT", "15000")
		timeout := resolveBLRPExportTimeout()
		assert.Equal(t, 15000*time.Millisecond, timeout)
	})

	t.Run("falls back to default on invalid value", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_EXPORT_TIMEOUT", "invalid")
		timeout := resolveBLRPExportTimeout()
		assert.Equal(t, 30000*time.Millisecond, timeout)
	})
}

func TestResolveBLRPMaxExportBatchSize(t *testing.T) {
	t.Run("defaults to 512", func(t *testing.T) {
		size := resolveBLRPMaxExportBatchSize()
		assert.Equal(t, 512, size)
	})

	t.Run("uses OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", "1024")
		size := resolveBLRPMaxExportBatchSize()
		assert.Equal(t, 1024, size)
	})

	t.Run("falls back to default on invalid value", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", "invalid")
		size := resolveBLRPMaxExportBatchSize()
		assert.Equal(t, 512, size)
	})

	t.Run("falls back to default on zero", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", "0")
		size := resolveBLRPMaxExportBatchSize()
		assert.Equal(t, 512, size)
	})

	t.Run("falls back to default on negative", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", "-100")
		size := resolveBLRPMaxExportBatchSize()
		assert.Equal(t, 512, size)
	})
}
