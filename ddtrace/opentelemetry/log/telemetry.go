// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import "github.com/DataDog/dd-trace-go/v2/internal/telemetry"

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
		logRecordsHandle: telemetry.Count(telemetry.NamespaceTracers, "otel.log_records", tags),
	}
}

// RecordLogRecords records the number of log records exported.
func (t *LogsExportTelemetry) RecordLogRecords(count int) {
	if t != nil && t.logRecordsHandle != nil && count > 0 {
		t.logRecordsHandle.Submit(float64(count))
	}
}
