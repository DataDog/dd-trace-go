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
	"strconv"
	"strings"
	"time"

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
	defaultOTLPProtocol = "http/json"

	// OTLP environment variables (logs-specific)
	envOTLPLogsEndpoint = "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"
	envOTLPLogsProtocol = "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL"
	envOTLPLogsHeaders  = "OTEL_EXPORTER_OTLP_LOGS_HEADERS"
	envOTLPLogsTimeout  = "OTEL_EXPORTER_OTLP_LOGS_TIMEOUT"

	// OTLP environment variables (generic)
	envOTLPEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOTLPProtocol = "OTEL_EXPORTER_OTLP_PROTOCOL"
	envOTLPHeaders  = "OTEL_EXPORTER_OTLP_HEADERS"
	envOTLPTimeout  = "OTEL_EXPORTER_OTLP_TIMEOUT"

	// DD environment variables for agent configuration
	envDDTraceAgentURL = "DD_TRACE_AGENT_URL"
	envDDAgentHost     = "DD_AGENT_HOST"

	// BatchLogRecordProcessor environment variables
	envBLRPMaxQueueSize       = "OTEL_BLRP_MAX_QUEUE_SIZE"
	envBLRPScheduleDelay      = "OTEL_BLRP_SCHEDULE_DELAY"
	envBLRPExportTimeout      = "OTEL_BLRP_EXPORT_TIMEOUT"
	envBLRPMaxExportBatchSize = "OTEL_BLRP_MAX_EXPORT_BATCH_SIZE"

	// Default values for BatchLogRecordProcessor
	defaultBLRPMaxQueueSize       = 2048
	defaultBLRPScheduleDelay      = 1000 * time.Millisecond
	defaultBLRPExportTimeout      = 30000 * time.Millisecond
	defaultBLRPMaxExportBatchSize = 512

	// Default values for BatchLogRecordProcessor in milliseconds (for telemetry reporting)
	defaultBLRPScheduleDelayMs = 1000
	defaultBLRPExportTimeoutMs = 30000
	defaultOTLPTimeoutMs       = 10000 // 10 seconds

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
	protocol := resolveOTLPProtocol()

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

// resolveOTLPProtocol returns the OTLP protocol from environment variables.
// Priority: OTEL_EXPORTER_OTLP_LOGS_PROTOCOL > OTEL_EXPORTER_OTLP_PROTOCOL > "http/json"
func resolveOTLPProtocol() string {
	// Check logs-specific protocol first
	if protocol := env.Get(envOTLPLogsProtocol); protocol != "" {
		return strings.ToLower(strings.TrimSpace(protocol))
	}
	// Fall back to general OTLP protocol
	if protocol := env.Get(envOTLPProtocol); protocol != "" {
		return strings.ToLower(strings.TrimSpace(protocol))
	}
	// Default to HTTP with JSON
	return defaultOTLPProtocol
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
	opts := []otlploghttp.Option{
		// Set timeout
		otlploghttp.WithTimeout(resolveExportTimeout()),
		// Set retry configuration
		otlploghttp.WithRetry(httpRetryConfig()),
	}

	// Only set endpoint if not already set by OTEL environment variables
	if !hasOTLPEndpointInEnv() {
		endpoint, path, insecure := resolveOTLPEndpointHTTP()
		opts = append(opts, otlploghttp.WithEndpoint(endpoint))
		opts = append(opts, otlploghttp.WithURLPath(path))
		if insecure {
			opts = append(opts, otlploghttp.WithInsecure())
		}
	}

	// Set headers if configured
	if headers := resolveHeaders(); len(headers) > 0 {
		opts = append(opts, otlploghttp.WithHeaders(headers))
	}

	// Add user-provided options last so they can override defaults
	opts = append(opts, userOpts...)

	return opts
}

