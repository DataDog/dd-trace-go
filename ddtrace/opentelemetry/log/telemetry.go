// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"cmp"
	"strconv"
	"strings"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// Note: Environment variable constants are defined in exporter.go
// Note: Default millisecond values are defined in exporter.go

// registerTelemetry reports OTel logs configuration to Datadog telemetry.
// This is called when the LoggerProvider is initialized and logs are enabled.
//
// Configuration telemetry includes:
//   - Generic OTLP Exporter Configurations: OTEL_EXPORTER_OTLP_TIMEOUT, OTEL_EXPORTER_OTLP_HEADERS,
//     OTEL_EXPORTER_OTLP_PROTOCOL, OTEL_EXPORTER_OTLP_ENDPOINT
//   - Logs-specific OTLP Exporter Configurations: OTEL_EXPORTER_OTLP_LOGS_TIMEOUT,
//     OTEL_EXPORTER_OTLP_LOGS_HEADERS, OTEL_EXPORTER_OTLP_LOGS_PROTOCOL, OTEL_EXPORTER_OTLP_LOGS_ENDPOINT
//   - BatchLogRecordProcessor Configurations: OTEL_BLRP_MAX_QUEUE_SIZE, OTEL_BLRP_SCHEDULE_DELAY,
//     OTEL_BLRP_EXPORT_TIMEOUT, OTEL_BLRP_MAX_EXPORT_BATCH_SIZE
func registerTelemetry() {
	cfg := internalconfig.Get()
	telemetryConfigs := []telemetry.Configuration{}

	// ===========================================
	// Generic OTLP Exporter Configurations
	// (These apply to all signals, not just logs)
	// ===========================================

	// OTEL_EXPORTER_OTLP_TIMEOUT
	// Always report this (with default) since it's used as fallback for logs timeout
	genericTimeout := getMillisecondsConfig(cfg.OTelExporterOTLPTimeout(), defaultOTLPTimeoutMs)
	telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
		Name:   envOTLPTimeout,
		Value:  genericTimeout.value,
		Origin: genericTimeout.origin,
	})

	// OTEL_EXPORTER_OTLP_HEADERS
	if headers := cfg.OTelExporterOTLPHeaders(); headers != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOTLPHeaders,
			Value:  headers,
			Origin: telemetry.OriginEnvVar,
		})
	}

	// OTEL_EXPORTER_OTLP_PROTOCOL
	if protocol := cfg.OTelExporterOTLPProtocol(); protocol != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOTLPProtocol,
			Value:  strings.ToLower(strings.TrimSpace(protocol)),
			Origin: telemetry.OriginEnvVar,
		})
	}

	// OTEL_EXPORTER_OTLP_ENDPOINT
	if endpoint := cfg.OTelExporterOTLPEndpoint(); endpoint != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOTLPEndpoint,
			Value:  endpoint,
			Origin: telemetry.OriginEnvVar,
		})
	}

	// ===========================================
	// Logs-specific OTLP Exporter Configurations
	// ===========================================

	// OTEL_EXPORTER_OTLP_LOGS_TIMEOUT
	logsTimeout := getMillisecondsConfig(cfg.OTelExporterOTLPLogsTimeout(), defaultOTLPTimeoutMs)
	telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
		Name:   envOTLPLogsTimeout,
		Value:  logsTimeout.value,
		Origin: logsTimeout.origin,
	})

	// OTEL_EXPORTER_OTLP_LOGS_HEADERS
	if headers := cfg.OTelExporterOTLPLogsHeaders(); headers != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOTLPLogsHeaders,
			Value:  headers,
			Origin: telemetry.OriginEnvVar,
		})
	}

	// OTEL_EXPORTER_OTLP_LOGS_PROTOCOL
	if protocol := cfg.OTelExporterOTLPLogsProtocol(); protocol != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOTLPLogsProtocol,
			Value:  strings.ToLower(strings.TrimSpace(protocol)),
			Origin: telemetry.OriginEnvVar,
		})
	}

	// OTEL_EXPORTER_OTLP_LOGS_ENDPOINT
	if endpoint := cfg.OTelExporterOTLPLogsEndpoint(); endpoint != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOTLPLogsEndpoint,
			Value:  endpoint,
			Origin: telemetry.OriginEnvVar,
		})
	}

	// ===========================================
	// BatchLogRecordProcessor Configurations
	// ===========================================

	maxQueueSizeValue, scheduleDelayValue, exportTimeoutValue, maxExportBatchSizeValue := cfg.OTelBLRPConfig()

	// OTEL_BLRP_MAX_QUEUE_SIZE
	maxQueueSize := getIntConfig(maxQueueSizeValue, defaultBLRPMaxQueueSize)
	telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
		Name:   envBLRPMaxQueueSize,
		Value:  maxQueueSize.value,
		Origin: maxQueueSize.origin,
	})

	// OTEL_BLRP_SCHEDULE_DELAY
	scheduleDelay := getMillisecondsConfig(scheduleDelayValue, defaultBLRPScheduleDelayMs)
	telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
		Name:   envBLRPScheduleDelay,
		Value:  scheduleDelay.value,
		Origin: scheduleDelay.origin,
	})

	// OTEL_BLRP_EXPORT_TIMEOUT
	exportTimeout := getMillisecondsConfig(exportTimeoutValue, defaultBLRPExportTimeoutMs)
	telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
		Name:   envBLRPExportTimeout,
		Value:  exportTimeout.value,
		Origin: exportTimeout.origin,
	})

	// OTEL_BLRP_MAX_EXPORT_BATCH_SIZE
	maxExportBatchSize := getIntConfig(maxExportBatchSizeValue, defaultBLRPMaxExportBatchSize)
	telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
		Name:   envBLRPMaxExportBatchSize,
		Value:  maxExportBatchSize.value,
		Origin: maxExportBatchSize.origin,
	})

	telemetry.RegisterAppConfigs(telemetryConfigs...)
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

