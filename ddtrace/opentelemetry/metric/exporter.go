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
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
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
	envOTLPEndpoint           = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOTLPMetricsEndpoint    = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	envOTLPProtocol           = "OTEL_EXPORTER_OTLP_PROTOCOL"
	envOTLPMetricsProtocol    = "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL"
	envOTLPMetricsTemporality = "OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE"

	// DD environment variables for agent configuration
	envDDTraceAgentURL  = "DD_TRACE_AGENT_URL"
	envDDAgentHost      = "DD_AGENT_HOST"
	envDDTraceAgentPort = "DD_TRACE_AGENT_PORT"
)

// newDatadogOTLPExporter creates an OTLP exporter (HTTP or gRPC) configured with Datadog-specific defaults.
//
// Protocol selection priority:
// 1. OTEL_EXPORTER_OTLP_METRICS_PROTOCOL
// 2. OTEL_EXPORTER_OTLP_PROTOCOL
// 3. Default: http/protobuf
//
// Supported protocols:
// - "http/protobuf" or "http": HTTP with protobuf encoding
// - "grpc": gRPC
//
// Endpoint resolution priority:
// 1. OTEL_EXPORTER_OTLP_METRICS_ENDPOINT (highest priority)
// 2. OTEL_EXPORTER_OTLP_ENDPOINT
// 3. DD_TRACE_AGENT_URL hostname with appropriate port
// 4. DD_AGENT_HOST with appropriate port
// 5. localhost with default port (default)
func newDatadogOTLPExporter(ctx context.Context, httpOpts []otlpmetrichttp.Option, grpcOpts []otlpmetricgrpc.Option) (metric.Exporter, error) {
	// Determine protocol
	protocol := getOTLPProtocol()

	switch protocol {
	case "grpc":
		return newDatadogOTLPGRPCExporter(ctx, grpcOpts...)
	case "http/protobuf", "http":
		return newDatadogOTLPHTTPExporter(ctx, httpOpts...)
	default:
		log.Warn("Unknown OTLP protocol %q, defaulting to http/protobuf", protocol)
		return newDatadogOTLPHTTPExporter(ctx, httpOpts...)
	}
}

// getOTLPProtocol returns the OTLP protocol from environment variables.
// Priority: OTEL_EXPORTER_OTLP_METRICS_PROTOCOL > OTEL_EXPORTER_OTLP_PROTOCOL > "http/protobuf"
func getOTLPProtocol() string {
	// Check metrics-specific protocol first
	if protocol := env.Get(envOTLPMetricsProtocol); protocol != "" {
		return strings.ToLower(strings.TrimSpace(protocol))
	}
	// Fall back to general OTLP protocol
	if protocol := env.Get(envOTLPProtocol); protocol != "" {
		return strings.ToLower(strings.TrimSpace(protocol))
	}
	// Default to HTTP with protobuf
	return "http/protobuf"
}

// newDatadogOTLPHTTPExporter creates an OTLP HTTP exporter configured with Datadog-specific defaults.
func newDatadogOTLPHTTPExporter(ctx context.Context, opts ...otlpmetrichttp.Option) (metric.Exporter, error) {
	// Build exporter options with DD defaults
	exporterOpts := buildHTTPExporterOptions(opts...)

	// Create the OTLP HTTP exporter
	exporter, err := otlpmetrichttp.New(ctx, exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP HTTP metrics exporter: %w", err)
	}

	return exporter, nil
}

// newDatadogOTLPGRPCExporter creates an OTLP gRPC exporter configured with Datadog-specific defaults.
func newDatadogOTLPGRPCExporter(ctx context.Context, opts ...otlpmetricgrpc.Option) (metric.Exporter, error) {
	// Build exporter options with DD defaults
	exporterOpts := buildGRPCExporterOptions(opts...)

	// Create the OTLP gRPC exporter
	exporter, err := otlpmetricgrpc.New(ctx, exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP gRPC metrics exporter: %w", err)
	}

	return exporter, nil
}

