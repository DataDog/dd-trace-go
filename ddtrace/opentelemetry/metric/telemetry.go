// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// Environment variable names for telemetry reporting
// Note: envOtelMetricsExporter and envDDMetricsOtelEnabled are defined in meter_provider.go
const (
	envOtelMetricExportInterval = "OTEL_METRIC_EXPORT_INTERVAL"
	envOtelMetricExportTimeout  = "OTEL_METRIC_EXPORT_TIMEOUT"
	envOTLPMetricsHeaders       = "OTEL_EXPORTER_OTLP_METRICS_HEADERS"
	envOTLPMetricsTimeout       = "OTEL_EXPORTER_OTLP_METRICS_TIMEOUT"
)

// registerTelemetry reports OTel metrics configuration to Datadog telemetry.
// This is called when the MeterProvider is created and metrics are enabled.
//
// Configuration telemetry includes:
//   - Datadog SDK Configurations: DD_METRICS_OTEL_ENABLED
//   - OpenTelemetry Metrics SDK Configurations: OTEL_METRICS_EXPORTER, OTEL_METRIC_EXPORT_INTERVAL, OTEL_METRIC_EXPORT_TIMEOUT
//   - OTLP Exporter Configurations: OTEL_EXPORTER_OTLP_METRICS_PROTOCOL, OTEL_EXPORTER_OTLP_METRICS_ENDPOINT,
//     OTEL_EXPORTER_OTLP_METRICS_HEADERS, OTEL_EXPORTER_OTLP_METRICS_TIMEOUT, OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE
//
// Telemetry names follow snake_case convention (e.g., "otel_metrics_enabled" for DD_METRICS_OTEL_ENABLED)
func registerTelemetry(cfg *config) {
	telemetryConfigs := []telemetry.Configuration{}

	// ===========================================
	// Datadog SDK Configurations
	// ===========================================

	// DD_METRICS_OTEL_ENABLED: Track adoption by customer/service
	telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
		Name:   "otel_metrics_enabled",
		Value:  true,
		Origin: originForEnv(envDDMetricsOtelEnabled),
	})

	// ===========================================
	// OpenTelemetry Metrics SDK Configurations
	// ===========================================

	// OTEL_METRICS_EXPORTER: Defaults to "otlp", can be prometheus, console, none
	otelMetricsExporter := env.Get(envOtelMetricsExporter)
	if otelMetricsExporter == "" {
		otelMetricsExporter = "otlp" // default
	}
	telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
		Name:   "otel_metrics_exporter",
		Value:  otelMetricsExporter,
		Origin: originForEnv(envOtelMetricsExporter),
	})

	// OTEL_METRIC_EXPORT_INTERVAL: Export interval for periodic reader
	otelExportInterval := env.Get(envOtelMetricExportInterval)
	if otelExportInterval != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   "otel_metric_export_interval",
			Value:  otelExportInterval,
			Origin: telemetry.OriginEnvVar,
		})
	} else {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   "otel_metric_export_interval",
			Value:  cfg.exportInterval.String(),
			Origin: telemetry.OriginDefault,
		})
	}

	// OTEL_METRIC_EXPORT_TIMEOUT: Export timeout for periodic reader
	otelExportTimeout := env.Get(envOtelMetricExportTimeout)
	if otelExportTimeout != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   "otel_metric_export_timeout",
			Value:  otelExportTimeout,
			Origin: telemetry.OriginEnvVar,
		})
	} else {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   "otel_metric_export_timeout",
			Value:  cfg.exportTimeout.String(),
			Origin: telemetry.OriginDefault,
		})
	}

	// ===========================================
	// OTLP Exporter Configurations
	// ===========================================

	// OTEL_EXPORTER_OTLP_METRICS_PROTOCOL: Track usage of each protocol (http/protobuf, grpc)
	protocol := otlpProtocol()
	telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
		Name:   "otel_exporter_otlp_metrics_protocol",
		Value:  protocol,
		Origin: originForProtocol(),
	})

	// OTEL_EXPORTER_OTLP_METRICS_ENDPOINT: Track non-standard endpoint usage
	endpoint, endpointOrigin := resolvedEndpointWithOrigin(protocol)
	telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
		Name:   "otel_exporter_otlp_metrics_endpoint",
		Value:  endpoint,
		Origin: endpointOrigin,
	})

	// OTEL_EXPORTER_OTLP_METRICS_HEADERS: Track custom header usage (value redacted for security)
	otelMetricsHeaders := env.Get(envOTLPMetricsHeaders)
	if otelMetricsHeaders != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   "otel_exporter_otlp_metrics_headers",
			Value:  "<redacted>", // Don't send actual header values for security
			Origin: telemetry.OriginEnvVar,
		})
	}

	// OTEL_EXPORTER_OTLP_METRICS_TIMEOUT: Track custom timeout for OTLP exporter
	otelMetricsTimeout := env.Get(envOTLPMetricsTimeout)
	if otelMetricsTimeout != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   "otel_exporter_otlp_metrics_timeout",
			Value:  otelMetricsTimeout,
			Origin: telemetry.OriginEnvVar,
		})
	}

	// OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE: Track delta vs cumulative usage
	// Default is "delta" for best Datadog experience (counter to OTel default of "cumulative")
	temporalityPref := strings.ToLower(strings.TrimSpace(env.Get(envOTLPMetricsTemporality)))
	if temporalityPref == "" {
		temporalityPref = "delta" // Datadog default
	}
	telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
		Name:   "otel_exporter_otlp_metrics_temporality_preference",
		Value:  temporalityPref,
		Origin: originForEnv(envOTLPMetricsTemporality),
	})

	telemetry.RegisterAppConfigs(telemetryConfigs...)
}

