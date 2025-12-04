// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/env"

	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

const (
	// Environment variables for controlling metrics behavior
	envDDMetricsOtelEnabled = "DD_METRICS_OTEL_ENABLED"
	envOtelMetricsExporter  = "OTEL_METRICS_EXPORTER"
)

// NewMeterProvider creates a new MeterProvider configured with Datadog-specific settings:
// - Resource with DD service, env, version, hostname, and tags
// - OTLP HTTP exporter with DD defaults (localhost:4318, http/protobuf)
// - Delta temporality for all metrics (default)
// - 60-second export interval
//
// Metrics can be disabled via environment variables:
// - DD_METRICS_OTEL_ENABLED=false (default: false/disabled)
// - OTEL_METRICS_EXPORTER=none
//
// When disabled, returns a no-op MeterProvider that doesn't export metrics.
//
// Users can override these defaults by passing additional options.
func NewMeterProvider(opts ...Option) (otelmetric.MeterProvider, error) {
	return NewMeterProviderWithContext(context.Background(), opts...)
}

// NewMeterProviderWithContext creates a new MeterProvider with a custom context.
func NewMeterProviderWithContext(ctx context.Context, opts ...Option) (otelmetric.MeterProvider, error) {
	// Check if metrics are enabled via environment variables
	if !isMetricsEnabled() {
		// Return a no-op MeterProvider that doesn't export metrics
		return noop.NewMeterProvider(), nil
	}

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

	// Create OTLP exporter with DD defaults (supports both HTTP and gRPC)
	exporter, err := newDatadogOTLPExporter(ctx, cfg.httpExporterOptions, cfg.grpcExporterOptions)
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

	// Create the MeterProvider
	return metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(reader),
	), nil
}

// isMetricsEnabled checks environment variables to determine if metrics should be enabled.
// Metrics are disabled by default and can be enabled by:
// - Setting DD_METRICS_OTEL_ENABLED=true
//
// Returns false (disabled) if:
// - DD_METRICS_OTEL_ENABLED is "false" or unset (default)
// - OTEL_METRICS_EXPORTER is set to "none"
func isMetricsEnabled() bool {
	// Check OTEL_METRICS_EXPORTER first - if set to "none", always disable
	if exporter := env.Get(envOtelMetricsExporter); exporter != "" {
		exporter = strings.ToLower(strings.TrimSpace(exporter))
		if exporter == "none" {
			return false
		}
	}

	// Check DD_METRICS_OTEL_ENABLED (default: false/disabled)
	metricsEnabled := env.Get(envDDMetricsOtelEnabled)
	if metricsEnabled == "" {
		// If not set, default to disabled
		return false
	}

	// If explicitly set, respect the value
	metricsEnabled = strings.ToLower(strings.TrimSpace(metricsEnabled))
	if metricsEnabled == "false" || metricsEnabled == "0" {
		return false
	}
	if metricsEnabled == "true" || metricsEnabled == "1" {
		return true
	}

	// Invalid value, default to disabled
	return false
}

// IsNoop returns true if the given MeterProvider is a no-op provider that doesn't export metrics.
func IsNoop(mp otelmetric.MeterProvider) bool {
	_, ok := mp.(noop.MeterProvider)
	return ok
}

// Shutdown gracefully shuts down the MeterProvider, flushing any pending metrics.
// For no-op providers, this is a no-op operation.
func Shutdown(ctx context.Context, mp otelmetric.MeterProvider) error {
	if IsNoop(mp) {
		return nil
	}
	if sdkMP, ok := mp.(*metric.MeterProvider); ok {
		return sdkMP.Shutdown(ctx)
	}
	return nil
}

// ForceFlush flushes any pending metrics.
// For no-op providers, this is a no-op operation.
func ForceFlush(ctx context.Context, mp otelmetric.MeterProvider) error {
	if IsNoop(mp) {
		return nil
	}
	if sdkMP, ok := mp.(*metric.MeterProvider); ok {
		return sdkMP.ForceFlush(ctx)
	}
	return nil
}

// deltaTemporalitySelector returns a temporality selector configured with Datadog defaults.
// Default temporality is Delta, but non-monotonic instruments use Cumulative per OTel spec:
// - Monotonic counters (Counter, ObservableCounter) → Delta (differences between measurements)
// - Non-monotonic counters (UpDownCounter, ObservableUpDownCounter) → Cumulative (absolute values)
// - Gauges (ObservableGauge) → Cumulative (point-in-time values)
// - Histograms → Delta (distribution of measurements)
// It respects OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE if set, with one exception:
// UpDownCounter and ObservableUpDownCounter ALWAYS use Cumulative (even if DELTA is requested).
func deltaTemporalitySelector() metric.TemporalitySelector {
	// Check if user has explicitly set temporality preference
	temporalityPref := strings.ToUpper(strings.TrimSpace(env.Get("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE")))

	return func(kind metric.InstrumentKind) metricdata.Temporality {
		// UpDownCounter and Gauge ALWAYS use cumulative, regardless of preference
		// They represent current state, not monotonic changes
		if kind == metric.InstrumentKindUpDownCounter ||
			kind == metric.InstrumentKindObservableUpDownCounter ||
			kind == metric.InstrumentKindObservableGauge {
			return metricdata.CumulativeTemporality
		}

		// For monotonic instruments, respect the user's preference if set
		if temporalityPref == "CUMULATIVE" {
			return metricdata.CumulativeTemporality
		}

		// Default behavior for monotonic instruments: Delta
		return metricdata.DeltaTemporality
	}
}

// cumulativeTemporalitySelector returns a temporality selector that uses cumulative temporality.
// This can be used if users need to override the default delta temporality.
func cumulativeTemporalitySelector() metric.TemporalitySelector {
	return func(metric.InstrumentKind) metricdata.Temporality {
		return metricdata.CumulativeTemporality
	}
}
