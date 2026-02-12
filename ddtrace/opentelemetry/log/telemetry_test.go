// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// TestRegisterTelemetry verifies that registerTelemetry reports all expected configurations.
func TestRegisterTelemetry(t *testing.T) {
	t.Run("reports all OTLP configurations", func(t *testing.T) {
		recorder := &telemetrytest.RecordClient{}
		defer telemetry.MockClient(recorder)()

		t.Setenv(envOTLPTimeout, "5000")
		t.Setenv(envOTLPHeaders, "api-key=secret")
		t.Setenv(envOTLPProtocol, "http/protobuf")
		t.Setenv(envOTLPEndpoint, "http://example.com:4318")
		t.Setenv(envOTLPLogsTimeout, "8000")
		t.Setenv(envOTLPLogsHeaders, "log-key=value")
		t.Setenv(envOTLPLogsProtocol, "grpc")
		t.Setenv(envOTLPLogsEndpoint, "http://logs.example.com:4317")

		registerTelemetry()

		// Verify generic OTLP configurations
		telemetrytest.CheckConfig(t, recorder.Configuration, envOTLPTimeout, 5000)
		telemetrytest.CheckConfig(t, recorder.Configuration, envOTLPHeaders, "api-key=secret")
		telemetrytest.CheckConfig(t, recorder.Configuration, envOTLPProtocol, "http/protobuf")
		telemetrytest.CheckConfig(t, recorder.Configuration, envOTLPEndpoint, "http://example.com:4318")

		// Verify logs-specific configurations
		telemetrytest.CheckConfig(t, recorder.Configuration, envOTLPLogsTimeout, 8000)
		telemetrytest.CheckConfig(t, recorder.Configuration, envOTLPLogsHeaders, "log-key=value")
		telemetrytest.CheckConfig(t, recorder.Configuration, envOTLPLogsProtocol, "grpc")
		telemetrytest.CheckConfig(t, recorder.Configuration, envOTLPLogsEndpoint, "http://logs.example.com:4317")
	})

	t.Run("reports BLRP configurations", func(t *testing.T) {
		recorder := &telemetrytest.RecordClient{}
		defer telemetry.MockClient(recorder)()

		t.Setenv(envBLRPMaxQueueSize, "4096")
		t.Setenv(envBLRPScheduleDelay, "2000")
		t.Setenv(envBLRPExportTimeout, "60000")
		t.Setenv(envBLRPMaxExportBatchSize, "1024")

		registerTelemetry()

		telemetrytest.CheckConfig(t, recorder.Configuration, envBLRPMaxQueueSize, 4096)
		telemetrytest.CheckConfig(t, recorder.Configuration, envBLRPScheduleDelay, 2000)
		telemetrytest.CheckConfig(t, recorder.Configuration, envBLRPExportTimeout, 60000)
		telemetrytest.CheckConfig(t, recorder.Configuration, envBLRPMaxExportBatchSize, 1024)
	})

	t.Run("reports default values when env vars not set", func(t *testing.T) {
		recorder := &telemetrytest.RecordClient{}
		defer telemetry.MockClient(recorder)()

		registerTelemetry()

		// Check that defaults are reported with OriginDefault
		var foundLogsTimeout, foundMaxQueueSize, foundScheduleDelay, foundExportTimeout, foundMaxBatchSize bool

		for _, cfg := range recorder.Configuration {
			switch cfg.Name {
			case envOTLPLogsTimeout:
				foundLogsTimeout = true
				assert.Equal(t, defaultOTLPTimeoutMs, cfg.Value)
				assert.Equal(t, telemetry.OriginDefault, cfg.Origin)
			case envBLRPMaxQueueSize:
				foundMaxQueueSize = true
				assert.Equal(t, defaultBLRPMaxQueueSize, cfg.Value)
				assert.Equal(t, telemetry.OriginDefault, cfg.Origin)
			case envBLRPScheduleDelay:
				foundScheduleDelay = true
				assert.Equal(t, defaultBLRPScheduleDelayMs, cfg.Value)
				assert.Equal(t, telemetry.OriginDefault, cfg.Origin)
			case envBLRPExportTimeout:
				foundExportTimeout = true
				assert.Equal(t, defaultBLRPExportTimeoutMs, cfg.Value)
				assert.Equal(t, telemetry.OriginDefault, cfg.Origin)
			case envBLRPMaxExportBatchSize:
				foundMaxBatchSize = true
				assert.Equal(t, defaultBLRPMaxExportBatchSize, cfg.Value)
				assert.Equal(t, telemetry.OriginDefault, cfg.Origin)
			}
		}

		assert.True(t, foundLogsTimeout, "expected OTEL_EXPORTER_OTLP_LOGS_TIMEOUT config")
		assert.True(t, foundMaxQueueSize, "expected OTEL_BLRP_MAX_QUEUE_SIZE config")
		assert.True(t, foundScheduleDelay, "expected OTEL_BLRP_SCHEDULE_DELAY config")
		assert.True(t, foundExportTimeout, "expected OTEL_BLRP_EXPORT_TIMEOUT config")
		assert.True(t, foundMaxBatchSize, "expected OTEL_BLRP_MAX_EXPORT_BATCH_SIZE config")
	})
}

