// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

const (
	// Default OTLP endpoint for Datadog Agent logs
	defaultOTLPHTTPPort = "4318"
	defaultOTLPGRPCPort = "4317"
	defaultOTLPLogsPath = "/v1/logs"

	// DD environment variables for agent configuration
	envDDAgentHost = "DD_AGENT_HOST"

	// HTTP retry configuration
	// InitialInterval: Start with 1s backoff to quickly recover from transient failures
	// MaxInterval: Cap at 30s to avoid excessive delays while still being patient
	// MaxElapsedTime: Give up after 5 minutes to prevent indefinite hangs
	httpRetryInitialInterval = 1 * time.Second
	httpRetryMaxInterval     = 30 * time.Second
	httpRetryMaxElapsedTime  = 5 * time.Minute

	// gRPC retry configuration
	// InitialInterval: Start with 5s backoff (longer than HTTP due to connection overhead)
	// MaxInterval: Cap at 30s to avoid excessive delays while still being patient
	// MaxElapsedTime: Give up after 5 minutes to prevent indefinite hangs
	grpcRetryInitialInterval = 5 * time.Second
	grpcRetryMaxInterval     = 30 * time.Second
	grpcRetryMaxElapsedTime  = 5 * time.Minute

	// Protocol and encoding constants for telemetry tagging
	protocolHTTP     = "http"
	protocolGRPC     = "grpc"
	encodingProtobuf = "protobuf"
)

// telemetryExporter wraps an sdklog.Exporter to track log record exports.
type telemetryExporter struct {
	sdklog.Exporter
	telemetry *LogsExportTelemetry
}

// Compile-time check that telemetryExporter implements sdklog.Exporter.
var _ sdklog.Exporter = (*telemetryExporter)(nil)

// Export implements sdklog.Exporter.
func (e *telemetryExporter) Export(ctx context.Context, records []sdklog.Record) error {
	err := e.Exporter.Export(ctx, records)
	// Record the number of log records exported (success or failure)
	// This matches the RFC requirement to track log_records counter
	if len(records) > 0 {
		e.telemetry.RecordLogRecords(len(records))
	}
	return err
}

// newOTLPExporter creates an OTLP exporter (HTTP or gRPC) configured with Datadog-specific defaults.
//
// The protocol comes from config.OTLPLogsProtocol, which only returns supported protocols:
// - "http/protobuf" or "http": HTTP with protobuf encoding (default)
// - "grpc": gRPC
//
// Endpoint resolution priority:
// 1. config.OTLPLogsEndpoint (highest priority)
// 2. DD_TRACE_AGENT_URL hostname with appropriate port
// 3. DD_AGENT_HOST with appropriate port
// 4. localhost with default port (default)
func newOTLPExporter(ctx context.Context, httpOpts []otlploghttp.Option, grpcOpts []otlploggrpc.Option) (sdklog.Exporter, error) {
	// Determine protocol
	protocol := config.Get().OTLPLogsProtocol()

	var exporter sdklog.Exporter
	var err error
	protocolTag := protocolHTTP

	switch protocol {
	case "grpc":
		exporter, err = newOTLPGRPCExporter(ctx, grpcOpts...)
		protocolTag = protocolGRPC
	default:
		exporter, err = newOTLPHTTPExporter(ctx, httpOpts...)
	}

	if err != nil {
		return nil, err
	}

	// Wrap the exporter with telemetry tracking
	return &telemetryExporter{
		Exporter:  exporter,
		telemetry: NewLogsExportTelemetry(protocolTag, encodingProtobuf),
	}, nil
}

// newOTLPHTTPExporter creates an OTLP HTTP exporter configured with Datadog-specific defaults.
func newOTLPHTTPExporter(ctx context.Context, opts ...otlploghttp.Option) (sdklog.Exporter, error) {
	// Build exporter options with DD defaults
	exporterOpts := buildHTTPExporterOptions(opts...)

	// Create the OTLP HTTP exporter
	exporter, err := otlploghttp.New(ctx, exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP HTTP logs exporter: %w", err)
	}

	return exporter, nil
}

