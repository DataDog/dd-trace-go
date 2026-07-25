// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
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

// registerNoopTelemetry reports that OTel metrics are disabled.
func registerNoopTelemetry() {
	// No telemetry to report when metrics are disabled
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
