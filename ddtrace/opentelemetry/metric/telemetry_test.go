// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

// TestTelemetryDefaultConfigurations verifies that default configuration values
// are reported to telemetry when no environment variables are set.
func TestTelemetryDefaultConfigurations(t *testing.T) {
	recorder := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(recorder)()

	t.Setenv("DD_METRICS_OTEL_ENABLED", "true")

	mp, err := NewMeterProvider()
	if err != nil {
		t.Fatalf("unexpected error creating MeterProvider: %v", err)
	}
	defer Shutdown(t.Context(), mp)

	// Check default values are reported
	expectedDefaults := map[string]int{
		"OTEL_EXPORTER_OTLP_METRICS_TIMEOUT": 10000, // 10 seconds
		"OTEL_METRIC_EXPORT_INTERVAL":        10000, // 10 seconds
		"OTEL_METRIC_EXPORT_TIMEOUT":         7500,  // 7.5 seconds
	}

	for configName, expectedValue := range expectedDefaults {
		found := false
		for _, cfg := range recorder.Configuration {
			if cfg.Name == configName {
				found = true
				assert.Equal(t, expectedValue, cfg.Value, "config %s has wrong value", configName)
				assert.Equal(t, telemetry.OriginDefault, cfg.Origin, "config %s should have default origin", configName)
				break
			}
		}
		assert.True(t, found, "expected config %s not found in telemetry", configName)
	}
}

// TestTelemetryExporterConfigurations verifies that OTEL_EXPORTER_OTLP_* configurations
// are reported to telemetry when set via environment variables.
func TestTelemetryExporterConfigurations(t *testing.T) {
	recorder := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(recorder)()

	// Set environment variables
	t.Setenv("DD_METRICS_OTEL_ENABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "30000")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "api-key=key,other-config-value=value")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "5000")
	t.Setenv("OTEL_METRIC_EXPORT_TIMEOUT", "5000")

	mp, err := NewMeterProvider()
	if err != nil {
		t.Fatalf("unexpected error creating MeterProvider: %v", err)
	}
	defer Shutdown(t.Context(), mp)

	// Check configurations are reported with env_var origin
	expectedConfigs := map[string]any{
		"OTEL_EXPORTER_OTLP_TIMEOUT":  30000,
		"OTEL_EXPORTER_OTLP_HEADERS":  "api-key=key,other-config-value=value",
		"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
		"OTEL_METRIC_EXPORT_INTERVAL": 5000,
		"OTEL_METRIC_EXPORT_TIMEOUT":  5000,
	}

	for configName, expectedValue := range expectedConfigs {
		found := false
		for _, cfg := range recorder.Configuration {
			if cfg.Name == configName {
				found = true
				assert.Equal(t, expectedValue, cfg.Value, "config %s has wrong value", configName)
				assert.Equal(t, telemetry.OriginEnvVar, cfg.Origin, "config %s should have env_var origin", configName)
				break
			}
		}
		assert.True(t, found, "expected config %s not found in telemetry", configName)
	}
}

// TestTelemetryExporterMetricsConfigurations verifies that OTEL_EXPORTER_OTLP_METRICS_*
// configurations are reported to telemetry when set via environment variables.
func TestTelemetryExporterMetricsConfigurations(t *testing.T) {
	recorder := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(recorder)()

	// Set environment variables
	t.Setenv("DD_METRICS_OTEL_ENABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TIMEOUT", "30000")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", "api-key=key,other-config-value=value")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "http://localhost:4325")

	mp, err := NewMeterProvider()
	if err != nil {
		t.Fatalf("unexpected error creating MeterProvider: %v", err)
	}
	defer Shutdown(t.Context(), mp)

	// Check configurations are reported with env_var origin
	expectedConfigs := map[string]any{
		"OTEL_EXPORTER_OTLP_METRICS_TIMEOUT":  30000,
		"OTEL_EXPORTER_OTLP_METRICS_HEADERS":  "api-key=key,other-config-value=value",
		"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL": "http/protobuf",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://localhost:4325",
	}

	for configName, expectedValue := range expectedConfigs {
		found := false
		for _, cfg := range recorder.Configuration {
			if cfg.Name == configName {
				found = true
				assert.Equal(t, expectedValue, cfg.Value, "config %s has wrong value", configName)
				assert.Equal(t, telemetry.OriginEnvVar, cfg.Origin, "config %s should have env_var origin", configName)
				break
			}
		}
		assert.True(t, found, "expected config %s not found in telemetry", configName)
	}
}

// TestMetricsExportTelemetry verifies that the MetricsExportTelemetry correctly
// tracks export attempts and successes.
func TestMetricsExportTelemetry(t *testing.T) {
	recorder := &telemetrytest.RecordClient{}
	defer telemetry.MockClient(recorder)()

	// Create telemetry tracker for HTTP/protobuf
	met := NewMetricsExportTelemetry("http", "protobuf")

	// Record some attempts and successes
	met.RecordAttempt()
	met.RecordSuccess()
	met.RecordAttempt()
	met.RecordSuccess()

	// Check that metrics were recorded
	attemptsKey := telemetrytest.MetricKey{
		Namespace: telemetry.NamespaceGeneral,
		Name:      "otel.metrics_export_attempts",
		Tags:      "encoding:protobuf,protocol:http",
		Kind:      "count",
	}
	successesKey := telemetrytest.MetricKey{
		Namespace: telemetry.NamespaceGeneral,
		Name:      "otel.metrics_export_successes",
		Tags:      "encoding:protobuf,protocol:http",
		Kind:      "count",
	}

	assert.Contains(t, recorder.Metrics, attemptsKey, "expected otel.metrics_export_attempts metric")
	assert.Contains(t, recorder.Metrics, successesKey, "expected otel.metrics_export_successes metric")

	if handle, ok := recorder.Metrics[attemptsKey]; ok {
		assert.Equal(t, float64(2), handle.Get(), "expected 2 attempts")
	}
	if handle, ok := recorder.Metrics[successesKey]; ok {
		assert.Equal(t, float64(2), handle.Get(), "expected 2 successes")
	}
}

// TestMetricsExportTelemetryGRPC verifies that gRPC protocol is correctly tagged.
func TestMetricsExportTelemetryGRPC(t *testing.T) {
	recorder := &telemetrytest.RecordClient{}
	defer telemetry.MockClient(recorder)()

	// Create telemetry tracker for gRPC/protobuf
	met := NewMetricsExportTelemetry("grpc", "protobuf")

	met.RecordAttempt()
	met.RecordSuccess()

	// Check that metrics were recorded with correct tags
	attemptsKey := telemetrytest.MetricKey{
		Namespace: telemetry.NamespaceGeneral,
		Name:      "otel.metrics_export_attempts",
		Tags:      "encoding:protobuf,protocol:grpc",
		Kind:      "count",
	}
	successesKey := telemetrytest.MetricKey{
		Namespace: telemetry.NamespaceGeneral,
		Name:      "otel.metrics_export_successes",
		Tags:      "encoding:protobuf,protocol:grpc",
		Kind:      "count",
	}

	assert.Contains(t, recorder.Metrics, attemptsKey, "expected otel.metrics_export_attempts metric with grpc tag")
	assert.Contains(t, recorder.Metrics, successesKey, "expected otel.metrics_export_successes metric with grpc tag")
}
