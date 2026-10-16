// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"net/http"
	"net/http/httptest"
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
	for protocol, expected := range map[string]struct {
		exporter sdklog.Exporter
		tags     string
	}{
		"":              {&otlploghttp.Exporter{}, "encoding:protobuf,protocol:http"},
		"http/protobuf": {&otlploghttp.Exporter{}, "encoding:protobuf,protocol:http"},
		"grpc":          {&otlploggrpc.Exporter{}, "encoding:protobuf,protocol:grpc"},
		"http/json":     {&otlploghttp.Exporter{}, "encoding:protobuf,protocol:http"},
	} {
		t.Run(protocol, func(t *testing.T) {
			recorder := &telemetrytest.RecordClient{}
			defer telemetry.MockClient(recorder)()
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", protocol)
			config.CreateNew()

			exporter, err := newOTLPExporter(t.Context(), nil, nil)
			require.NoError(t, err)
			defer exporter.Shutdown(t.Context())
			te := exporter.(*telemetryExporter)
			assert.IsType(t, expected.exporter, te.Exporter)

			te.telemetry.RecordLogRecords(1)
			assert.Contains(t, recorder.Metrics, telemetrytest.MetricKey{
				Namespace: telemetry.NamespaceTracers,
				Name:      "otel.log_records",
				Tags:      expected.tags,
				Kind:      "count",
			})
		})
	}
}

func TestNewOTLPExporterHTTPConfig(t *testing.T) {
	t.Cleanup(func() { config.CreateNew() })
	t.Run("endpoint and headers", func(t *testing.T) {
		requests := make(chan *http.Request, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			requests <- r
		}))
		defer srv.Close()
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", srv.URL+"/custom")
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "api-key=secret%20value")
		config.CreateNew()

		exporter, err := newOTLPExporter(t.Context(), nil, nil)
		require.NoError(t, err)
		defer exporter.Shutdown(t.Context())
		require.NoError(t, exporter.Export(t.Context(), []sdklog.Record{{}}))

		r := <-requests
		assert.Equal(t, "/custom", r.URL.Path)
		assert.Equal(t, "secret value", r.Header.Get("api-key"))
	})

	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			<-release
		}))
		defer srv.Close()
		defer close(release)
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", srv.URL)
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", "50")
		config.CreateNew()

		noRetry := []otlploghttp.Option{otlploghttp.WithRetry(otlploghttp.RetryConfig{Enabled: false})}
		exporter, err := newOTLPExporter(t.Context(), noRetry, nil)
		require.NoError(t, err)
		defer exporter.Shutdown(t.Context())
		start := time.Now()
		assert.Error(t, exporter.Export(t.Context(), []sdklog.Record{{}}))
		assert.Less(t, time.Since(start), 5*time.Second)
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

func TestOTLPLogsDuplicatePathWarning(t *testing.T) {
	t.Cleanup(func() { config.CreateNew() })
	previousLevel := internallog.GetLevel()
	internallog.SetLevel(internallog.LevelDebug)
	t.Cleanup(func() { internallog.SetLevel(previousLevel) })
	for _, tc := range []struct {
		name            string
		signalEndpoint  string
		genericEndpoint string
		grpc            bool
		userOpts        []otlploghttp.Option
		wantWarnings    int
	}{
		{name: "generic signal suffix", genericEndpoint: "http://collector/v1/logs", wantWarnings: 1},
		{name: "generic trailing slash", genericEndpoint: "http://collector/v1/logs/", wantWarnings: 1},
		{name: "generic base", genericEndpoint: "http://collector/base"},
		{name: "signal custom path overrides generic", signalEndpoint: "http://collector/custom", genericEndpoint: "http://collector/v1/logs"},
		{name: "signal root", signalEndpoint: "http://collector/"},
		{name: "same URL in both sources", signalEndpoint: "http://collector/v1/logs", genericEndpoint: "http://collector/v1/logs"},
		{name: "invalid signal falls back to generic", signalEndpoint: "http://%invalid", genericEndpoint: "http://collector/v1/logs", wantWarnings: 1},
		{name: "explicit URL", genericEndpoint: "http://collector/v1/logs", userOpts: []otlploghttp.Option{otlploghttp.WithEndpointURL("http://other/v1/logs")}},
		{name: "explicit path", genericEndpoint: "http://collector/v1/logs", userOpts: []otlploghttp.Option{otlploghttp.WithURLPath("/v1/logs")}},
		{name: "grpc", genericEndpoint: "http://collector/v1/logs", grpc: true},
		{name: "credentials omitted", genericEndpoint: "https://secret-user:secret-password@collector/secret-path/v1/logs?token=secret-query", wantWarnings: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", tc.signalEndpoint)
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", tc.genericEndpoint)
			config.CreateNew()
			logger := new(internallog.RecordLogger)
			defer internallog.UseLogger(logger)()
			if tc.grpc {
				buildGRPCExporterOptions()
			} else {
				buildHTTPExporterOptions(tc.userOpts...)
			}
			var warnings []string
			for _, entry := range logger.Logs() {
				if strings.Contains(entry, " WARN: ") {
					warnings = append(warnings, entry)
				}
			}
			require.Len(t, warnings, tc.wantWarnings)
			for _, warning := range warnings {
				assert.Contains(t, warning, "OTEL_EXPORTER_OTLP_ENDPOINT")
				assert.Contains(t, warning, "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT")
				assert.NotContains(t, warning, "secret-")
			}
		})
	}
}
