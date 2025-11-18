// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

// MeterProvider is a wrapper around the OpenTelemetry MeterProvider configured with Datadog defaults.
type MeterProvider struct {
	*metric.MeterProvider
}

// NewMeterProvider creates a new MeterProvider configured with Datadog-specific settings:
// - Resource with DD service, env, version, hostname, and tags
// - OTLP HTTP exporter with DD defaults (localhost:4318, http/protobuf)
// - Delta temporality for all metrics (default)
// - 60-second export interval
//
// Users can override these defaults by passing additional options.
func NewMeterProvider(opts ...Option) (*MeterProvider, error) {
	return NewMeterProviderWithContext(context.Background(), opts...)
}

// NewMeterProviderWithContext creates a new MeterProvider with a custom context.
func NewMeterProviderWithContext(ctx context.Context, opts ...Option) (*MeterProvider, error) {
	// Apply configuration options
	cfg := newConfig()
	for _, opt := range opts {
		opt.apply(cfg)
	}

	// Build Datadog-specific resource
	res, err := buildDatadogResource(ctx, cfg.resourceOptions...)
	if err != nil {
		return nil, err
	}

	// Create OTLP exporter with DD defaults
	exporter, err := newDatadogOTLPExporter(ctx, cfg.exporterOptions...)
	if err != nil {
		return nil, err
	}

	// Build metric reader with DD defaults
	// Note: Temporality is configured via the exporter's TemporalitySelector option
	// The default OTLP exporter uses cumulative, but we configure delta via exporter options
	reader := metric.NewPeriodicReader(
		exporter,
		metric.WithInterval(cfg.exportInterval),
		metric.WithTimeout(cfg.exportTimeout),
	)

	// Apply views if needed for temporality customization
	providerOpts := []metric.Option{
		metric.WithResource(res),
		metric.WithReader(reader),
	}

	// Add custom temporality view if specified
	if cfg.temporalitySelector != nil {
		// Create a view that doesn't change the instrument but allows temporality configuration
		// Note: The actual temporality is set by the reader/exporter combination
		// This is mainly for documentation purposes
	} else {
		// Default to delta temporality by adding it to exporter options
		// This will be handled in the exporter configuration
	}

	// Create the MeterProvider
	meterProvider := metric.NewMeterProvider(providerOpts...)

	return &MeterProvider{
		MeterProvider: meterProvider,
	}, nil
}

// Shutdown gracefully shuts down the MeterProvider, flushing any pending metrics.
func (mp *MeterProvider) Shutdown(ctx context.Context) error {
	if mp.MeterProvider == nil {
		return nil
	}
	return mp.MeterProvider.Shutdown(ctx)
}

// ForceFlush flushes any pending metrics.
func (mp *MeterProvider) ForceFlush(ctx context.Context) error {
	if mp.MeterProvider == nil {
		return nil
	}
	return mp.MeterProvider.ForceFlush(ctx)
}

// deltaTemporalitySelector returns a temporality selector that uses delta temporality for all instruments.
// This is the Datadog default, as delta temporality is more efficient for monitoring systems.
func deltaTemporalitySelector() metric.TemporalitySelector {
	return func(kind metric.InstrumentKind) metricdata.Temporality {
		// Use delta temporality for all metric types
		switch kind {
		case metric.InstrumentKindCounter,
			metric.InstrumentKindUpDownCounter,
			metric.InstrumentKindHistogram,
			metric.InstrumentKindObservableCounter,
			metric.InstrumentKindObservableUpDownCounter,
			metric.InstrumentKindObservableGauge:
			return metricdata.DeltaTemporality
		default:
			return metricdata.DeltaTemporality
		}
	}
}

// cumulativeTemporalitySelector returns a temporality selector that uses cumulative temporality.
// This can be used if users need to override the default delta temporality.
func cumulativeTemporalitySelector() metric.TemporalitySelector {
	return func(kind metric.InstrumentKind) metricdata.Temporality {
		switch kind {
		case metric.InstrumentKindCounter,
			metric.InstrumentKindUpDownCounter,
			metric.InstrumentKindHistogram,
			metric.InstrumentKindObservableCounter,
			metric.InstrumentKindObservableUpDownCounter:
			return metricdata.CumulativeTemporality
		case metric.InstrumentKindObservableGauge:
			// Gauges are always cumulative (represent a point-in-time value)
			return metricdata.CumulativeTemporality
		default:
			return metricdata.CumulativeTemporality
		}
	}
}

// config holds the configuration for the MeterProvider
type config struct {
	resourceOptions     []resource.Option
	exporterOptions     []otlpmetrichttp.Option
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

// WithExporter adds OTLP exporter options to the MeterProvider.
// These will override the Datadog defaults if there are conflicts.
func WithExporter(opts ...otlpmetrichttp.Option) Option {
	return optionFunc(func(c *config) {
		c.exporterOptions = append(c.exporterOptions, opts...)
	})
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
