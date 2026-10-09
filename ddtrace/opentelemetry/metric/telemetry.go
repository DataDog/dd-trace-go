// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"cmp"
	"strconv"
	"strings"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// Environment variable names for telemetry reporting
// Note: envOtelMetricsExporter and envDDMetricsOtelEnabled are defined in meter_provider.go
const (
	// Generic OTLP exporter configurations (apply to all signals)
	envOTLPTimeout = "OTEL_EXPORTER_OTLP_TIMEOUT"

	// Metrics-specific OTLP exporter configurations
	envOTLPMetricsTimeout = "OTEL_EXPORTER_OTLP_METRICS_TIMEOUT"

	// Default values (in milliseconds) per OTel spec
	defaultOTLPTimeoutMs = 10000 // 10 seconds
)

// registerTelemetry reports OTel metrics configuration to Datadog telemetry.
// This is called when the MeterProvider is created and metrics are enabled.
//
// Transport configuration is reported by internal/config.
func registerTelemetry(cfg *config) {
	telemetryConfigs := []telemetry.Configuration{}

	// ===========================================
	// Generic OTLP Exporter Configurations
	// (These apply to all signals, not just metrics)
	// ===========================================

	// OTEL_EXPORTER_OTLP_TIMEOUT
	if timeout := env.Get(envOTLPTimeout); timeout != "" {
		if ms, err := parseMilliseconds(timeout); err == nil {
			telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
				Name:   envOTLPTimeout,
				Value:  ms,
				Origin: telemetry.OriginEnvVar,
			})
		}
	}

	// ===========================================
	// Metrics-specific OTLP Exporter Configurations
	// ===========================================

	// OTEL_EXPORTER_OTLP_METRICS_TIMEOUT
	metricsTimeout := getMillisecondsConfig(envOTLPMetricsTimeout, defaultOTLPTimeoutMs)
	telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
		Name:   envOTLPMetricsTimeout,
		Value:  metricsTimeout.value,
		Origin: metricsTimeout.origin,
	})

	internalconfig.Get().ReportRuntimeMetricsReaderConfig()

	telemetry.RegisterAppConfigs(telemetryConfigs...)
}

// registerNoopTelemetry reports that OTel metrics are disabled.
func registerNoopTelemetry() {
	// No telemetry to report when metrics are disabled
}

// parseMilliseconds parses a string value as milliseconds.
// The value can be a plain integer (milliseconds) or a duration string.
func parseMilliseconds(value string) (int, error) {
	value = strings.TrimSpace(value)

	// Try parsing as integer (milliseconds)
	if ms, err := strconv.Atoi(value); err == nil {
		return ms, nil
	}

	// Could add support for duration strings like "10s" here if needed
	return 0, strconv.ErrSyntax
}

// msConfig holds a milliseconds configuration value with its origin.
type msConfig struct {
	value  int
	origin telemetry.Origin
}

// parseMsFromEnv attempts to parse a milliseconds value from an environment variable.
// Returns a zero msConfig if the env var is empty or parsing fails.
func parseMsFromEnv(envVar string) msConfig {
	if v := env.Get(envVar); v != "" {
		if ms, err := parseMilliseconds(v); err == nil {
			return msConfig{value: ms, origin: telemetry.OriginEnvVar}
		}
	}
	return msConfig{}
}

// getMillisecondsConfig reads a milliseconds value from an environment variable,
// falling back to the provided default. Uses cmp.Or to select the first valid config.
func getMillisecondsConfig(envVar string, defaultMs int) msConfig {
	return cmp.Or(
		parseMsFromEnv(envVar),
		msConfig{value: defaultMs, origin: telemetry.OriginDefault},
	)
}

// MetricsExportTelemetry provides telemetry metrics for OTLP metrics export operations.
type MetricsExportTelemetry struct {
	attemptsHandle  telemetry.MetricHandle
	successesHandle telemetry.MetricHandle
}

// NewMetricsExportTelemetry creates a new MetricsExportTelemetry for tracking export operations.
// The protocol should be "http" or "grpc", and encoding is typically "protobuf".
func NewMetricsExportTelemetry(protocol, encoding string) *MetricsExportTelemetry {
	tags := []string{
		"protocol:" + protocol,
		"encoding:" + encoding,
	}

	return &MetricsExportTelemetry{
		attemptsHandle:  telemetry.Count(telemetry.NamespaceTracers, "otel.metrics_export_attempts", tags),
		successesHandle: telemetry.Count(telemetry.NamespaceTracers, "otel.metrics_export_successes", tags),
	}
}

// RecordAttempt records a metrics export attempt.
func (t *MetricsExportTelemetry) RecordAttempt() {
	if t != nil && t.attemptsHandle != nil {
		t.attemptsHandle.Submit(1)
	}
}

// RecordSuccess records a successful metrics export.
func (t *MetricsExportTelemetry) RecordSuccess() {
	if t != nil && t.successesHandle != nil {
		t.successesHandle.Submit(1)
	}
}