// newOTLPGRPCExporter creates an OTLP gRPC exporter configured with Datadog-specific defaults.
func newOTLPGRPCExporter(ctx context.Context, opts ...otlploggrpc.Option) (sdklog.Exporter, error) {
	// Build exporter options with DD defaults
	exporterOpts := buildGRPCExporterOptions(opts...)

	// Create the OTLP gRPC exporter
	exporter, err := otlploggrpc.New(ctx, exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP gRPC logs exporter: %w", err)
	}

	return exporter, nil
}

// buildHTTPExporterOptions constructs the OTLP HTTP exporter options with DD-specific defaults
func buildHTTPExporterOptions(userOpts ...otlploghttp.Option) []otlploghttp.Option {
	cfg := config.Get()
	opts := []otlploghttp.Option{
		// Set timeout
		otlploghttp.WithTimeout(cfg.OTLPLogsTimeout()),
		// Set retry configuration
		otlploghttp.WithRetry(httpRetryConfig()),
	}

	if rawEndpoint := cfg.OTLPLogsEndpoint(); rawEndpoint != "" {
		// Sanitize the URL to handle trailing slashes correctly
		sanitizedURL := sanitizeOTLPEndpoint(rawEndpoint, cfg.OTLPLogsEndpointIsGeneric())
		opts = append(opts, otlploghttp.WithEndpointURL(sanitizedURL))
		log.Debug("Using sanitized OTLP logs endpoint: %s", sanitizedURL)
	} else {
		// Use DD agent configuration as default
		endpoint, path, insecure := resolveOTLPEndpointHTTP()
		opts = append(opts, otlploghttp.WithEndpoint(endpoint))
		opts = append(opts, otlploghttp.WithURLPath(path))
		if insecure {
			opts = append(opts, otlploghttp.WithInsecure())
		}
	}

	// Always set headers so the SDK does not re-read them from the environment
	opts = append(opts, otlploghttp.WithHeaders(cfg.OTLPLogsHeaders()))

	// Add user-provided options last so they can override defaults
	opts = append(opts, userOpts...)

	return opts
}

// buildGRPCExporterOptions constructs the OTLP gRPC exporter options with DD-specific defaults
func buildGRPCExporterOptions(userOpts ...otlploggrpc.Option) []otlploggrpc.Option {
	cfg := config.Get()
	opts := []otlploggrpc.Option{
		// Set timeout
		otlploggrpc.WithTimeout(cfg.OTLPLogsTimeout()),
		// Set retry config
		otlploggrpc.WithRetry(grpcRetryConfig()),
	}

	if rawEndpoint := cfg.OTLPLogsEndpoint(); rawEndpoint != "" {
		// For gRPC, we extract host:port and insecure flag from the URL
		u, _ := url.Parse(rawEndpoint)
		endpoint := u.Host
		if endpoint == "" {
			endpoint = u.Path // Handle URLs without scheme
		}
		opts = append(opts, otlploggrpc.WithEndpoint(endpoint))
		insecure := u.Scheme == "http" || u.Scheme == "grpc"
		if insecure {
			opts = append(opts, otlploggrpc.WithInsecure())
		} else if u.Scheme == "https" {
			opts = append(opts, otlploggrpc.WithEndpointURL(rawEndpoint))
		}
		log.Debug("Using OTLP logs gRPC endpoint: %s (insecure: %t)", endpoint, insecure)
	} else {
		// Use DD agent configuration as default
		endpoint, insecure := resolveOTLPEndpointGRPC()
		opts = append(opts, otlploggrpc.WithEndpoint(endpoint))
		if insecure {
			opts = append(opts, otlploggrpc.WithInsecure())
		}
	}

	// Always set headers so the SDK does not re-read them from the environment
	opts = append(opts, otlploggrpc.WithHeaders(cfg.OTLPLogsHeaders()))

	// Add user-provided options last so they can override defaults
	opts = append(opts, userOpts...)

	return opts
}

