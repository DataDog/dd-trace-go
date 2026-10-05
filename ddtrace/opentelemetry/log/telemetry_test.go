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
			Namespace: telemetry.NamespaceTracers,
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
			Namespace: telemetry.NamespaceTracers,
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
			Namespace: telemetry.NamespaceTracers,
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
			Namespace: telemetry.NamespaceTracers,
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
			Namespace: telemetry.NamespaceTracers,
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
