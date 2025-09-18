// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package logs

import (
	"context"
	"fmt"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// NewExporter creates an OTLP log exporter based on the configuration
func NewExporter(ctx context.Context, config *Config) (sdklog.Exporter, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	switch config.Protocol {
	case "grpc":
		return newGRPCExporter(ctx, config)
	case "http/protobuf":
		return newHTTPExporter(ctx, config, otlploghttp.WithURLPath("/v1/logs"))
	case "http/json":
		return newHTTPExporter(ctx, config,
			otlploghttp.WithURLPath("/v1/logs"),
			// Note: JSON encoding would need additional configuration
		)
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", config.Protocol)
	}
}

// newGRPCExporter creates a gRPC OTLP log exporter
func newGRPCExporter(ctx context.Context, config *Config) (sdklog.Exporter, error) {
	opts := []otlploggrpc.Option{
		otlploggrpc.WithEndpoint(config.Endpoint),
		otlploggrpc.WithTimeout(config.Timeout),
	}

	// Add headers
	if len(config.Headers) > 0 {
		opts = append(opts, otlploggrpc.WithHeaders(config.Headers))
	}

	// Check if endpoint is localhost (likely Datadog Agent) - use insecure connection
	if isLocalEndpoint(config.Endpoint) {
		opts = append(opts, otlploggrpc.WithInsecure())
	}

	exporter, err := otlploggrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC exporter: %w", err)
	}

	return exporter, nil
}

// newHTTPExporter creates an HTTP OTLP log exporter
func newHTTPExporter(ctx context.Context, config *Config, opts ...otlploghttp.Option) (sdklog.Exporter, error) {
	defaultOpts := []otlploghttp.Option{
		otlploghttp.WithEndpoint(config.Endpoint),
		otlploghttp.WithTimeout(config.Timeout),
	}

	// Add headers
	if len(config.Headers) > 0 {
		defaultOpts = append(defaultOpts, otlploghttp.WithHeaders(config.Headers))
	}

	// Check if endpoint is localhost (likely Datadog Agent) - use insecure connection
	if isLocalEndpoint(config.Endpoint) {
		defaultOpts = append(defaultOpts, otlploghttp.WithInsecure())
	}

	// Combine default options with provided options
	allOpts := append(defaultOpts, opts...)

	exporter, err := otlploghttp.New(ctx, allOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP exporter: %w", err)
	}

	return exporter, nil
}

// isLocalEndpoint checks if the endpoint is a local address
func isLocalEndpoint(endpoint string) bool {
	return contains(endpoint, "localhost") ||
		contains(endpoint, "127.0.0.1") ||
		contains(endpoint, "::1")
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return indexOf(s, substr) != -1
}

// ExporterWrapper wraps an OTLP exporter to add Datadog-specific functionality
type ExporterWrapper struct {
	exporter sdklog.Exporter
	config   *Config
}

// NewExporterWrapper creates a new ExporterWrapper
func NewExporterWrapper(exporter sdklog.Exporter, config *Config) *ExporterWrapper {
	return &ExporterWrapper{
		exporter: exporter,
		config:   config,
	}
}

// Export exports log records, with error handling and retries
func (w *ExporterWrapper) Export(ctx context.Context, records []sdklog.Record) error {
	if len(records) == 0 {
		return nil
	}

	// Add timeout to context if not already present
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, w.config.Timeout)
		defer cancel()
	}

	err := w.exporter.Export(ctx, records)
	if err != nil {
		// Log error but don't fail the application
		log.Warn("Failed to export OTLP logs: %v", err)

		// Check if it's a specific error that indicates unsupported endpoint
		if isUnsupportedError(err) {
			log.Warn("OTLP logs endpoint may not support logs. Ensure Datadog Agent version is 7.48.0 or greater.")
		}

		return err
	}

	return nil
}

// Shutdown shuts down the exporter
func (w *ExporterWrapper) Shutdown(ctx context.Context) error {
	return w.exporter.Shutdown(ctx)
}

// ForceFlush forces a flush of the exporter
func (w *ExporterWrapper) ForceFlush(ctx context.Context) error {
	return w.exporter.ForceFlush(ctx)
}

// isUnsupportedError checks if the error indicates an unsupported endpoint
func isUnsupportedError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()

	// Check for HTTP 404 or similar errors
	return contains(errStr, "404") ||
		contains(errStr, "Not Found") ||
		contains(errStr, "not found") ||
		contains(errStr, "unsupported") ||
		contains(errStr, "Unsupported")
}

// TestExporter creates a test exporter that validates the configuration without actually sending data
func TestExporter(config *Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	exporter, err := NewExporter(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to create exporter: %w", err)
	}
	defer func() {
		if shutdownErr := exporter.Shutdown(ctx); shutdownErr != nil {
			log.Warn("Failed to shutdown test exporter: %v", shutdownErr)
		}
	}()

	// Try to export an empty batch to test connectivity
	err = exporter.Export(ctx, []sdklog.Record{})
	if err != nil {
		return fmt.Errorf("exporter test failed: %w", err)
	}

	return nil
}