// buildGRPCExporterOptions constructs the OTLP gRPC exporter options with DD-specific defaults
func buildGRPCExporterOptions(userOpts ...otlploggrpc.Option) []otlploggrpc.Option {
	opts := []otlploggrpc.Option{
		// Set timeout
		otlploggrpc.WithTimeout(resolveExportTimeout()),
		// Set retry config
		otlploggrpc.WithRetry(grpcRetryConfig()),
	}

	// Only set endpoint if not already set by OTEL environment variables
	if !hasOTLPEndpointInEnv() {
		endpoint, insecure := resolveOTLPEndpointGRPC()
		opts = append(opts, otlploggrpc.WithEndpoint(endpoint))
		if insecure {
			opts = append(opts, otlploggrpc.WithInsecure())
		}
	}

	// Set headers if configured
	if headers := resolveHeaders(); len(headers) > 0 {
		opts = append(opts, otlploggrpc.WithHeaders(headers))
	}

	// Add user-provided options last so they can override defaults
	opts = append(opts, userOpts...)

	return opts
}

// hasOTLPEndpointInEnv checks if OTLP endpoint is configured via OTEL environment variables
func hasOTLPEndpointInEnv() bool {
	if v := env.Get(envOTLPLogsEndpoint); v != "" {
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
// - path is the URL path (e.g., "/v1/logs")
// - insecure indicates whether to use http (true) or https (false)
//
// Priority order:
// 1. DD_TRACE_AGENT_URL with port changed to 4318
// 2. DD_AGENT_HOST:4318
// 3. localhost:4318 (default)
func resolveOTLPEndpointHTTP() (endpoint, path string, insecure bool) {
	path = defaultOTLPLogsPath
	insecure = true // default to http

	// Check DD_TRACE_AGENT_URL first
	if agentURL := env.Get(envDDTraceAgentURL); agentURL != "" {
		u, err := url.Parse(agentURL)
		if err != nil {
			log.Warn("Failed to parse DD_TRACE_AGENT_URL for logs: %s, using default", err.Error())
		} else {
			// Extract hostname from the agent URL and use port 4318
			hostname := u.Hostname()
			if hostname != "" {
				endpoint = net.JoinHostPort(hostname, defaultOTLPHTTPPort)
				// Preserve the scheme from DD_TRACE_AGENT_URL
				insecure = (u.Scheme == "http" || u.Scheme == "unix")
				log.Debug("Using OTLP logs endpoint from DD_TRACE_AGENT_URL: %s", endpoint)
				return
			}
		}
	}

	// Check DD_AGENT_HOST
	if host := env.Get(envDDAgentHost); host != "" {
		endpoint = net.JoinHostPort(host, defaultOTLPHTTPPort)
		insecure = true
		log.Debug("Using OTLP logs endpoint from DD_AGENT_HOST: %s", endpoint)
		return
	}

	// Default to localhost:4318
	endpoint = "localhost:4318"
	insecure = true
	log.Debug("Using default OTLP logs endpoint: %s", endpoint)
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

	// Check DD_TRACE_AGENT_URL first
	if agentURL := env.Get(envDDTraceAgentURL); agentURL != "" {
		u, err := url.Parse(agentURL)
		if err != nil {
			log.Warn("Failed to parse DD_TRACE_AGENT_URL for logs: %s, using default", err.Error())
		} else {
			// Extract hostname from the agent URL and use port 4317 for gRPC
			hostname := u.Hostname()
			if hostname != "" {
				endpoint = net.JoinHostPort(hostname, defaultOTLPGRPCPort)
				// Preserve the scheme from DD_TRACE_AGENT_URL
				insecure = (u.Scheme == "http" || u.Scheme == "unix")
				log.Debug("Using OTLP gRPC logs endpoint from DD_TRACE_AGENT_URL: %s", endpoint)
				return
			}
		}
	}

	// Check DD_AGENT_HOST
	if host := env.Get(envDDAgentHost); host != "" {
		endpoint = net.JoinHostPort(host, defaultOTLPGRPCPort)
		log.Debug("Using OTLP gRPC logs endpoint from DD_AGENT_HOST: %s", endpoint)
		return
	}

	// Default to localhost:4317
	endpoint = net.JoinHostPort("localhost", defaultOTLPGRPCPort)
	log.Debug("Using default OTLP gRPC logs endpoint: %s", endpoint)
	return
}

// resolveHeaders returns the headers to send with OTLP requests.
// Priority: OTEL_EXPORTER_OTLP_LOGS_HEADERS > OTEL_EXPORTER_OTLP_HEADERS
// Format: k=v,k2=v2 (spaces are trimmed, invalid entries are ignored)
func resolveHeaders() map[string]string {
	// Check logs-specific headers first
	if headersStr := env.Get(envOTLPLogsHeaders); headersStr != "" {
		return parseHeaders(headersStr)
	}
	// Fall back to general OTLP headers
	if headersStr := env.Get(envOTLPHeaders); headersStr != "" {
		return parseHeaders(headersStr)
	}
	return nil
}

// parseHeaders parses header string in format "k=v,k2=v2"
// Spaces are trimmed, invalid entries (no '=') are silently ignored
func parseHeaders(str string) map[string]string {
	headers := make(map[string]string)
	for _, entry := range strings.Split(str, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			// Invalid entry, skip it
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if key != "" {
			headers[key] = val
		}
	}
	return headers
}

// resolveExportTimeout returns the export timeout from environment variables.
// Priority: OTEL_EXPORTER_OTLP_LOGS_TIMEOUT > OTEL_EXPORTER_OTLP_TIMEOUT > default (30s)
func resolveExportTimeout() time.Duration {
	// Check logs-specific timeout first
	if timeoutStr := env.Get(envOTLPLogsTimeout); timeoutStr != "" {
		if timeout, err := parseTimeout(timeoutStr); err == nil {
			return timeout
		}
	}
	// Fall back to general OTLP timeout
	if timeoutStr := env.Get(envOTLPTimeout); timeoutStr != "" {
		if timeout, err := parseTimeout(timeoutStr); err == nil {
			return timeout
		}
	}
	// Default to 30 seconds
	return 30 * time.Second
}

// parseTimeout parses timeout string (milliseconds as integer)
func parseTimeout(str string) (time.Duration, error) {
	ms, err := strconv.ParseInt(str, 10, 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(ms) * time.Millisecond, nil
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

// resolveBLRPMaxQueueSize returns the max queue size for BatchLogRecordProcessor.
// Default: 2048
func resolveBLRPMaxQueueSize() int {
	if sizeStr := env.Get(envBLRPMaxQueueSize); sizeStr != "" {
		if size, err := strconv.Atoi(sizeStr); err == nil && size > 0 {
			return size
		}
	}
	return defaultBLRPMaxQueueSize
}

// resolveBLRPScheduleDelay returns the schedule delay for BatchLogRecordProcessor.
// Default: 1000ms
func resolveBLRPScheduleDelay() time.Duration {
	if delayStr := env.Get(envBLRPScheduleDelay); delayStr != "" {
		if delay, err := parseTimeout(delayStr); err == nil {
			return delay
		}
	}
	return defaultBLRPScheduleDelay
}

// resolveBLRPExportTimeout returns the export timeout for BatchLogRecordProcessor.
// Default: 30000ms
func resolveBLRPExportTimeout() time.Duration {
	if timeoutStr := env.Get(envBLRPExportTimeout); timeoutStr != "" {
		if timeout, err := parseTimeout(timeoutStr); err == nil {
			return timeout
		}
	}
	return defaultBLRPExportTimeout
}

// resolveBLRPMaxExportBatchSize returns the max export batch size for BatchLogRecordProcessor.
// Default: 512
func resolveBLRPMaxExportBatchSize() int {
	if sizeStr := env.Get(envBLRPMaxExportBatchSize); sizeStr != "" {
		if size, err := strconv.Atoi(sizeStr); err == nil && size > 0 {
			return size
		}
	}
	return defaultBLRPMaxExportBatchSize
}
