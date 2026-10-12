// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/config"
	internallog "github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

func TestNewOTLPExporterProtocol(t *testing.T) {
	t.Cleanup(func() { config.CreateNew() })
	for protocol, expected := range map[string]sdklog.Exporter{
		"":        &otlploghttp.Exporter{},
		"grpc":    &otlploggrpc.Exporter{},
		"unknown": &otlploghttp.Exporter{},
	} {
		t.Run(protocol, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", protocol)
			config.CreateNew()
			exporter, err := newOTLPExporter(t.Context(), nil, nil)
			require.NoError(t, err)
			assert.IsType(t, expected, exporter.(*telemetryExporter).Exporter)
		})
	}
}

func TestOTLPJSONFallsBackToProtobuf(t *testing.T) {
	t.Setenv(envOTLPLogsProtocol, "http/json")
	config.CreateNew()
	t.Cleanup(func() { config.CreateNew() })
	logger := new(internallog.RecordLogger)
	defer internallog.UseLogger(logger)()
	previousLevel := internallog.GetLevel()
	internallog.SetLevel(internallog.LevelWarn)
	defer internallog.SetLevel(previousLevel)

	recorder := &telemetrytest.RecordClient{}
	defer telemetry.MockClient(recorder)()

	ctx := context.Background()
	exporter, err := newOTLPExporter(ctx, nil, nil)
	require.NoError(t, err)
	defer exporter.Shutdown(ctx)

	wrapped := exporter.(*telemetryExporter)
	assert.IsType(t, new(otlploghttp.Exporter), wrapped.Exporter)
	assert.Contains(t, strings.Join(logger.Logs(), "\n"), `Unknown OTLP logs protocol "http/json", defaulting to http/protobuf`)

	wrapped.telemetry.RecordLogRecords(1)
	key := telemetrytest.MetricKey{
		Namespace: telemetry.NamespaceTracers,
		Name:      "otel.log_records",
		Tags:      "encoding:protobuf,protocol:http",
		Kind:      "count",
	}
	require.Contains(t, recorder.Metrics, key)
	assert.Equal(t, float64(1), recorder.Metrics[key].Get())
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
	t.Cleanup(func() { config.CreateNew() })
	t.Run("defaults to localhost:4318", func(t *testing.T) {
		config.CreateNew()
		endpoint, path, insecure := resolveOTLPEndpointHTTP()
		assert.Equal(t, "localhost:4318", endpoint)
		assert.Equal(t, "/v1/logs", path)
		assert.True(t, insecure)
	})

	t.Run("uses DD_AGENT_HOST", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "agent.example.com")
		config.CreateNew()
		endpoint, path, insecure := resolveOTLPEndpointHTTP()
		assert.Equal(t, "agent.example.com:4318", endpoint)
		assert.Equal(t, "/v1/logs", path)
		assert.True(t, insecure)
	})

	t.Run("uses DD_TRACE_AGENT_URL", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "http://trace-agent:8126")
		config.CreateNew()
		endpoint, path, insecure := resolveOTLPEndpointHTTP()
		assert.Equal(t, "trace-agent:4318", endpoint)
		assert.Equal(t, "/v1/logs", path)
		assert.True(t, insecure)
	})

	t.Run("DD_TRACE_AGENT_URL wins over DD_AGENT_HOST", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "agent-host")
		t.Setenv("DD_TRACE_AGENT_URL", "http://trace-agent:8126")
		config.CreateNew()
		endpoint, _, _ := resolveOTLPEndpointHTTP()
		assert.Equal(t, "trace-agent:4318", endpoint)
	})

	t.Run("preserves https scheme", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "https://secure-agent:8126")
		config.CreateNew()
		_, _, insecure := resolveOTLPEndpointHTTP()
		assert.False(t, insecure, "https should result in insecure=false")
	})

	t.Run("handles unix socket scheme", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "unix:///var/run/datadog/apm.socket")
		config.CreateNew()
		_, _, insecure := resolveOTLPEndpointHTTP()
		assert.True(t, insecure, "unix scheme should result in insecure=true")
	})

	t.Run("handles IPv6 addresses", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "http://[::1]:8126")
		config.CreateNew()
		endpoint, _, _ := resolveOTLPEndpointHTTP()
		assert.Equal(t, "[::1]:4318", endpoint)
	})

	t.Run("unix socket falls back to DD_AGENT_HOST", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "unix:///var/run/datadog/apm.socket")
		t.Setenv("DD_AGENT_HOST", "agent-host")
		config.CreateNew()
		endpoint, _, insecure := resolveOTLPEndpointHTTP()
		assert.Equal(t, "agent-host:4318", endpoint)
		assert.True(t, insecure)
	})

	t.Run("ignores DD_TRACE_AGENT_URL with unsupported scheme", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "tcp://trace-agent:8126")
		t.Setenv("DD_AGENT_HOST", "agent-host")
		config.CreateNew()
		endpoint, _, _ := resolveOTLPEndpointHTTP()
		assert.Equal(t, "agent-host:4318", endpoint)
	})

	t.Run("uses programmatic agent URL", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "http://trace-agent:8126")
		config.CreateNew().SetAgentURL(&url.URL{Scheme: "https", Host: "code-agent:8126"}, telemetry.OriginCode)
		endpoint, _, insecure := resolveOTLPEndpointHTTP()
		assert.Equal(t, "code-agent:4318", endpoint)
		assert.False(t, insecure)
	})
}

