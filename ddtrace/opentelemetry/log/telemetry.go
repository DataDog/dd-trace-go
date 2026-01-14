// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"cmp"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
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
	telemetryConfigs := []telemetry.Configuration{}

	// ===========================================
	// Generic OTLP Exporter Configurations
	// (These apply to all signals, not just logs)
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
	// Logs-specific OTLP Exporter Configurations
	// ===========================================

	// OTEL_EXPORTER_OTLP_LOGS_TIMEOUT
	logsTimeout := getMillisecondsConfig(envOTLPLogsTimeout, defaultOTLPTimeoutMs)
	telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
		Name:   envOTLPLogsTimeout,
		Value:  logsTimeout.value,
		Origin: logsTimeout.origin,
	})

	// OTEL_EXPORTER_OTLP_LOGS_HEADERS
	if headers := env.Get(envOTLPLogsHeaders); headers != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOTLPLogsHeaders,
			Value:  headers,
			Origin: telemetry.OriginEnvVar,
		})
	}

	// OTEL_EXPORTER_OTLP_LOGS_PROTOCOL
	if protocol := env.Get(envOTLPLogsProtocol); protocol != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOTLPLogsProtocol,
			Value:  strings.ToLower(strings.TrimSpace(protocol)),
			Origin: telemetry.OriginEnvVar,
		})
	}

	// OTEL_EXPORTER_OTLP_LOGS_ENDPOINT
	if endpoint := env.Get(envOTLPLogsEndpoint); endpoint != "" {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
			Name:   envOTLPLogsEndpoint,
			Value:  endpoint,
			Origin: telemetry.OriginEnvVar,
		})
	}

	// ===========================================
	// BatchLogRecordProcessor Configurations
	// ===========================================

	// OTEL_BLRP_MAX_QUEUE_SIZE
	maxQueueSize := getIntConfig(envBLRPMaxQueueSize, defaultBLRPMaxQueueSize)
	telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
		Name:   envBLRPMaxQueueSize,
		Value:  maxQueueSize.value,
		Origin: maxQueueSize.origin,
	})

	// OTEL_BLRP_SCHEDULE_DELAY
	scheduleDelay := getMillisecondsConfig(envBLRPScheduleDelay, defaultBLRPScheduleDelayMs)
	telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
		Name:   envBLRPScheduleDelay,
		Value:  scheduleDelay.value,
		Origin: scheduleDelay.origin,
	})

	// OTEL_BLRP_EXPORT_TIMEOUT
	exportTimeout := getMillisecondsConfig(envBLRPExportTimeout, defaultBLRPExportTimeoutMs)
	telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{
		Name:   envBLRPExportTimeout,
		Value:  exportTimeout.value,
		Origin: exportTimeout.origin,
	})

	// OTEL_BLRP_MAX_EXPORT_BATCH_SIZE
	maxExportBatchSize := getIntConfig(envBLRPMaxExportBatchSize, defaultBLRPMaxExportBatchSize)
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

// parseIntFromEnv attempts to parse an integer value from an environment variable.
// Returns a zero msConfig if the env var is empty or parsing fails.
func parseIntFromEnv(envVar string) msConfig {
	if v := env.Get(envVar); v != "" {
		if val, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return msConfig{value: val, origin: telemetry.OriginEnvVar}
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

// getIntConfig reads an integer value from an environment variable,
// falling back to the provided default. Uses cmp.Or to select the first valid config.
func getIntConfig(envVar string, defaultVal int) msConfig {
	return cmp.Or(
		parseIntFromEnv(envVar),
		msConfig{value: defaultVal, origin: telemetry.OriginDefault},
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
