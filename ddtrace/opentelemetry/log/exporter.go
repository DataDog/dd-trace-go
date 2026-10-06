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
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/config"
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
	defaultOTLPProtocol = "http/json"

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
	encodingJSON     = "json"
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
// Protocol selection priority:
// 1. OTEL_EXPORTER_OTLP_LOGS_PROTOCOL
// 2. OTEL_EXPORTER_OTLP_PROTOCOL
// 3. Default: http/json
//
// Supported protocols:
// - "http/json": HTTP with JSON encoding (default)
// - "http/protobuf" or "http": HTTP with protobuf encoding
// - "grpc": gRPC
//
// Endpoint resolution priority:
// 1. OTEL_EXPORTER_OTLP_LOGS_ENDPOINT (highest priority)
// 2. OTEL_EXPORTER_OTLP_ENDPOINT
// 3. DD_TRACE_AGENT_URL hostname with appropriate port
// 4. DD_AGENT_HOST with appropriate port
// 5. localhost with default port (default)
func newOTLPExporter(ctx context.Context, httpOpts []otlploghttp.Option, grpcOpts []otlploggrpc.Option) (sdklog.Exporter, error) {
	// Determine protocol
	protocol := config.Get().OTLPLogsProtocol()

	var exporter sdklog.Exporter
	var err error
	var protocolTag, encodingTag string

	switch protocol {
	case "grpc":
		exporter, err = newOTLPGRPCExporter(ctx, grpcOpts...)
		protocolTag = protocolGRPC
		encodingTag = encodingProtobuf
	case "http/json":
		exporter, err = newOTLPHTTPExporter(ctx, httpOpts...)
		protocolTag = protocolHTTP
		encodingTag = encodingJSON
	case "http/protobuf", "http":
		exporter, err = newOTLPHTTPExporter(ctx, httpOpts...)
		protocolTag = protocolHTTP
		encodingTag = encodingProtobuf
	default:
		log.Warn("Unknown OTLP logs protocol %q, defaulting to %s", protocol, defaultOTLPProtocol)
		exporter, err = newOTLPHTTPExporter(ctx, httpOpts...)
		protocolTag = protocolHTTP
		encodingTag = encodingJSON
	}

	if err != nil {
		return nil, err
	}

	// Wrap the exporter with telemetry tracking
	return &telemetryExporter{
		Exporter:  exporter,
		telemetry: NewLogsExportTelemetry(protocolTag, encodingTag),
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
		otlploghttp.WithHeaders(cfg.OTLPLogsHeaders()),
		// Set retry configuration
		otlploghttp.WithRetry(httpRetryConfig()),
	}

	// Check if OTEL environment variables are set
	if rawEndpoint := cfg.OTLPLogsEndpoint(); rawEndpoint != "" {
		// Parse and sanitize the URL to handle trailing slashes correctly
		sanitizedURL := sanitizeOTLPEndpoint(rawEndpoint, "/v1/logs")
		if sanitizedURL != "" {
			opts = append(opts, otlploghttp.WithEndpointURL(sanitizedURL))
			log.Debug("Using sanitized OTLP logs endpoint: %s", sanitizedURL)
		} else {
			// Fallback to DD agent config if URL cannot be parsed
			log.Warn("Invalid OTLP endpoint URL '%s', falling back to DD agent configuration", rawEndpoint)
			endpoint, insecure := resolveLogsAgentEndpoint(defaultOTLPHTTPPort)
			opts = append(opts, otlploghttp.WithEndpoint(endpoint))
			opts = append(opts, otlploghttp.WithURLPath(defaultOTLPLogsPath))
			if insecure {
				opts = append(opts, otlploghttp.WithInsecure())
			}
		}
	} else {
		// Use DD agent configuration as default
		endpoint, insecure := resolveLogsAgentEndpoint(defaultOTLPHTTPPort)
		opts = append(opts, otlploghttp.WithEndpoint(endpoint))
		opts = append(opts, otlploghttp.WithURLPath(defaultOTLPLogsPath))
		if insecure {
			opts = append(opts, otlploghttp.WithInsecure())
		}
	}

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
		otlploggrpc.WithHeaders(cfg.OTLPLogsHeaders()),
		// Set retry config
		otlploggrpc.WithRetry(grpcRetryConfig()),
	}

	// Check if OTEL environment variables are set
	if rawEndpoint := cfg.OTLPLogsEndpoint(); rawEndpoint != "" {
		// For gRPC, we extract host:port and insecure flag from the URL
		u, err := url.Parse(rawEndpoint)
		if err != nil {
			// Fallback to DD agent config if URL cannot be parsed
			log.Warn("Invalid OTLP endpoint URL '%s', falling back to DD agent configuration: %s", rawEndpoint, err.Error())
			endpoint, insecure := resolveLogsAgentEndpoint(defaultOTLPGRPCPort)
			opts = append(opts, otlploggrpc.WithEndpoint(endpoint))
			if insecure {
				opts = append(opts, otlploggrpc.WithInsecure())
			}
		} else {
			endpoint := u.Host
			if endpoint == "" {
				endpoint = u.Path // Handle URLs without scheme
			}
			opts = append(opts, otlploggrpc.WithEndpoint(endpoint))
			if u.Scheme == "http" || u.Scheme == "grpc" {
				opts = append(opts, otlploggrpc.WithInsecure())
			}
			// ruleguard: Forbidden: (internal log) format verbs %v, %+v, or %#v prevents controlled data exposure.
			// Use specific format verbs like %s, %d, %q and sanitize data before logging. (gocritic)
			//nolint:gocritic // TODO: Fix the warning above and remove the nolint.
			log.Debug("Using OTLP logs gRPC endpoint: %s (insecure: %v)", endpoint, u.Scheme == "http" || u.Scheme == "grpc")
		}
	} else {
		// Use DD agent configuration as default
		endpoint, insecure := resolveLogsAgentEndpoint(defaultOTLPGRPCPort)
		opts = append(opts, otlploggrpc.WithEndpoint(endpoint))
		if insecure {
			opts = append(opts, otlploggrpc.WithInsecure())
		}
	}

	// Add user-provided options last so they can override defaults
	opts = append(opts, userOpts...)

	return opts
}

// sanitizeOTLPEndpoint sanitizes an OTLP endpoint URL by:
// 1. Parsing the URL
// 2. Trimming any trailing slashes from the path
// 3. Appending the signal-specific path (e.g., "/v1/logs")
// 4. Returning the complete URL
//
// This works around issues where the OTel SDK may not handle trailing slashes correctly,
// which can result in double slashes like http://host:4320//v1/logs
func sanitizeOTLPEndpoint(rawURL, signalPath string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		log.Warn("Failed to parse OTLP endpoint URL: %s", err.Error())
		return ""
	}

	// Trim trailing slashes from the path
	u.Path = strings.TrimRight(u.Path, "/")

	// If the URL already has a path, keep it; otherwise use the signal-specific path
	if u.Path == "" {
		u.Path = signalPath
	} else if !strings.HasSuffix(u.Path, signalPath) {
		// If path doesn't already end with signal path, append it
		u.Path = u.Path + signalPath
	}

	return u.String()
}

func resolveLogsAgentEndpoint(port string) (endpoint string, insecure bool) {
	u := config.Get().OTLPLogsAgentURL()
	return net.JoinHostPort(u.Hostname(), port), u.Scheme == "http" || u.Scheme == "unix"
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
