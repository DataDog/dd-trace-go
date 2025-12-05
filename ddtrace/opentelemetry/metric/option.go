// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

// config holds the configuration for the MeterProvider
type config struct {
	resourceOptions     []resource.Option
	httpExporterOptions []otlpmetrichttp.Option
	grpcExporterOptions []otlpmetricgrpc.Option
	exportInterval      time.Duration
	exportTimeout       time.Duration
	temporalitySelector metric.TemporalitySelector
}

// newConfig creates a default configuration
func newConfig() *config {
	return &config{
		exportInterval: 60 * time.Second,
		exportTimeout:  30 * time.Second,
	}
}

// Option is a function that configures the MeterProvider
type Option interface {
	apply(*config)
}

type optionFunc func(*config)

func (o optionFunc) apply(c *config) {
	o(c)
}

// WithResource adds resource options to the MeterProvider.
// These will be merged with the Datadog-specific resource attributes.
func WithResource(opts ...resource.Option) Option {
	return optionFunc(func(c *config) {
		c.resourceOptions = append(c.resourceOptions, opts...)
	})
}

// WithHTTPExporter allows customization of the OTLP HTTP exporter with additional options.
func WithHTTPExporter(opts ...otlpmetrichttp.Option) Option {
	return optionFunc(func(c *config) {
		c.httpExporterOptions = append(c.httpExporterOptions, opts...)
	})
}

// WithGRPCExporter allows customization of the OTLP gRPC exporter with additional options.
func WithGRPCExporter(opts ...otlpmetricgrpc.Option) Option {
	return optionFunc(func(c *config) {
		c.grpcExporterOptions = append(c.grpcExporterOptions, opts...)
	})
}

// WithExporter adds OTLP HTTP exporter options to the MeterProvider (deprecated).
// Use WithHTTPExporter or WithGRPCExporter instead.
func WithExporter(opts ...otlpmetrichttp.Option) Option {
	return WithHTTPExporter(opts...)
}

// WithExportInterval sets the interval at which metrics are exported.
// Default is 60 seconds.
func WithExportInterval(interval time.Duration) Option {
	return optionFunc(func(c *config) {
		c.exportInterval = interval
	})
}

// WithExportTimeout sets the timeout for each export operation.
// Default is 30 seconds.
func WithExportTimeout(timeout time.Duration) Option {
	return optionFunc(func(c *config) {
		c.exportTimeout = timeout
	})
}

// WithDeltaTemporality configures the MeterProvider to use delta temporality (default).
func WithDeltaTemporality() Option {
	return optionFunc(func(c *config) {
		c.temporalitySelector = deltaTemporalitySelector()
	})
}

// WithCumulativeTemporality configures the MeterProvider to use cumulative temporality.
func WithCumulativeTemporality() Option {
	return optionFunc(func(c *config) {
		c.temporalitySelector = cumulativeTemporalitySelector()
	})
}

// WithTemporalitySelector allows users to provide a custom temporality selector.
func WithTemporalitySelector(selector metric.TemporalitySelector) Option {
	return optionFunc(func(c *config) {
		c.temporalitySelector = selector
	})
}

// AggregationSelector allows customization of aggregation for specific instruments
type AggregationSelector = metric.AggregationSelector

// View allows customization of metrics streams
type View = metric.View

// WithView adds a custom view to the MeterProvider
func WithView(view View) Option {
	// Note: This would require modifying the config structure to support views
	// For now, this is a placeholder for future extension
	return optionFunc(func(c *config) {
		// TODO: Add view support to config
	})
}

// Scope provides a namespace for instruments
type Scope = instrumentation.Scope
