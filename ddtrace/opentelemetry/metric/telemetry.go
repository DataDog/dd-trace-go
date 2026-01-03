// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// Environment variable names for telemetry reporting
// Note: envOtelMetricsExporter and envDDMetricsOtelEnabled are defined in meter_provider.go
const (
	// OTel Metrics SDK configurations
	envOtelMetricExportInterval = "OTEL_METRIC_EXPORT_INTERVAL"
	envOtelMetricExportTimeout  = "OTEL_METRIC_EXPORT_TIMEOUT"

	// Generic OTLP exporter configurations (apply to all signals)
	envOTLPTimeout = "OTEL_EXPORTER_OTLP_TIMEOUT"
	envOTLPHeaders = "OTEL_EXPORTER_OTLP_HEADERS"

	// Metrics-specific OTLP exporter configurations
	envOTLPMetricsHeaders = "OTEL_EXPORTER_OTLP_METRICS_HEADERS"
	envOTLPMetricsTimeout = "OTEL_EXPORTER_OTLP_METRICS_TIMEOUT"

	// Default values (in milliseconds) per OTel spec
	defaultExportIntervalMs = 10000 // 10 seconds
	defaultExportTimeoutMs  = 7500  // 7.5 seconds (75% of interval, per OTel spec)
	defaultOTLPTimeoutMs    = 10000 // 10 seconds
)

// registerTelemetry reports OTel metrics configuration to Datadog telemetry.
// This is called when the MeterProvider is created and metrics are enabled.
//
// Configuration telemetry includes:
//   - Generic OTLP Exporter Configurations: OTEL_EXPORTER_OTLP_TIMEOUT, OTEL_EXPORTER_OTLP_HEADERS,
//     OTEL_EXPORTER_OTLP_PROTOCOL, OTEL_EXPORTER_OTLP_ENDPOINT
//   - Metrics-specific OTLP Exporter Configurations: OTEL_EXPORTER_OTLP_METRICS_TIMEOUT,
//     OTEL_EXPORTER_OTLP_METRICS_HEADERS, OTEL_EXPORTER_OTLP_METRICS_PROTOCOL, OTEL_EXPORTER_OTLP_METRICS_ENDPOINT
//   - OpenTelemetry Metrics SDK Configurations: OTEL_METRIC_EXPORT_INTERVAL, OTEL_METRIC_EXPORT_TIMEOUT
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

	// OTEL_EXPORTER_OTLP_HEADERS
	if headers := env.Get(envOTLPHeaders); headers != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOTLPHeaders,
			Value:  headers,
			Origin: telemetry.OriginEnvVar,
		})
	}

	// OTEL_EXPORTER_OTLP_PROTOCOL
	if protocol := env.Get(envOTLPProtocol); protocol != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOTLPProtocol,
			Value:  strings.ToLower(strings.TrimSpace(protocol)),
			Origin: telemetry.OriginEnvVar,
		})
	}

	// OTEL_EXPORTER_OTLP_ENDPOINT
	if endpoint := env.Get(envOTLPEndpoint); endpoint != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOTLPEndpoint,
			Value:  endpoint,
			Origin: telemetry.OriginEnvVar,
		})
	}

	// ===========================================
	// Metrics-specific OTLP Exporter Configurations
	// ===========================================

	// OTEL_EXPORTER_OTLP_METRICS_TIMEOUT
	if timeout := env.Get(envOTLPMetricsTimeout); timeout != "" {
		if ms, err := parseMilliseconds(timeout); err == nil {
			telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
				Name:   envOTLPMetricsTimeout,
				Value:  ms,
				Origin: telemetry.OriginEnvVar,
			})
		}
	} else {
		// Report default value
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOTLPMetricsTimeout,
			Value:  defaultOTLPTimeoutMs,
			Origin: telemetry.OriginDefault,
		})
	}

	// OTEL_EXPORTER_OTLP_METRICS_HEADERS
	if headers := env.Get(envOTLPMetricsHeaders); headers != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOTLPMetricsHeaders,
			Value:  headers,
			Origin: telemetry.OriginEnvVar,
		})
	}

	// OTEL_EXPORTER_OTLP_METRICS_PROTOCOL
	if protocol := env.Get(envOTLPMetricsProtocol); protocol != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOTLPMetricsProtocol,
			Value:  strings.ToLower(strings.TrimSpace(protocol)),
			Origin: telemetry.OriginEnvVar,
		})
	}

	// OTEL_EXPORTER_OTLP_METRICS_ENDPOINT
	if endpoint := env.Get(envOTLPMetricsEndpoint); endpoint != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOTLPMetricsEndpoint,
			Value:  endpoint,
			Origin: telemetry.OriginEnvVar,
		})
	}

	// ===========================================
	// OpenTelemetry Metrics SDK Configurations
	// ===========================================

	// OTEL_METRIC_EXPORT_INTERVAL
	if interval := env.Get(envOtelMetricExportInterval); interval != "" {
		if ms, err := parseMilliseconds(interval); err == nil {
			telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
				Name:   envOtelMetricExportInterval,
				Value:  ms,
				Origin: telemetry.OriginEnvVar,
			})
		}
	} else {
		// Report default value
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOtelMetricExportInterval,
			Value:  defaultExportIntervalMs,
			Origin: telemetry.OriginDefault,
		})
	}

	// OTEL_METRIC_EXPORT_TIMEOUT
	if timeout := env.Get(envOtelMetricExportTimeout); timeout != "" {
		if ms, err := parseMilliseconds(timeout); err == nil {
			telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
				Name:   envOtelMetricExportTimeout,
				Value:  ms,
				Origin: telemetry.OriginEnvVar,
			})
		}
	} else {
		// Report default value
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOtelMetricExportTimeout,
			Value:  defaultExportTimeoutMs,
			Origin: telemetry.OriginDefault,
		})
	}

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
		attemptsHandle:  telemetry.Count(telemetry.NamespaceGeneral, "otel.metrics_export_attempts", tags),
		successesHandle: telemetry.Count(telemetry.NamespaceGeneral, "otel.metrics_export_successes", tags),
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
