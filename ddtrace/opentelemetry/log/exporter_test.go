// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	collectorlog "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/DataDog/dd-trace-go/v2/internal/config"
)

func TestResolveOTLPEndpointHTTP(t *testing.T) {
	config.SetUseFreshConfig(true)
	t.Cleanup(func() { config.SetUseFreshConfig(false) })
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
	config.SetUseFreshConfig(true)
	t.Cleanup(func() { config.SetUseFreshConfig(false) })
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

type logsConfigCollector struct {
	collectorlog.UnimplementedLogsServiceServer
	headers chan string
}

func (s *logsConfigCollector) Export(ctx context.Context, _ *collectorlog.ExportLogsServiceRequest) (*collectorlog.ExportLogsServiceResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get("x-config-source")
	value := ""
	if len(values) > 0 {
		value = values[0]
	}
	s.headers <- value
	return &collectorlog.ExportLogsServiceResponse{}, nil
}

func TestExporterUsesCentralizedConfig(t *testing.T) {
	for _, protocol := range []string{"http/protobuf", "grpc"} {
		t.Run(protocol, func(t *testing.T) {
			for _, tc := range []struct {
				name         string
				headers      string
				userOverride bool
				expected     string
			}{
				{name: "configured headers", headers: "x-config-source=centralized", expected: "centralized"},
				{name: "empty headers"},
				{name: "user options", headers: "x-config-source=centralized", userOverride: true, expected: "user"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Cleanup(func() { config.CreateNew() })
					captured := make(chan string, 1)
					var endpoint string
					if protocol == "grpc" {
						listener, err := net.Listen("tcp", "127.0.0.1:0")
						require.NoError(t, err)
						server := grpc.NewServer()
						collectorlog.RegisterLogsServiceServer(server, &logsConfigCollector{headers: captured})
						t.Cleanup(server.Stop)
						go func() { _ = server.Serve(listener) }()
						endpoint = "http://" + listener.Addr().String()
					} else {
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							assert.Equal(t, "/v1/logs", r.URL.Path)
							captured <- r.Header.Get("x-config-source")
							w.Header().Set("Content-Type", "application/x-protobuf")
						}))
						t.Cleanup(server.Close)
						endpoint = server.URL
					}
					t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", protocol)
					t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", endpoint)
					t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "")
					t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", tc.headers)
					config.CreateNew()

					t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "invalid")
					t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "http://invalid:1")
					t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "x-config-source=environment")
					var httpOpts []otlploghttp.Option
					var grpcOpts []otlploggrpc.Option
					if tc.userOverride {
						headers := map[string]string{"x-config-source": "user"}
						httpOpts = append(httpOpts, otlploghttp.WithHeaders(headers))
						grpcOpts = append(grpcOpts, otlploggrpc.WithHeaders(headers))
					}
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					exporter, err := newOTLPExporter(ctx, httpOpts, grpcOpts)
					require.NoError(t, err)
					t.Cleanup(func() { assert.NoError(t, exporter.Shutdown(context.Background())) })
					require.NoError(t, exporter.Export(ctx, []sdklog.Record{{}}))
					select {
					case value := <-captured:
						assert.Equal(t, tc.expected, value)
					case <-ctx.Done():
						t.Fatal("collector did not receive a log export")
					}
				})
			}
		})
	}
}
