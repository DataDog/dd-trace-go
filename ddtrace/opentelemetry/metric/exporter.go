// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"
	"fmt"
	"net/url"
	"time"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

const (
	defaultOTLPProtocol = "http/protobuf"

	// Telemetry tag values for protocol and encoding
	protocolHTTP     = "http"
	protocolGRPC     = "grpc"
	encodingProtobuf = "protobuf"
)

// telemetryExporter wraps a metric.Exporter to track export attempts and successes.
type telemetryExporter struct {
	metric.Exporter
	telemetry *MetricsExportTelemetry
}

// Export implements metric.Exporter.
func (e *telemetryExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	e.telemetry.RecordAttempt()
	err := e.Exporter.Export(ctx, rm)
	if err == nil {
		e.telemetry.RecordSuccess()
	}
	return err
}

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
	protocol := internalconfig.Get().RuntimeMetricsProtocol()

	var exporter metric.Exporter
	var err error
	var protocolTag, encodingTag string

	switch protocol {
	case protocolGRPC:
		exporter, err = newDatadogOTLPGRPCExporter(ctx, grpcOpts...)
		protocolTag = protocolGRPC
		encodingTag = encodingProtobuf
	case defaultOTLPProtocol, protocolHTTP:
		exporter, err = newDatadogOTLPHTTPExporter(ctx, httpOpts...)
		protocolTag = protocolHTTP
		encodingTag = encodingProtobuf
	default:
		log.Warn("Unknown OTLP protocol %q, defaulting to %s", protocol, defaultOTLPProtocol)
		exporter, err = newDatadogOTLPHTTPExporter(ctx, httpOpts...)
		protocolTag = protocolHTTP
		encodingTag = encodingProtobuf
	}

	if err != nil {
		return nil, err
	}

	// Wrap the exporter with telemetry tracking
	return &telemetryExporter{
		Exporter:  exporter,
		telemetry: NewMetricsExportTelemetry(protocolTag, encodingTag),
	}, nil
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
	opts := make([]otlpmetrichttp.Option, 0, 5+len(userOpts))
	opts = append(opts,
		// Set retry configuration
		otlpmetrichttp.WithRetry(datadogRetryConfig()),
		// Set timeout
		otlpmetrichttp.WithTimeout(30*time.Second),
		// Set delta temporality as default (Datadog preference)
		otlpmetrichttp.WithTemporalitySelector(deltaTemporalitySelector()),
	)

	endpoint, path, insecure := internalconfig.Get().RuntimeMetricsHTTPEndpoint()
	scheme := "https"
	if insecure {
		scheme = "http"
	}
	opts = append(opts, otlpmetrichttp.WithEndpointURL((&url.URL{Scheme: scheme, Host: endpoint, Path: path}).String()), otlpmetrichttp.WithHeaders(internalconfig.Get().RuntimeMetricsHeaders()))

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
		otlpmetricgrpc.WithTemporalitySelector(deltaTemporalitySelector()),
		// Set retry config
		otlpmetricgrpc.WithRetry(datadogGRPCRetryConfig()),
	}

	endpoint, insecure := internalconfig.Get().RuntimeMetricsGRPCEndpoint()
	scheme := "https"
	if insecure {
		scheme = "http"
	}
	opts = append(opts, otlpmetricgrpc.WithEndpointURL(scheme+"://"+endpoint), otlpmetricgrpc.WithEndpoint(endpoint), otlpmetricgrpc.WithHeaders(internalconfig.Get().RuntimeMetricsHeaders()))
	if insecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}

	// Add user-provided options last so they can override defaults
	opts = append(opts, userOpts...)

	return opts
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
