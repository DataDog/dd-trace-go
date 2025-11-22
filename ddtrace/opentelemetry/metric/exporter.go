// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

const (
	// Default OTLP HTTP endpoint for Datadog
	defaultOTLPEndpoint = "http://localhost:4318"
	defaultOTLPPath     = "/v1/metrics"
	defaultOTLPPort     = "4318"

	// OTLP environment variables
	envOTLPEndpoint        = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOTLPMetricsEndpoint = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	envOTLPProtocol        = "OTEL_EXPORTER_OTLP_PROTOCOL"
	envOTLPMetricsProtocol = "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL"

	// DD environment variables for agent configuration
	envDDTraceAgentURL  = "DD_TRACE_AGENT_URL"
	envDDAgentHost      = "DD_AGENT_HOST"
	envDDTraceAgentPort = "DD_TRACE_AGENT_PORT"
)

// newDatadogOTLPExporter creates an OTLP HTTP exporter configured with Datadog-specific defaults.
//
// Default configuration:
// - Protocol: http/protobuf
// - Endpoint: Determined from DD_TRACE_AGENT_URL or DD_AGENT_HOST with port 4318
// - Content-Type: application/x-protobuf
// - Retry logic: Retries on 429, 502, 503, 504; Does NOT retry on 400 or partial success
// - Honor Retry-After headers
//
// Endpoint resolution priority:
// 1. OTEL_EXPORTER_OTLP_METRICS_ENDPOINT (highest priority)
// 2. OTEL_EXPORTER_OTLP_ENDPOINT
// 3. DD_TRACE_AGENT_URL hostname with port 4318
// 4. DD_AGENT_HOST:4318
// 5. localhost:4318 (default)
func newDatadogOTLPExporter(ctx context.Context, opts ...otlpmetrichttp.Option) (metric.Exporter, error) {
	// Build exporter options with DD defaults
	exporterOpts := buildExporterOptions(opts...)

	// Create the OTLP HTTP exporter
	exporter, err := otlpmetrichttp.New(ctx, exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP metrics exporter: %w", err)
	}

	return exporter, nil
}

// buildExporterOptions constructs the OTLP exporter options with DD-specific defaults
func buildExporterOptions(userOpts ...otlpmetrichttp.Option) []otlpmetrichttp.Option {
	opts := []otlpmetrichttp.Option{
		// Set retry configuration
		otlpmetrichttp.WithRetry(datadogRetryConfig()),
		// Set timeout
		otlpmetrichttp.WithTimeout(30 * time.Second),
		// Set delta temporality as default (Datadog preference)
		otlpmetrichttp.WithTemporalitySelector(datadogTemporalitySelector()),
	}

	// Only set endpoint if not already set by OTEL environment variables
	if !hasOTLPEndpointInEnv() {
		endpoint, path, insecure := resolveOTLPEndpoint()
		opts = append(opts, otlpmetrichttp.WithEndpoint(endpoint))
		opts = append(opts, otlpmetrichttp.WithURLPath(path))
		if insecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		}
	}

	// Add user-provided options last so they can override defaults
	opts = append(opts, userOpts...)

	return opts
}

// hasOTLPEndpointInEnv checks if OTLP endpoint is configured via OTEL environment variables
func hasOTLPEndpointInEnv() bool {
	if v := env.Get(envOTLPMetricsEndpoint); v != "" {
		return true
	}
	if v := env.Get(envOTLPEndpoint); v != "" {
		return true
	}
	return false
}

// resolveOTLPEndpoint determines the OTLP endpoint from DD agent configuration.
// Returns (endpoint, path, insecure) where:
// - endpoint is the host:port (e.g., "localhost:4318")
// - path is the URL path (e.g., "/v1/metrics")
// - insecure indicates whether to use http (true) or https (false)
//
// Priority order:
// 1. DD_TRACE_AGENT_URL with port changed to 4318
// 2. DD_AGENT_HOST:4318
// 3. localhost:4318 (default)
func resolveOTLPEndpoint() (endpoint, path string, insecure bool) {
	path = defaultOTLPPath
	insecure = true // default to http

	// Check DD_TRACE_AGENT_URL first
	if agentURL := env.Get(envDDTraceAgentURL); agentURL != "" {
		u, err := url.Parse(agentURL)
		if err != nil {
			log.Warn("Failed to parse DD_TRACE_AGENT_URL for metrics: %s, using default", err.Error())
		} else {
			// Extract hostname from the agent URL and use port 4318
			hostname := u.Hostname()
			if hostname != "" {
				endpoint = net.JoinHostPort(hostname, defaultOTLPPort)
				// Preserve the scheme from DD_TRACE_AGENT_URL
				insecure = (u.Scheme == "http" || u.Scheme == "unix")
				log.Debug("Using OTLP metrics endpoint from DD_TRACE_AGENT_URL: %s", endpoint)
				return
			}
		}
	}

	// Check DD_AGENT_HOST
	if host := env.Get(envDDAgentHost); host != "" {
		endpoint = net.JoinHostPort(host, defaultOTLPPort)
		insecure = true
		return
	}

	// Default to localhost:4318
	endpoint = "localhost:4318"
	insecure = true
	return
}

// datadogRetryConfig returns a retry configuration that matches Datadog requirements
// The OTLP exporter will automatically retry on 429, 502, 503, 504 and honor Retry-After headers
func datadogRetryConfig() otlpmetrichttp.RetryConfig {
	return otlpmetrichttp.RetryConfig{
		Enabled:         true,
		InitialInterval: 1 * time.Second,
		MaxInterval:     30 * time.Second,
		MaxElapsedTime:  5 * time.Minute,
	}
}

// datadogTemporalitySelector returns a temporality selector configured with Datadog defaults.
// Default temporality is Delta, but non-monotonic instruments use Cumulative per OTel spec:
// - Monotonic counters (Counter, ObservableCounter) → Delta (differences between measurements)
// - Non-monotonic counters (UpDownCounter, ObservableUpDownCounter) → Cumulative (absolute values)
// - Gauges (ObservableGauge) → Cumulative (point-in-time values)
// - Histograms → Delta (distribution of measurements)
func datadogTemporalitySelector() metric.TemporalitySelector {
	return func(kind metric.InstrumentKind) metricdata.Temporality {
		switch kind {
		case metric.InstrumentKindCounter,
			metric.InstrumentKindHistogram,
			metric.InstrumentKindObservableCounter:
			// Monotonic instruments use delta temporality
			return metricdata.DeltaTemporality

		case metric.InstrumentKindUpDownCounter,
			metric.InstrumentKindObservableUpDownCounter,
			metric.InstrumentKindObservableGauge:
			// Non-monotonic instruments use cumulative temporality
			return metricdata.CumulativeTemporality

		default:
			// Default to delta temporality
			return metricdata.DeltaTemporality
		}
	}
}
