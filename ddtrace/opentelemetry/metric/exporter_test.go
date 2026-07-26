// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
)

func TestNewConfigUsesCapturedMetricTiming(t *testing.T) {
	setConfigEnv(t, envOtelMetricExportInterval, "1234")
	setConfigEnv(t, envOtelMetricExportTimeout, "567")
	internalconfig.CreateNew()

	require.NoError(t, os.Unsetenv(envOtelMetricExportInterval))
	require.NoError(t, os.Unsetenv(envOtelMetricExportTimeout))

	cfg := newConfig()
	assert.Equal(t, 1234*time.Millisecond, cfg.exportInterval)
	assert.Equal(t, 567*time.Millisecond, cfg.exportTimeout)
}

func TestHTTPExporterUsesOTelSDKEnvParsing(t *testing.T) {
	requests := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requests <- r.Clone(r.Context()):
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	setConfigEnv(t, envOTLPMetricsEndpoint, server.URL)
	setConfigEnv(t, "OTEL_EXPORTER_OTLP_METRICS_HEADERS", "x-test-header=hello%20world")

	ctx := t.Context()
	exporter, err := newDatadogOTLPHTTPExporter(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = exporter.Shutdown(ctx) })

	require.NoError(t, exporter.Export(ctx, &metricdata.ResourceMetrics{}))
	select {
	case req := <-requests:
		assert.Equal(t, "/", req.URL.Path)
		assert.Equal(t, "hello world", req.Header.Get("x-test-header"))
	case <-time.After(time.Second):
		t.Fatal("OTLP exporter did not use the OTel SDK environment configuration")
	}
}

// TestResolveOTLPEndpoint_Default verifies that the default HTTP endpoint
// is localhost:4318 with /v1/metrics path and insecure connection.
func TestResolveOTLPEndpoint_Default(t *testing.T) {
	endpoint, path, insecure := resolveOTLPEndpointHTTP()
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
		{
			name:             "non-HTTP scheme",
			agentURL:         "grpc://agent.example.com:8126",
			expectedEndpoint: "agent.example.com:4318",
			expectedInsecure: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setConfigEnv(t, envDDTraceAgentURL, tt.agentURL)

			endpoint, path, insecure := resolveOTLPEndpointHTTP()
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
		setConfigEnv(t, envDDTraceAgentURL, "http://priority-agent:8126")
		setConfigEnv(t, envDDAgentHost, "fallback-agent")

		endpoint, _, _ := resolveOTLPEndpointHTTP()
		assert.Equal(t, "priority-agent:4318", endpoint)
	})

	t.Run("DD_AGENT_HOST as fallback", func(t *testing.T) {
		setConfigEnv(t, envDDAgentHost, "fallback-agent")

		endpoint, path, insecure := resolveOTLPEndpointHTTP()
		assert.Equal(t, "fallback-agent:4318", endpoint)
		assert.Equal(t, "/v1/metrics", path)
		assert.True(t, insecure)
	})

	t.Run("unix socket falls back to DD_AGENT_HOST", func(t *testing.T) {
		setConfigEnv(t, envDDTraceAgentURL, "unix:///var/run/datadog/apm.socket")
		setConfigEnv(t, envDDAgentHost, "fallback-agent")

		endpoint, _, _ := resolveOTLPEndpointHTTP()
		assert.Equal(t, "fallback-agent:4318", endpoint)
	})
}

// TestResolveOTLPEndpoint_InvalidURL verifies that when DD_TRACE_AGENT_URL is invalid,
// the endpoint resolution falls back to DD_AGENT_HOST.
func TestResolveOTLPEndpoint_InvalidURL(t *testing.T) {
	setConfigEnv(t, envDDTraceAgentURL, "://invalid-url")
	setConfigEnv(t, envDDAgentHost, "fallback-agent")

	// Should fall back to DD_AGENT_HOST when URL parsing fails
	endpoint, _, _ := resolveOTLPEndpointHTTP()
	assert.Equal(t, "fallback-agent:4318", endpoint)
}

// TestHasOTLPEndpointInEnv verifies detection of OTEL_EXPORTER_OTLP_ENDPOINT
// and OTEL_EXPORTER_OTLP_METRICS_ENDPOINT environment variables.
func TestHasOTLPEndpointInEnv(t *testing.T) {
	t.Run("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT set", func(t *testing.T) {
		setConfigEnv(t, envOTLPMetricsEndpoint, "http://custom:4318")
		assert.True(t, hasOTLPEndpointInEnv())
	})

	t.Run("OTEL_EXPORTER_OTLP_ENDPOINT set", func(t *testing.T) {
		setConfigEnv(t, envOTLPEndpoint, "http://custom:4318")
		assert.True(t, hasOTLPEndpointInEnv())
	})

	t.Run("no OTEL endpoint set", func(t *testing.T) {
		assert.False(t, hasOTLPEndpointInEnv())
	})
}

