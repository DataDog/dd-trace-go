// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
)

func otelMetricsExporter() string {
	return strings.ToLower(strings.TrimSpace(env.Get("OTEL_METRICS_EXPORTER")))
}

func otelMetricsExporterIncludesOTLP() bool {
	for part := range strings.SplitSeq(otelMetricsExporter(), ",") {
		if strings.TrimSpace(part) == "otlp" {
			return true
		}
	}
	return false
}

// metricsExportEnabled reports whether the DD OTel MeterProvider should be installed.
func metricsExportEnabled() bool {
	if otelMetricsExporter() == "none" {
		return false
	}
	return boolEnv("DD_METRICS_OTEL_ENABLED") || otelMetricsExporterIncludesOTLP()
}

// runtimeMetricsEnabled reports whether Go runtime metrics collection should start.
func runtimeMetricsEnabled() bool {
	if otelMetricsExporter() == "none" {
		return false
	}
	return boolEnv("DD_RUNTIME_METRICS_ENABLED") &&
		boolEnv("DD_METRICS_OTEL_ENABLED") &&
		otelMetricsExporterIncludesOTLP()
}

func boolEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(env.Get(name))) {
	case "true", "1":
		return true
	}
	return false
}
