// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
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
	_, report := internalconfig.PrepareOTelLogSnapshot()
	report()
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
