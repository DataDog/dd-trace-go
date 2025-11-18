// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"
	"fmt"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/env"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

const (
	// Default OTLP HTTP endpoint for Datadog
	defaultOTLPEndpoint = "http://localhost:4318"
	defaultOTLPPath     = "/v1/metrics"

	// OTLP environment variables
	envOTLPEndpoint        = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOTLPMetricsEndpoint = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	envOTLPProtocol        = "OTEL_EXPORTER_OTLP_PROTOCOL"
	envOTLPMetricsProtocol = "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL"
)

// newDatadogOTLPExporter creates an OTLP HTTP exporter configured with Datadog-specific defaults.
//
// Default configuration:
// - Protocol: http/protobuf
// - Endpoint: http://localhost:4318/v1/metrics
// - Content-Type: application/x-protobuf
// - Retry logic: Retries on 429, 502, 503, 504; Does NOT retry on 400 or partial success
// - Honor Retry-After headers
//
// The exporter respects standard OTEL environment variables:
// - OTEL_EXPORTER_OTLP_METRICS_ENDPOINT (highest priority)
// - OTEL_EXPORTER_OTLP_ENDPOINT
// - OTEL_EXPORTER_OTLP_METRICS_PROTOCOL
// - OTEL_EXPORTER_OTLP_PROTOCOL
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

	// Only set endpoint if not already set by environment variables
	if !hasEndpointInEnv() {
		opts = append(opts, otlpmetrichttp.WithEndpoint("localhost:4318"))
		opts = append(opts, otlpmetrichttp.WithURLPath(defaultOTLPPath))
		opts = append(opts, otlpmetrichttp.WithInsecure()) // default is http, not https
	}

	// Add user-provided options last so they can override defaults
	opts = append(opts, userOpts...)

	return opts
}

// hasEndpointInEnv checks if OTLP endpoint is configured via environment variables
func hasEndpointInEnv() bool {
	if v := env.Get(envOTLPMetricsEndpoint); v != "" {
		return true
	}
	if v := env.Get(envOTLPEndpoint); v != "" {
		return true
	}
	return false
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

// datadogTemporalitySelector returns a temporality selector that uses delta temporality
// for all instruments, which is the Datadog preference
func datadogTemporalitySelector() metric.TemporalitySelector {
	return func(kind metric.InstrumentKind) metricdata.Temporality {
		// Use delta temporality for all metric types
		return metricdata.DeltaTemporality
	}
}
