// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/otelmetricsinstall"
)

const (
	// Environment variables for controlling metrics behavior
	envDDMetricsOtelEnabled = "DD_METRICS_OTEL_ENABLED"
	envOtelMetricsExporter  = "OTEL_METRICS_EXPORTER"
)

// installedProvider is the MeterProvider we installed as the OTel global.
// Accessed only from StartHook and ShutdownHook, both of which run under tracer's startStopMu.
// ShutdownHook only shuts down this provider, never one the user installed.
var installedProvider *metric.MeterProvider

func init() {
	otelmetricsinstall.StartHook = func(ctx context.Context) error {
		if err := installGlobal(); err != nil {
			return err
		}
		return startGoRuntimeMetrics(ctx)
	}
	otelmetricsinstall.ShutdownHook = func(ctx context.Context) error {
		p := installedProvider
		installedProvider = nil
		if p == nil {
			return nil
		}
		err := p.Shutdown(ctx)
		otel.SetMeterProvider(noop.NewMeterProvider())
		return err
	}
}

// installGlobal installs a DD-configured MeterProvider as the OTel global.
// Called only from StartHook after the tracer has already verified that metrics
// should be enabled — no further enablement checks are needed here.
func installGlobal(opts ...Option) error {
	// Already installed by us.
	if installedProvider != nil {
		return nil
	}
	// Don't replace a real OTel SDK MeterProvider the user installed themselves.
	// The OTel global defaults to a delegating provider (not a real SDK) that
	// silently drops metrics; we only skip installation if a real SDK provider
	// is already in place.
	if _, ok := otel.GetMeterProvider().(*metric.MeterProvider); ok {
		return nil
	}
	allOpts := append(opts,
		optionFunc(func(cfg *config) {
			cfg.ddConfig = internalconfig.Get()
		}),
		WithProducer(NewRuntimeProducer()),
	)
	mp, err := NewMeterProvider(allOpts...)
	if err != nil {
		return err
	}
	sdkMP, ok := mp.(*metric.MeterProvider)
	if !ok {
		// metrics were disabled by the time NewMeterProvider ran; nothing to install.
		return nil
	}
	otel.SetMeterProvider(sdkMP)
	installedProvider = sdkMP
	return nil
}

// NewMeterProvider creates a new MeterProvider configured with Datadog-specific settings:
// - Resource with DD service, env, version, hostname, and tags
// - OTLP HTTP exporter with DD defaults (localhost:4318, http/protobuf)
// - Delta temporality for all metrics (default)
// - 60-second export interval
//
// Users can override these defaults by passing additional options.
func NewMeterProvider(opts ...Option) (otelmetric.MeterProvider, error) {
	return NewMeterProviderWithContext(context.Background(), opts...)
}

func NewMeterProviderWithContext(ctx context.Context, opts ...Option) (otelmetric.MeterProvider, error) {

	cfg := newConfig()
	for _, opt := range opts {
		opt.apply(cfg)
	}

	if !metricsEnabled(cfg.ddConfig) {
		// Report to telemetry that metrics are disabled
		registerNoopTelemetry()
		// Return a no-op MeterProvider that doesn't export metrics
		return noop.NewMeterProvider(), nil
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

func metricsEnabled(c *internalconfig.Config) bool {
	if c != nil {
		return c.RuntimeMetricsOtelEnabled() && c.OTLPExportMetricsMode()
	}
	if exporter := env.Get(envOtelMetricsExporter); exporter != "" {
		if strings.ToLower(strings.TrimSpace(exporter)) == "none" {
			return false
		}
	}
	switch strings.ToLower(strings.TrimSpace(env.Get(envDDMetricsOtelEnabled))) {
	case "true", "1":
		return true
	}
	return false
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