// registerNoopTelemetry reports that OTel metrics are disabled.
func registerNoopTelemetry() {
	telemetryConfigs := []telemetry.Configuration{}

	// DD_METRICS_OTEL_ENABLED: Track when feature is disabled
	origin := originForEnv(envDDMetricsOtelEnabled)

	// If OTEL_METRICS_EXPORTER=none was set, that's the origin
	if exporter := env.Get(envOtelMetricsExporter); strings.ToLower(strings.TrimSpace(exporter)) == "none" {
		origin = telemetry.OriginEnvVar
	}

	telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
		Name:   "otel_metrics_enabled",
		Value:  false,
		Origin: origin,
	})

	// OTEL_METRICS_EXPORTER: Report if set (especially if "none")
	otelMetricsExporter := env.Get(envOtelMetricsExporter)
	if otelMetricsExporter != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   "otel_metrics_exporter",
			Value:  strings.ToLower(strings.TrimSpace(otelMetricsExporter)),
			Origin: telemetry.OriginEnvVar,
		})
	}

	telemetry.RegisterAppConfigs(telemetryConfigs...)
}

// originForEnv returns the telemetry origin based on whether an env var is set.
func originForEnv(envVar string) telemetry.Origin {
	if env.Get(envVar) != "" {
		return telemetry.OriginEnvVar
	}
	return telemetry.OriginDefault
}

// originForProtocol determines the origin of the protocol configuration.
func originForProtocol() telemetry.Origin {
	if env.Get(envOTLPMetricsProtocol) != "" {
		return telemetry.OriginEnvVar
	}
	if env.Get(envOTLPProtocol) != "" {
		return telemetry.OriginEnvVar
	}
	return telemetry.OriginDefault
}

// resolvedEndpointWithOrigin returns the resolved endpoint and its origin.
func resolvedEndpointWithOrigin(protocol string) (string, telemetry.Origin) {
	// Check OTEL endpoint env vars first (highest priority)
	if endpoint := env.Get(envOTLPMetricsEndpoint); endpoint != "" {
		return endpoint, telemetry.OriginEnvVar
	}
	if endpoint := env.Get(envOTLPEndpoint); endpoint != "" {
		return endpoint, telemetry.OriginEnvVar
	}

	// Check DD env vars
	if env.Get(envDDTraceAgentURL) != "" {
		if protocol == "grpc" {
			endpoint, _ := resolveOTLPEndpointGRPC()
			return endpoint, telemetry.OriginEnvVar
		}
		endpoint, _, _ := resolveOTLPEndpointHTTP()
		return endpoint, telemetry.OriginEnvVar
	}

	if env.Get(envDDAgentHost) != "" {
		if protocol == "grpc" {
			endpoint, _ := resolveOTLPEndpointGRPC()
			return endpoint, telemetry.OriginEnvVar
		}
		endpoint, _, _ := resolveOTLPEndpointHTTP()
		return endpoint, telemetry.OriginEnvVar
	}

	// Default endpoint
	if protocol == "grpc" {
		return "localhost:4317", telemetry.OriginDefault
	}
	return "localhost:4318", telemetry.OriginDefault
}

// reportExportIntervalChange reports a change to the export interval via telemetry.
// This can be called when the export interval is changed via WithExportInterval.
func reportExportIntervalChange(interval time.Duration) {
	telemetry.RegisterAppConfigs(telemetry.Configuration{
		Name:   "otel_metric_export_interval",
		Value:  interval.String(),
		Origin: telemetry.OriginCode,
	})
}

// reportExportTimeoutChange reports a change to the export timeout via telemetry.
// This can be called when the export timeout is changed via WithExportTimeout.
func reportExportTimeoutChange(timeout time.Duration) {
	telemetry.RegisterAppConfigs(telemetry.Configuration{
		Name:   "otel_metric_export_timeout",
		Value:  timeout.String(),
		Origin: telemetry.OriginCode,
	})
}
