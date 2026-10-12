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
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/json")
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
