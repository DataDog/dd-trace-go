// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"context"
	"fmt"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

const (
	defaultOTLPProtocol = "http/protobuf"

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
// The protocol comes from config.OTLPLogsProtocol. Supported protocols:
// - "http/protobuf" or "http": HTTP with protobuf encoding (default)
// - "grpc": gRPC
//
// The destination comes from config.OTLPLogsHTTPURL or config.OTLPLogsGRPCURL.
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
	case "http/protobuf", "http":
		exporter, err = newOTLPHTTPExporter(ctx, httpOpts...)
		protocolTag = protocolHTTP
		encodingTag = encodingProtobuf
	default:
		log.Warn("Unsupported OTLP logs protocol; falling back to %s.", defaultOTLPProtocol)
		exporter, err = newOTLPHTTPExporter(ctx, httpOpts...)
		protocolTag = protocolHTTP
		encodingTag = encodingProtobuf
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
	endpointURL := cfg.OTLPLogsHTTPURL()
	log.Debug("Using OTLP logs endpoint: %s", endpointURL)
	// User-provided options come last so they can override defaults
	return append([]otlploghttp.Option{
		// Set timeout
		otlploghttp.WithTimeout(cfg.OTLPLogsTimeout()),
		// Set retry configuration
		otlploghttp.WithRetry(httpRetryConfig()),
		otlploghttp.WithEndpointURL(endpointURL),
		// Always set headers so the SDK does not re-read them from the environment
		otlploghttp.WithHeaders(cfg.OTLPLogsHeaders()),
	}, userOpts...)
}

// buildGRPCExporterOptions constructs the OTLP gRPC exporter options with DD-specific defaults
func buildGRPCExporterOptions(userOpts ...otlploggrpc.Option) []otlploggrpc.Option {
	cfg := config.Get()
	endpointURL := cfg.OTLPLogsGRPCURL()
	log.Debug("Using OTLP logs gRPC endpoint: %s", endpointURL)
	// User-provided options come last so they can override defaults
	return append([]otlploggrpc.Option{
		// Set timeout
		otlploggrpc.WithTimeout(cfg.OTLPLogsTimeout()),
		// Set retry config
		otlploggrpc.WithRetry(grpcRetryConfig()),
		otlploggrpc.WithEndpointURL(endpointURL),
		// Always set headers so the SDK does not re-read them from the environment
		otlploggrpc.WithHeaders(cfg.OTLPLogsHeaders()),
	}, userOpts...)
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