func TestResolveOTLPEndpointGRPC(t *testing.T) {
	t.Cleanup(func() { config.CreateNew() })
	t.Run("defaults to localhost:4317", func(t *testing.T) {
		config.CreateNew()
		endpoint, insecure := resolveOTLPEndpointGRPC()
		assert.Equal(t, "localhost:4317", endpoint)
		assert.True(t, insecure)
	})

	t.Run("uses DD_AGENT_HOST", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "agent.example.com")
		config.CreateNew()
		endpoint, insecure := resolveOTLPEndpointGRPC()
		assert.Equal(t, "agent.example.com:4317", endpoint)
		assert.True(t, insecure)
	})

	t.Run("uses DD_TRACE_AGENT_URL", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "http://trace-agent:8126")
		config.CreateNew()
		endpoint, insecure := resolveOTLPEndpointGRPC()
		assert.Equal(t, "trace-agent:4317", endpoint)
		assert.True(t, insecure)
	})

	t.Run("DD_TRACE_AGENT_URL wins over DD_AGENT_HOST", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "agent-host")
		t.Setenv("DD_TRACE_AGENT_URL", "http://trace-agent:8126")
		config.CreateNew()
		endpoint, _ := resolveOTLPEndpointGRPC()
		assert.Equal(t, "trace-agent:4317", endpoint)
	})

	t.Run("preserves https scheme", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "https://secure-agent:8126")
		config.CreateNew()
		_, insecure := resolveOTLPEndpointGRPC()
		assert.False(t, insecure, "https should result in insecure=false")
	})
}

func TestResolveHeaders(t *testing.T) {
	t.Run("returns empty headers when none configured", func(t *testing.T) {
		headers := resolveHeaders()
		assert.Empty(t, headers)
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

	t.Run("falls back to generic when OTEL_EXPORTER_OTLP_LOGS_HEADERS has no valid entry", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "generic=value")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "invalid")
		headers := resolveHeaders()
		assert.Equal(t, map[string]string{
			"generic": "value",
		}, headers)
	})

	t.Run("falls back to generic when OTEL_EXPORTER_OTLP_LOGS_HEADERS has only invalid header names", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "generic=value")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "bad key=value")
		headers := resolveHeaders()
		assert.Equal(t, map[string]string{
			"generic": "value",
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

	t.Run("ignores entries with invalid header names", func(t *testing.T) {
		headers := parseHeaders("bad key=value,bad:key=value,key=value2")
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

func TestResolveHeadersPercentEncodedValues(t *testing.T) {
	for _, key := range []string{envOTLPHeaders, envOTLPLogsHeaders} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(envOTLPHeaders, "")
			t.Setenv(envOTLPLogsHeaders, "")
			t.Setenv(key, "api-key=hello%20world%2Cvalue%3D1,percent=%25,lowercase=%2c%3d,plus=a+b%2Bc,once=%2520,empty=,plain=unchanged,x%2Dkey=value,short=bad%2,invalid=bad%GG,trailing=bad%")

			assert.Equal(t, map[string]string{
				"api-key":   "hello world,value=1",
				"percent":   "%",
				"lowercase": ",=",
				"plus":      "a+b+c",
				"once":      "%20",
				"empty":     "",
				"plain":     "unchanged",
				"x%2Dkey":   "value",
			}, resolveHeaders())
		})
	}

	t.Run("signal overrides generic", func(t *testing.T) {
		t.Setenv(envOTLPHeaders, "api-key=generic%20value,generic-only=kept")
		t.Setenv(envOTLPLogsHeaders, "api-key=logs%20value")
		assert.Equal(t, map[string]string{"api-key": "logs value"}, resolveHeaders())
	})
}

func TestResolveExportTimeout(t *testing.T) {
	t.Run("defaults to 10 seconds", func(t *testing.T) {
		timeout := resolveExportTimeout()
		assert.Equal(t, 10*time.Second, timeout)
	})

	t.Run("uses OTEL_EXPORTER_OTLP_TIMEOUT", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "5000")
		timeout := resolveExportTimeout()
		assert.Equal(t, 5*time.Second, timeout)
	})

	t.Run("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT wins over generic", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "5000")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", "8000")
		timeout := resolveExportTimeout()
		assert.Equal(t, 8*time.Second, timeout)
	})

	t.Run("falls back to default on invalid value", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", "invalid")
		timeout := resolveExportTimeout()
		assert.Equal(t, 10*time.Second, timeout)
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
