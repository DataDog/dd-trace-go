// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"
	"fmt"
	"sync"
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
	protocolHTTPJSON = "http/json"
	encodingProtobuf = "protobuf"
	encodingJSON     = "json"

	// exporterTimeout is the export timeout of the OTLP exporters, retries included.
	exporterTimeout = 30 * time.Second
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
// Supported protocols, as validated by internal/config:
// - "http/protobuf" or "http": HTTP with protobuf encoding, using the upstream exporter
// - "http/json": HTTP with JSON encoding, using our own exporter because the upstream one only produces protobuf
// - "grpc": gRPC, using the upstream exporter
//
// temporality is the selector chosen with WithDeltaTemporality, WithCumulativeTemporality or
// WithTemporalitySelector, or nil to keep the Datadog delta default. Under "http/json" the
// WithHTTPExporter options cannot apply, so they are dropped with a warning.
//
// Endpoint resolution priority:
// 1. OTEL_EXPORTER_OTLP_METRICS_ENDPOINT (highest priority)
// 2. OTEL_EXPORTER_OTLP_ENDPOINT
// 3. DD_TRACE_AGENT_URL hostname with appropriate port
// 4. DD_AGENT_HOST with appropriate port
// 5. localhost with default port (default)
// warnHTTPOptionsIgnored makes the WithHTTPExporter warning fire once per process.
var warnHTTPOptionsIgnored sync.Once

func newDatadogOTLPExporter(ctx context.Context, httpOpts []otlpmetrichttp.Option, grpcOpts []otlpmetricgrpc.Option, temporality metric.TemporalitySelector) (metric.Exporter, error) {
	// Determine protocol
	protocol := internalconfig.Get().RuntimeMetricsProtocol()

	var exporter metric.Exporter
	var err error
	var protocolTag, encodingTag string

	switch protocol {
	case protocolGRPC:
		if temporality != nil {
			// Last option wins over the delta default set by buildGRPCExporterOptions.
			grpcOpts = append(grpcOpts, otlpmetricgrpc.WithTemporalitySelector(temporality))
		}
		exporter, err = newDatadogOTLPGRPCExporter(ctx, grpcOpts...)
		protocolTag = protocolGRPC
		encodingTag = encodingProtobuf
	case protocolHTTPJSON:
		if len(httpOpts) > 0 {
			warnHTTPOptionsIgnored.Do(func() {
				log.Warn("WithHTTPExporter options are ignored when OTEL_EXPORTER_OTLP_METRICS_PROTOCOL is http/json") //errtrack:ignore unsupported user configuration
			})
		}
		exporter = newJSONExporter(temporality)
		protocolTag = protocolHTTP
		encodingTag = encodingJSON
	default:
		log.Warn("Unknown OTLP protocol %q, defaulting to %s", protocol, defaultOTLPProtocol) //errtrack:ignore invalid user configuration
		fallthrough
	case defaultOTLPProtocol, protocolHTTP:
		if temporality != nil {
			// Last option wins over the delta default set by buildHTTPExporterOptions.
			httpOpts = append(httpOpts, otlpmetrichttp.WithTemporalitySelector(temporality))
		}
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
		otlpmetrichttp.WithTimeout(exporterTimeout),
		// Set delta temporality as default (Datadog preference)
		otlpmetrichttp.WithTemporalitySelector(deltaTemporalitySelector()),
	)

	opts = append(opts, otlpmetrichttp.WithEndpointURL(internalconfig.Get().RuntimeMetricsHTTPURL()), otlpmetrichttp.WithHeaders(internalconfig.Get().RuntimeMetricsHeaders()))

	// Add user-provided options last so they can override defaults
	opts = append(opts, userOpts...)

	return opts
}

// buildGRPCExporterOptions constructs the OTLP gRPC exporter options with DD-specific defaults
func buildGRPCExporterOptions(userOpts ...otlpmetricgrpc.Option) []otlpmetricgrpc.Option {
	opts := []otlpmetricgrpc.Option{
		// Set timeout
		otlpmetricgrpc.WithTimeout(exporterTimeout),
		// Set delta temporality as default (Datadog preference)
		otlpmetricgrpc.WithTemporalitySelector(deltaTemporalitySelector()),
		// Set retry config
		otlpmetricgrpc.WithRetry(datadogGRPCRetryConfig()),
	}

	endpoint, insecure := internalconfig.Get().RuntimeMetricsGRPCEndpoint()
	opts = append(opts, otlpmetricgrpc.WithEndpointURL(internalconfig.Get().RuntimeMetricsGRPCURL()), otlpmetricgrpc.WithEndpoint(endpoint), otlpmetricgrpc.WithHeaders(internalconfig.Get().RuntimeMetricsHeaders()))
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
