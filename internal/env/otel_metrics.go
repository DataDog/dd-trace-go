// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package env

import "strings"

// OtelMetricsExporter returns a normalized OTEL_METRICS_EXPORTER value (lowercase, trimmed).
func OtelMetricsExporter() string {
	return strings.ToLower(strings.TrimSpace(Get("OTEL_METRICS_EXPORTER")))
}

// OtelMetricsExporterIncludesOTLP reports whether OTEL_METRICS_EXPORTER selects OTLP export.
func OtelMetricsExporterIncludesOTLP() bool {
	exporter := OtelMetricsExporter()
	if exporter == "" {
		return false
	}
	for part := range strings.SplitSeq(exporter, ",") {
		if strings.TrimSpace(part) == "otlp" {
			return true
		}
	}
	return false
}

// MetricsExportEnabled reports whether Datadog OTel metric export (InstallGlobal / MeterProvider) should be active.
// Enabled when DD_METRICS_OTEL_ENABLED is true or OTEL_METRICS_EXPORTER includes otlp.
// Disabled when OTEL_METRICS_EXPORTER is none.
func MetricsExportEnabled() bool {
	switch OtelMetricsExporter() {
	case "none":
		return false
	}
	if OtelMetricsExporterIncludesOTLP() {
		return true
	}
	return envBoolTrue("DD_METRICS_OTEL_ENABLED")
}

// OtelRuntimeMetricsEnabled reports whether OTel Go runtime metrics (semconv) should be collected.
func OtelRuntimeMetricsEnabled() bool {
	if OtelMetricsExporter() == "none" {
		return false
	}
	return envBoolTrue("DD_RUNTIME_METRICS_ENABLED") &&
		envBoolTrue("DD_METRICS_OTEL_ENABLED") &&
		OtelMetricsExporterIncludesOTLP()
}

func envBoolTrue(name string) bool {
	switch strings.ToLower(strings.TrimSpace(Get(name))) {
	case "true", "1":
		return true
	default:
		return false
	}
}