// TestLogsExportTelemetry verifies that the LogsExportTelemetry struct correctly
// tracks log record exports with different protocols and encodings.
func TestLogsExportTelemetry(t *testing.T) {
	t.Run("http/json", func(t *testing.T) {
		recorder := &telemetrytest.RecordClient{}
		defer telemetry.MockClient(recorder)()

		// Create telemetry tracker for HTTP/JSON
		let := NewLogsExportTelemetry("http", "json")

		// Record some log exports
		let.RecordLogRecords(5)
		let.RecordLogRecords(10)
		let.RecordLogRecords(3)

		// Check that metrics were recorded
		key := telemetrytest.MetricKey{
			Namespace: telemetry.NamespaceGeneral,
			Name:      "otel.log_records",
			Tags:      "encoding:json,protocol:http",
			Kind:      "count",
		}

		assert.Contains(t, recorder.Metrics, key, "expected otel.log_records metric")
		if handle, ok := recorder.Metrics[key]; ok {
			assert.Equal(t, float64(18), handle.Get(), "expected total count of 18 log records (5+10+3)")
		}
	})

	t.Run("http/protobuf", func(t *testing.T) {
		recorder := &telemetrytest.RecordClient{}
		defer telemetry.MockClient(recorder)()

		// Create telemetry tracker for HTTP/protobuf
		let := NewLogsExportTelemetry("http", "protobuf")

		let.RecordLogRecords(7)

		// Check that metrics were recorded with correct tags
		key := telemetrytest.MetricKey{
			Namespace: telemetry.NamespaceGeneral,
			Name:      "otel.log_records",
			Tags:      "encoding:protobuf,protocol:http",
			Kind:      "count",
		}

		assert.Contains(t, recorder.Metrics, key, "expected otel.log_records metric with protobuf tag")
		if handle, ok := recorder.Metrics[key]; ok {
			assert.Equal(t, float64(7), handle.Get(), "expected 7 log records")
		}
	})

	t.Run("grpc/protobuf", func(t *testing.T) {
		recorder := &telemetrytest.RecordClient{}
		defer telemetry.MockClient(recorder)()

		// Create telemetry tracker for gRPC/protobuf
		let := NewLogsExportTelemetry("grpc", "protobuf")

		let.RecordLogRecords(12)

		// Check that metrics were recorded with correct tags
		key := telemetrytest.MetricKey{
			Namespace: telemetry.NamespaceGeneral,
			Name:      "otel.log_records",
			Tags:      "encoding:protobuf,protocol:grpc",
			Kind:      "count",
		}

		assert.Contains(t, recorder.Metrics, key, "expected otel.log_records metric with grpc tag")
		if handle, ok := recorder.Metrics[key]; ok {
			assert.Equal(t, float64(12), handle.Get(), "expected 12 log records")
		}
	})

	t.Run("exporter integration", func(t *testing.T) {
		recorder := &telemetrytest.RecordClient{}
		defer telemetry.MockClient(recorder)()

		// Create a test exporter
		testExp := &testExporter{}
		let := NewLogsExportTelemetry("http", "json")

		// Wrap it with telemetry
		te := &telemetryExporter{
			Exporter:  testExp,
			telemetry: let,
		}

		ctx := context.Background()

		// Create some test log records
		records := []sdklog.Record{
			{}, // Empty records for testing
			{},
			{},
		}

		// Export records
		err := te.Export(ctx, records)
		require.NoError(t, err)

		// Verify telemetry was recorded
		key := telemetrytest.MetricKey{
			Namespace: telemetry.NamespaceGeneral,
			Name:      "otel.log_records",
			Tags:      "encoding:json,protocol:http",
			Kind:      "count",
		}

		assert.Contains(t, recorder.Metrics, key, "expected otel.log_records metric")
		if handle, ok := recorder.Metrics[key]; ok {
			assert.Equal(t, float64(3), handle.Get(), "expected 3 log records to be counted")
		}
	})

	t.Run("nil telemetry doesn't panic", func(t *testing.T) {
		var let *LogsExportTelemetry // nil

		// Should not panic
		let.RecordLogRecords(5)
	})

	t.Run("zero count not recorded", func(t *testing.T) {
		recorder := &telemetrytest.RecordClient{}
		defer telemetry.MockClient(recorder)()

		let := NewLogsExportTelemetry("http", "json")

		// Record zero
		let.RecordLogRecords(0)

		// Verify no metrics were recorded
		key := telemetrytest.MetricKey{
			Namespace: telemetry.NamespaceGeneral,
			Name:      "otel.log_records",
			Tags:      "encoding:json,protocol:http",
			Kind:      "count",
		}

		// The key might exist but should have zero value, or not exist at all
		if handle, ok := recorder.Metrics[key]; ok {
			assert.Equal(t, float64(0), handle.Get(), "expected zero count for zero records")
		}
	})
}