// configValue holds a configuration value (typically in milliseconds) with its origin.
// Used for telemetry reporting to track whether a value came from environment
// variables or defaults.
type configValue struct {
	value  int
	origin telemetry.Origin
}

// parseMsConfig returns an unset value when parsing fails.
func parseMsConfig(value string) configValue {
	if value != "" {
		if ms, err := parseMilliseconds(value); err == nil {
			return configValue{value: ms, origin: telemetry.OriginEnvVar}
		}
	}
	return configValue{}
}

// parseIntConfig returns an unset value when parsing fails.
func parseIntConfig(value string) configValue {
	if value != "" {
		if val, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			return configValue{value: val, origin: telemetry.OriginEnvVar}
		}
	}
	return configValue{}
}

func getMillisecondsConfig(value string, defaultMs int) configValue {
	return cmp.Or(
		parseMsConfig(value),
		configValue{value: defaultMs, origin: telemetry.OriginDefault},
	)
}

func getIntConfig(value string, defaultVal int) configValue {
	return cmp.Or(
		parseIntConfig(value),
		configValue{value: defaultVal, origin: telemetry.OriginDefault},
	)
}

// LogsExportTelemetry provides telemetry metrics for OTLP logs export operations.
type LogsExportTelemetry struct {
	logRecordsHandle telemetry.MetricHandle
}

// NewLogsExportTelemetry creates a new LogsExportTelemetry for tracking log export operations.
// The protocol should be "http" or "grpc", and encoding should be "json" or "protobuf".
func NewLogsExportTelemetry(protocol, encoding string) *LogsExportTelemetry {
	tags := []string{
		"protocol:" + protocol,
		"encoding:" + encoding,
	}

	return &LogsExportTelemetry{
		logRecordsHandle: telemetry.Count(telemetry.NamespaceGeneral, "otel.log_records", tags),
	}
}

// RecordLogRecords records the number of log records exported.
func (t *LogsExportTelemetry) RecordLogRecords(count int) {
	if t != nil && t.logRecordsHandle != nil && count > 0 {
		t.logRecordsHandle.Submit(float64(count))
	}
}