// TestGetOTLPProtocol verifies protocol selection from environment variables:
// - OTEL_EXPORTER_OTLP_METRICS_PROTOCOL takes priority
// - OTEL_EXPORTER_OTLP_PROTOCOL as fallback
// - Default: http/protobuf
func TestGetOTLPProtocol(t *testing.T) {
	t.Run("Default to http/protobuf", func(t *testing.T) {
		protocol := otlpProtocol()
		assert.Equal(t, defaultOTLPProtocol, protocol)
	})

	t.Run("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL takes priority", func(t *testing.T) {
		setConfigEnv(t, "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "grpc")
		setConfigEnv(t, "OTEL_EXPORTER_OTLP_PROTOCOL", "http")

		protocol := otlpProtocol()
		assert.Equal(t, "grpc", protocol)
	})

	t.Run("OTEL_EXPORTER_OTLP_PROTOCOL as fallback", func(t *testing.T) {
		setConfigEnv(t, "OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")

		protocol := otlpProtocol()
		assert.Equal(t, "grpc", protocol)
	})

	t.Run("Case insensitive", func(t *testing.T) {
		setConfigEnv(t, "OTEL_EXPORTER_OTLP_PROTOCOL", "GRPC")

		protocol := otlpProtocol()
		assert.Equal(t, "grpc", protocol)
	})

	t.Run("Trim whitespace", func(t *testing.T) {
		setConfigEnv(t, "OTEL_EXPORTER_OTLP_PROTOCOL", "  http/protobuf  ")

		protocol := otlpProtocol()
		assert.Equal(t, defaultOTLPProtocol, protocol)
	})
}

// TestResolveOTLPEndpointGRPC verifies gRPC endpoint resolution with default port 4317
// and proper handling of DD_TRACE_AGENT_URL and DD_AGENT_HOST.
func TestResolveOTLPEndpointGRPC(t *testing.T) {
	t.Run("Default to localhost:4317", func(t *testing.T) {
		endpoint, insecure := resolveOTLPEndpointGRPC()
		assert.Equal(t, "localhost:4317", endpoint)
		assert.True(t, insecure)
	})

	t.Run("DD_TRACE_AGENT_URL", func(t *testing.T) {
		setConfigEnv(t, envDDTraceAgentURL, "http://custom-agent:8126")

		endpoint, insecure := resolveOTLPEndpointGRPC()
		assert.Equal(t, "custom-agent:4317", endpoint)
		assert.True(t, insecure)
	})

	t.Run("non-HTTP DD_TRACE_AGENT_URL scheme", func(t *testing.T) {
		setConfigEnv(t, envDDTraceAgentURL, "grpc://custom-agent:8126")

		endpoint, insecure := resolveOTLPEndpointGRPC()
		assert.Equal(t, "custom-agent:4317", endpoint)
		assert.False(t, insecure)
	})

	t.Run("DD_AGENT_HOST", func(t *testing.T) {
		setConfigEnv(t, envDDAgentHost, "custom-host")

		endpoint, _ := resolveOTLPEndpointGRPC()
		assert.Equal(t, "custom-host:4317", endpoint)
	})

	t.Run("unix socket falls back to DD_AGENT_HOST", func(t *testing.T) {
		setConfigEnv(t, envDDTraceAgentURL, "unix:///var/run/datadog/apm.socket")
		setConfigEnv(t, envDDAgentHost, "custom-host")

		endpoint, _ := resolveOTLPEndpointGRPC()
		assert.Equal(t, "custom-host:4317", endpoint)
	})
}

// TestDeltaTemporalitySelector verifies temporality selection per OTel spec:
// - Monotonic instruments (Counter, Histogram, ObservableCounter) → Delta
// - Non-monotonic instruments (UpDownCounter, ObservableUpDownCounter, ObservableGauge) → Cumulative
// - OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE overrides for monotonic instruments only
func TestDeltaTemporalitySelector(t *testing.T) {
	t.Run("Default behavior (no env var set)", func(t *testing.T) {
		selector := deltaTemporalitySelector()

		// Test temporality for each instrument kind per OTel spec:
		// - Monotonic instruments (Counter, ObservableCounter, Histogram) → Delta
		// - Non-monotonic instruments (UpDownCounter, ObservableUpDownCounter, ObservableGauge) → Cumulative
		tests := []struct {
			name                string
			kind                metric.InstrumentKind
			expectedTemporality metricdata.Temporality
		}{
			// Monotonic instruments - should use Delta
			{"Counter", metric.InstrumentKindCounter, metricdata.DeltaTemporality},
			{"Histogram", metric.InstrumentKindHistogram, metricdata.DeltaTemporality},
			{"ObservableCounter", metric.InstrumentKindObservableCounter, metricdata.DeltaTemporality},

			// Non-monotonic instruments - should use Cumulative
			{"UpDownCounter", metric.InstrumentKindUpDownCounter, metricdata.CumulativeTemporality},
			{"ObservableUpDownCounter", metric.InstrumentKindObservableUpDownCounter, metricdata.CumulativeTemporality},
			{"ObservableGauge", metric.InstrumentKindObservableGauge, metricdata.CumulativeTemporality},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				temporality := selector(tt.kind)
				assert.Equal(t, tt.expectedTemporality, temporality, "Incorrect temporality for %s", tt.name)
			})
		}
	})

	t.Run("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=CUMULATIVE", func(t *testing.T) {
		setConfigEnv(t, "OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "CUMULATIVE")
		selector := deltaTemporalitySelector()

		// All instruments should use cumulative when explicitly set
		tests := []metric.InstrumentKind{
			metric.InstrumentKindCounter,
			metric.InstrumentKindHistogram,
			metric.InstrumentKindObservableCounter,
			metric.InstrumentKindUpDownCounter,
			metric.InstrumentKindObservableUpDownCounter,
			metric.InstrumentKindObservableGauge,
		}

		for _, kind := range tests {
			got := selector(kind)
			assert.Equal(t, metricdata.CumulativeTemporality, got, "Expected CUMULATIVE for %v", kind)
		}
	})

	t.Run("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=DELTA", func(t *testing.T) {
		setConfigEnv(t, "OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "DELTA")
		selector := deltaTemporalitySelector()

		// Monotonic instruments should use delta
		deltaTests := []metric.InstrumentKind{
			metric.InstrumentKindCounter,
			metric.InstrumentKindHistogram,
			metric.InstrumentKindObservableCounter,
		}
		for _, kind := range deltaTests {
			got := selector(kind)
			assert.Equal(t, metricdata.DeltaTemporality, got, "Expected DELTA for %v", kind)
		}

		// UpDownCounter and Gauge should ALWAYS use cumulative (even when DELTA is requested)
		cumulativeTests := []metric.InstrumentKind{
			metric.InstrumentKindUpDownCounter,
			metric.InstrumentKindObservableUpDownCounter,
			metric.InstrumentKindObservableGauge,
		}
		for _, kind := range cumulativeTests {
			got := selector(kind)
			assert.Equal(t, metricdata.CumulativeTemporality, got, "Expected CUMULATIVE for %v (even with DELTA preference)", kind)
		}
	})

	t.Run("Case insensitive", func(t *testing.T) {
		setConfigEnv(t, "OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "cumulative")
		selector := deltaTemporalitySelector()

		got := selector(metric.InstrumentKindCounter)
		assert.Equal(t, metricdata.CumulativeTemporality, got)
	})

	t.Run("With whitespace", func(t *testing.T) {
		setConfigEnv(t, "OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "  CUMULATIVE  ")
		selector := deltaTemporalitySelector()

		got := selector(metric.InstrumentKindCounter)
		assert.Equal(t, metricdata.CumulativeTemporality, got)
	})
}

// TestTemporalitySelectorHonored verifies that a user-configured temporality selector
// overrides the hardcoded delta default when passed as the last exporter option.
// This covers the fix that bridges cfg.temporalitySelector → exporter options in
// NewMeterProviderWithContext: the build functions use last-wins ordering, so appending
// the selector after the delta default is sufficient.
func TestTemporalitySelectorHonored(t *testing.T) {
	ctx := context.Background()

	t.Run("cumulative selector overrides delta default for monotonic counter", func(t *testing.T) {
		// Mirror what the fixed NewMeterProviderWithContext does when
		// WithCumulativeTemporality() is set: append the selector as the last option.
		httpOpts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithTemporalitySelector(cumulativeTemporalitySelector()),
		}
		exp, err := newDatadogOTLPExporter(ctx, httpOpts, nil)
		require.NoError(t, err)
		defer exp.Shutdown(ctx) //nolint:errcheck

		assert.Equal(t, metricdata.CumulativeTemporality, exp.Temporality(metric.InstrumentKindCounter))
	})

	t.Run("default (no selector option) gives delta for monotonic counter", func(t *testing.T) {
		exp, err := newDatadogOTLPExporter(ctx, nil, nil)
		require.NoError(t, err)
		defer exp.Shutdown(ctx) //nolint:errcheck

		assert.Equal(t, metricdata.DeltaTemporality, exp.Temporality(metric.InstrumentKindCounter))
	})
}
