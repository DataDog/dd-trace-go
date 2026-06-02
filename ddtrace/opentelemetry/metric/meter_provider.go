// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/otelmetricsinstall"

	"go.opentelemetry.io/otel"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// installedProvider tracks the MeterProvider installed by InstallGlobal so that
// ShutdownHook only shuts down the provider we own, not one the user installed.
var installedProvider *metric.MeterProvider

func init() {
	otelmetricsinstall.StartHook = func(ctx context.Context) error {
		if err := InstallGlobal(); err != nil {
			return err
		}
		return startGoRuntimeMetrics(ctx)
	}
	otelmetricsinstall.ShutdownHook = func(ctx context.Context) error {
		if installedProvider == nil {
			return nil
		}
		return installedProvider.Shutdown(ctx)
	}
}

// InstallGlobal installs a DD-configured MeterProvider as the global OTel provider.
// No-op when metric export is disabled (DD_METRICS_OTEL_ENABLED or OTEL_METRICS_EXPORTER=otlp).
// tracer.Start() calls it when export is enabled.
func InstallGlobal(opts ...Option) error {
	if !isMetricsEnabled() {
		return nil
	}
	// Don't replace a real OTel SDK MeterProvider that the user already installed.
	// The OTel global defaults to an internal delegating *meterProvider (not a real
	// SDK — it silently drops metrics until a real provider is set). We only skip
	// installation if a real *metric.MeterProvider is already configured.
	if _, ok := otel.GetMeterProvider().(*metric.MeterProvider); ok {
		return nil
	}
	allOpts := append(opts, withRuntimeProducerDefault())
	mp, err := NewMeterProvider(allOpts...)
	if err != nil {
		return err
	}
	otel.SetMeterProvider(mp)
	installedProvider = mp.(*metric.MeterProvider)
	return nil
}

// withRuntimeProducerDefault injects the RuntimeProducer unless the caller disabled it
// or the DD_RUNTIME_METRICS_ENABLED env var is set to false.
func withRuntimeProducerDefault() Option {
	return optionFunc(func(c *config) {
		if c.disableRuntimeProducer {
			return
		}
		// Respect the user's opt-out of runtime metrics reporting.
		// When DD_RUNTIME_METRICS_ENABLED=false, suppress automatic go.schedule.duration
		// collection so the RuntimeProducer scope doesn't appear in exported metrics.
		if runtimeMetricsDisabled := env.Get("DD_RUNTIME_METRICS_ENABLED"); runtimeMetricsDisabled == "false" || runtimeMetricsDisabled == "0" {
			return
		}
		c.producers = append(c.producers, NewRuntimeProducer())
	})
}

// NewMeterProvider creates a new MeterProvider configured with Datadog-specific settings:
// - Resource with DD service, env, version, hostname, and tags
// - OTLP HTTP exporter with DD defaults (localhost:4318, http/protobuf)
// - Delta temporality for all metrics (default)
// - 60-second export interval
//
// Metrics are enabled when DD_METRICS_OTEL_ENABLED is true or OTEL_METRICS_EXPORTER
// includes otlp. OTEL_METRICS_EXPORTER=none disables export.
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
		// Report to telemetry that metrics are disabled
		registerNoopTelemetry()
		// Return a no-op MeterProvider that doesn't export metrics
		return noop.NewMeterProvider(), nil
	}

	// Apply configuration options
	cfg := newConfig()
	for _, opt := range opts {
		opt.apply(cfg)
	}

	// Report configuration to telemetry
	registerTelemetry(cfg)

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
	readerOpts := []metric.PeriodicReaderOption{
		metric.WithInterval(cfg.exportInterval),
		metric.WithTimeout(cfg.exportTimeout),
	}
	for _, p := range cfg.producers {
		readerOpts = append(readerOpts, metric.WithProducer(p))
	}
	reader := metric.NewPeriodicReader(exporter, readerOpts...)

	// Create the MeterProvider
	return metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(reader),
	), nil
}

func isMetricsEnabled() bool {
	return metricsExportEnabled()
}

// isNoop returns true if the given MeterProvider is a no-op provider that doesn't export metrics.
func isNoop(mp otelmetric.MeterProvider) bool {
	_, ok := mp.(noop.MeterProvider)
	return ok
}

// Shutdown gracefully shuts down the MeterProvider, flushing any pending metrics.
// For no-op providers, this is a no-op operation.
func Shutdown(ctx context.Context, mp otelmetric.MeterProvider) error {
	if isNoop(mp) {
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
	if isNoop(mp) {
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