// sanitizeOTLPEndpoint appends the signal path only to a generic OTLP base URL.
// Signal-specific URLs retain their path, including a trailing slash.
// internal/config only returns endpoints that parse.
func sanitizeOTLPEndpoint(rawURL string, isGeneric bool) string {
	if isGeneric {
		u, _ := url.Parse(rawURL)
		return u.JoinPath(defaultOTLPLogsPath).String()
	}
	return rawURL
}

// resolveOTLPEndpointHTTP determines the OTLP HTTP endpoint from DD agent configuration.
// Returns (endpoint, path, insecure) where:
// - endpoint is the host:port (e.g., "localhost:4318")
// - path is the URL path (e.g., "/v1/logs")
// - insecure indicates whether to use http (true) or https (false)
//
// Note: This function is only called when OTEL_EXPORTER_OTLP_ENDPOINT and
// OTEL_EXPORTER_OTLP_LOGS_ENDPOINT are NOT set, as the OTel SDK automatically
// reads those environment variables.
func resolveOTLPEndpointHTTP() (endpoint, path string, insecure bool) {
	endpoint, insecure = resolveLogsAgentEndpoint(defaultOTLPHTTPPort, "OTLP logs")
	return endpoint, defaultOTLPLogsPath, insecure
}

// resolveOTLPEndpointGRPC determines the OTLP gRPC endpoint from DD agent configuration.
// Returns (endpoint, insecure) where:
// - endpoint is the host:port (e.g., "localhost:4317")
// - insecure indicates whether to use grpc (true) or grpcs (false)
func resolveOTLPEndpointGRPC() (endpoint string, insecure bool) {
	return resolveLogsAgentEndpoint(defaultOTLPGRPCPort, "OTLP gRPC logs")
}

// resolveLogsAgentEndpoint returns the tracer's agent host with the given OTLP
// port, and whether to connect without TLS.
//
// Priority order:
// 1. The tracer's resolved agent URL (DD_TRACE_AGENT_URL, DD_AGENT_HOST, or a programmatic override)
// 2. DD_AGENT_HOST, when the agent URL is a Unix socket
// 3. localhost (default)
func resolveLogsAgentEndpoint(port, name string) (endpoint string, insecure bool) {
	if u := config.Get().RawAgentURL(); u != nil && u.Hostname() != "" {
		endpoint = net.JoinHostPort(u.Hostname(), port)
		log.Debug("Using %s endpoint from DD_TRACE_AGENT_URL: %s", name, endpoint)
		return endpoint, u.Scheme == "http" || u.Scheme == "unix"
	}
	if host := env.Get(envDDAgentHost); host != "" {
		endpoint = net.JoinHostPort(host, port)
		log.Debug("Using %s endpoint from DD_AGENT_HOST: %s", name, endpoint)
		return endpoint, true
	}
	endpoint = net.JoinHostPort("localhost", port)
	log.Debug("Using default %s endpoint: %s", name, endpoint)
	return endpoint, true
}

// httpRetryConfig returns the retry configuration for OTLP HTTP exporter.
func httpRetryConfig() otlploghttp.RetryConfig {
	return otlploghttp.RetryConfig{
		Enabled:         true,
		InitialInterval: httpRetryInitialInterval,
		MaxInterval:     httpRetryMaxInterval,
		MaxElapsedTime:  httpRetryMaxElapsedTime,
	}
}

// grpcRetryConfig returns the retry configuration for OTLP gRPC exporter.
func grpcRetryConfig() otlploggrpc.RetryConfig {
	return otlploggrpc.RetryConfig{
		Enabled:         true,
		InitialInterval: grpcRetryInitialInterval,
		MaxInterval:     grpcRetryMaxInterval,
		MaxElapsedTime:  grpcRetryMaxElapsedTime,
	}
}
