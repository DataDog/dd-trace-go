// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

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
			agentURL:         "http://agent.example.com:8126/v0.4/traces",
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
			t.Setenv(envDDTraceAgentURL, tt.agentURL)

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
		t.Setenv(envDDTraceAgentURL, "http://priority-agent:8126")
		t.Setenv(envDDAgentHost, "fallback-agent")

		endpoint, _, _ := resolveOTLPEndpointHTTP()
		assert.Equal(t, "priority-agent:4318", endpoint)
	})

	t.Run("DD_AGENT_HOST as fallback", func(t *testing.T) {
		t.Setenv(envDDAgentHost, "fallback-agent")

		endpoint, path, insecure := resolveOTLPEndpointHTTP()
		assert.Equal(t, "fallback-agent:4318", endpoint)
		assert.Equal(t, "/v1/metrics", path)
		assert.True(t, insecure)
	})
}

// TestResolveOTLPEndpoint_InvalidURL verifies that when DD_TRACE_AGENT_URL is invalid,
// the endpoint resolution falls back to DD_AGENT_HOST.
func TestResolveOTLPEndpoint_InvalidURL(t *testing.T) {
	t.Setenv(envDDTraceAgentURL, "://invalid-url")
	t.Setenv(envDDAgentHost, "fallback-agent")

	// Should fall back to DD_AGENT_HOST when URL parsing fails
	endpoint, _, _ := resolveOTLPEndpointHTTP()
	assert.Equal(t, "fallback-agent:4318", endpoint)
}

// TestHasOTLPEndpointInEnv verifies detection of OTEL_EXPORTER_OTLP_ENDPOINT
// and OTEL_EXPORTER_OTLP_METRICS_ENDPOINT environment variables.
func TestHasOTLPEndpointInEnv(t *testing.T) {
	t.Run("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT set", func(t *testing.T) {
		t.Setenv(envOTLPMetricsEndpoint, "http://custom:4318")
		assert.True(t, hasOTLPEndpointInEnv())
	})

	t.Run("OTEL_EXPORTER_OTLP_ENDPOINT set", func(t *testing.T) {
		t.Setenv(envOTLPEndpoint, "http://custom:4318")
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
		t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "grpc")
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http")

		protocol := otlpProtocol()
		assert.Equal(t, "grpc", protocol)
	})

	t.Run("OTEL_EXPORTER_OTLP_PROTOCOL as fallback", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")

		protocol := otlpProtocol()
		assert.Equal(t, "grpc", protocol)
	})

	t.Run("Case insensitive", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "GRPC")

		protocol := otlpProtocol()
		assert.Equal(t, "grpc", protocol)
	})

	t.Run("Trim whitespace", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "  http/protobuf  ")

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
		t.Setenv(envDDTraceAgentURL, "http://custom-agent:8126")

		endpoint, insecure := resolveOTLPEndpointGRPC()
		assert.Equal(t, "custom-agent:4317", endpoint)
		assert.True(t, insecure)
	})

	t.Run("DD_AGENT_HOST", func(t *testing.T) {
		t.Setenv(envDDAgentHost, "custom-host")

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
		t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "CUMULATIVE")
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
		t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "DELTA")
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
		t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "cumulative")
		selector := deltaTemporalitySelector()

		got := selector(metric.InstrumentKindCounter)
		assert.Equal(t, metricdata.CumulativeTemporality, got)
	})

	t.Run("With whitespace", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "  CUMULATIVE  ")
		selector := deltaTemporalitySelector()

		got := selector(metric.InstrumentKindCounter)
		assert.Equal(t, metricdata.CumulativeTemporality, got)
	})
}