// buildHTTPExporterOptions constructs the OTLP HTTP exporter options with DD-specific defaults
func buildHTTPExporterOptions(userOpts ...otlpmetrichttp.Option) []otlpmetrichttp.Option {
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
		endpoint, path, insecure := resolveOTLPEndpointHTTP()
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

// buildGRPCExporterOptions constructs the OTLP gRPC exporter options with DD-specific defaults
func buildGRPCExporterOptions(userOpts ...otlpmetricgrpc.Option) []otlpmetricgrpc.Option {
	opts := []otlpmetricgrpc.Option{
		// Set timeout
		otlpmetricgrpc.WithTimeout(30 * time.Second),
		// Set delta temporality as default (Datadog preference)
		otlpmetricgrpc.WithTemporalitySelector(datadogTemporalitySelector()),
		// Set retry config
		otlpmetricgrpc.WithRetry(datadogGRPCRetryConfig()),
	}

	// Only set endpoint if not already set by OTEL environment variables
	if !hasOTLPEndpointInEnv() {
		endpoint, insecure := resolveOTLPEndpointGRPC()
		opts = append(opts, otlpmetricgrpc.WithEndpoint(endpoint))
		if insecure {
			opts = append(opts, otlpmetricgrpc.WithInsecure())
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

// resolveOTLPEndpointHTTP determines the OTLP HTTP endpoint from DD agent configuration.
// Returns (endpoint, path, insecure) where:
// - endpoint is the host:port (e.g., "localhost:4318")
// - path is the URL path (e.g., "/v1/metrics")
// - insecure indicates whether to use http (true) or https (false)
//
// Priority order:
// 1. DD_TRACE_AGENT_URL with port changed to 4318
// 2. DD_AGENT_HOST:4318
// 3. localhost:4318 (default)
func resolveOTLPEndpointHTTP() (endpoint, path string, insecure bool) {
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

// resolveOTLPEndpointGRPC determines the OTLP gRPC endpoint from DD agent configuration.
// Returns (endpoint, insecure) where:
// - endpoint is the host:port (e.g., "localhost:4317")
// - insecure indicates whether to use grpc (true) or grpcs (false)
//
// Priority order:
// 1. DD_TRACE_AGENT_URL with port changed to 4317
// 2. DD_AGENT_HOST:4317
// 3. localhost:4317 (default)
func resolveOTLPEndpointGRPC() (endpoint string, insecure bool) {
	insecure = true // default to grpc (not grpcs)
	const defaultGRPCPort = "4317"

	// Check DD_TRACE_AGENT_URL first
	if agentURL := env.Get(envDDTraceAgentURL); agentURL != "" {
		u, err := url.Parse(agentURL)
		if err != nil {
			log.Warn("Failed to parse DD_TRACE_AGENT_URL for metrics: %s, using default", err.Error())
		} else {
			// Extract hostname from the agent URL and use port 4317 for gRPC
			hostname := u.Hostname()
			if hostname != "" {
				endpoint = net.JoinHostPort(hostname, defaultGRPCPort)
				// Preserve the scheme from DD_TRACE_AGENT_URL
				insecure = (u.Scheme == "http" || u.Scheme == "unix")
				log.Debug("Using OTLP gRPC metrics endpoint from DD_TRACE_AGENT_URL: %s", endpoint)
				return
			}
		}
	}

	// Check DD_AGENT_HOST
	if host := env.Get(envDDAgentHost); host != "" {
		endpoint = net.JoinHostPort(host, defaultGRPCPort)
		log.Debug("Using OTLP gRPC metrics endpoint from DD_AGENT_HOST: %s", endpoint)
		return
	}

	// Default to localhost:4317
	endpoint = net.JoinHostPort("localhost", defaultGRPCPort)
	log.Debug("Using default OTLP gRPC metrics endpoint: %s", endpoint)
	return
}

// datadogGRPCRetryConfig returns the retry configuration for OTLP gRPC exporter.
func datadogGRPCRetryConfig() otlpmetricgrpc.RetryConfig {
	return otlpmetricgrpc.RetryConfig{
		Enabled:         true,
		InitialInterval: 5 * time.Second,
		MaxInterval:     30 * time.Second,
		MaxElapsedTime:  5 * time.Minute,
	}
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
// It respects OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE if set, with one exception:
// UpDownCounter and ObservableUpDownCounter ALWAYS use Cumulative (even if DELTA is requested)
// because they represent state that can go up/down, not monotonic increases.
//
// Behavior:
// - UpDownCounter, ObservableUpDownCounter → Always CUMULATIVE (regardless of preference)
// - ObservableGauge → Always CUMULATIVE (regardless of preference)
// - Counter, ObservableCounter, Histogram → Respects preference, defaults to DELTA
func datadogTemporalitySelector() metric.TemporalitySelector {
	// Check if user has explicitly set temporality preference
	temporalityPref := strings.ToUpper(strings.TrimSpace(env.Get(envOTLPMetricsTemporality)))

	return func(kind metric.InstrumentKind) metricdata.Temporality {
		// UpDownCounter and Gauge ALWAYS use cumulative, regardless of preference
		// They represent current state, not monotonic changes
		switch kind {
		case metric.InstrumentKindUpDownCounter,
			metric.InstrumentKindObservableUpDownCounter,
			metric.InstrumentKindObservableGauge:
			return metricdata.CumulativeTemporality
		}

		// For monotonic instruments, respect the user's preference if set
		if temporalityPref != "" {
			switch temporalityPref {
			case "CUMULATIVE":
				return metricdata.CumulativeTemporality
			case "DELTA":
				return metricdata.DeltaTemporality
			}
		}

		// Default behavior for monotonic instruments: Delta
		switch kind {
		case metric.InstrumentKindCounter,
			metric.InstrumentKindHistogram,
			metric.InstrumentKindObservableCounter:
			return metricdata.DeltaTemporality
		default:
			return metricdata.DeltaTemporality
		}
	}
}
