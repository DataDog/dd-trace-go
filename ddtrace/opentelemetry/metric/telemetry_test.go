// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
	"github.com/stretchr/testify/assert"
)

// TestTelemetryRegistration verifies that OTel metrics configuration is registered
// with telemetry when a MeterProvider is created.
// Telemetry names follow snake_case convention (e.g., "otel_metrics_enabled" for DD_METRICS_OTEL_ENABLED)
func TestTelemetryRegistration(t *testing.T) {
	tests := []struct {
		name        string
		envVars     map[string]string
		wantConfigs map[string]any
	}{
		{
			name: "metrics enabled with defaults",
			envVars: map[string]string{
				"DD_METRICS_OTEL_ENABLED": "true",
			},
			wantConfigs: map[string]any{
				"otel_metrics_enabled":                              true,
				"otel_metrics_exporter":                             "otlp",
				"otel_exporter_otlp_metrics_protocol":               "http/protobuf",
				"otel_exporter_otlp_metrics_endpoint":               "localhost:4318",
				"otel_exporter_otlp_metrics_temporality_preference": "delta",
				"otel_metric_export_interval":                       "1m0s",
				"otel_metric_export_timeout":                        "30s",
			},
		},
		{
			name: "metrics enabled with grpc protocol",
			envVars: map[string]string{
				"DD_METRICS_OTEL_ENABLED":     "true",
				"OTEL_EXPORTER_OTLP_PROTOCOL": "grpc",
			},
			wantConfigs: map[string]any{
				"otel_metrics_enabled":                              true,
				"otel_exporter_otlp_metrics_protocol":               "grpc",
				"otel_exporter_otlp_metrics_endpoint":               "localhost:4317",
				"otel_exporter_otlp_metrics_temporality_preference": "delta",
			},
		},
		{
			name: "metrics enabled with custom endpoint via DD_AGENT_HOST",
			envVars: map[string]string{
				"DD_METRICS_OTEL_ENABLED": "true",
				"DD_AGENT_HOST":           "custom-agent",
			},
			wantConfigs: map[string]any{
				"otel_metrics_enabled":                true,
				"otel_exporter_otlp_metrics_endpoint": "custom-agent:4318",
			},
		},
		{
			name: "metrics enabled with OTEL endpoint override",
			envVars: map[string]string{
				"DD_METRICS_OTEL_ENABLED":     "true",
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://otel-collector:4318",
			},
			wantConfigs: map[string]any{
				"otel_metrics_enabled":                true,
				"otel_exporter_otlp_metrics_endpoint": "http://otel-collector:4318",
			},
		},
		{
			name: "metrics enabled with cumulative temporality",
			envVars: map[string]string{
				"DD_METRICS_OTEL_ENABLED":                           "true",
				"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE": "cumulative",
			},
			wantConfigs: map[string]any{
				"otel_metrics_enabled":                              true,
				"otel_exporter_otlp_metrics_temporality_preference": "cumulative",
			},
		},
		{
			name:    "metrics disabled by default",
			envVars: map[string]string{},
			wantConfigs: map[string]any{
				"otel_metrics_enabled": false,
			},
		},
		{
			name: "metrics disabled via OTEL_METRICS_EXPORTER=none",
			envVars: map[string]string{
				"DD_METRICS_OTEL_ENABLED": "true",
				"OTEL_METRICS_EXPORTER":   "none",
			},
			wantConfigs: map[string]any{
				"otel_metrics_enabled":  false,
				"otel_metrics_exporter": "none",
			},
		},
		{
			name: "metrics enabled with custom headers",
			envVars: map[string]string{
				"DD_METRICS_OTEL_ENABLED":            "true",
				"OTEL_EXPORTER_OTLP_METRICS_HEADERS": "api-key=secret",
			},
			wantConfigs: map[string]any{
				"otel_metrics_enabled":               true,
				"otel_exporter_otlp_metrics_headers": "<redacted>",
			},
		},
		{
			name: "metrics enabled with custom OTLP timeout",
			envVars: map[string]string{
				"DD_METRICS_OTEL_ENABLED":            "true",
				"OTEL_EXPORTER_OTLP_METRICS_TIMEOUT": "10000",
			},
			wantConfigs: map[string]any{
				"otel_metrics_enabled":               true,
				"otel_exporter_otlp_metrics_timeout": "10000",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up mock telemetry client
			recorder := new(telemetrytest.RecordClient)
			defer telemetry.MockClient(recorder)()

			// Set environment variables
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			// Create MeterProvider (this triggers telemetry registration)
			mp, err := NewMeterProvider()
			if err != nil {
				t.Fatalf("unexpected error creating MeterProvider: %v", err)
			}
			defer Shutdown(t.Context(), mp)

			// Verify telemetry configurations
			for key, wantValue := range tt.wantConfigs {
				found := false
				for _, cfg := range recorder.Configuration {
					if cfg.Name == key {
						found = true
						assert.Equal(t, wantValue, cfg.Value, "config %s has wrong value", key)
						break
					}
				}
				assert.True(t, found, "expected config %s not found in telemetry", key)
			}
		})
	}
}

// TestTelemetryOrigins verifies that configuration origins are correctly reported.
func TestTelemetryOrigins(t *testing.T) {
	tests := []struct {
		name       string
		envVars    map[string]string
		configName string
		wantOrigin telemetry.Origin
	}{
		{
			name: "enabled via env var",
			envVars: map[string]string{
				"DD_METRICS_OTEL_ENABLED": "true",
			},
			configName: "otel_metrics_enabled",
			wantOrigin: telemetry.OriginEnvVar,
		},
		{
			name:       "disabled by default",
			envVars:    map[string]string{},
			configName: "otel_metrics_enabled",
			wantOrigin: telemetry.OriginDefault,
		},
		{
			name: "protocol via env var",
			envVars: map[string]string{
				"DD_METRICS_OTEL_ENABLED":     "true",
				"OTEL_EXPORTER_OTLP_PROTOCOL": "grpc",
			},
			configName: "otel_exporter_otlp_metrics_protocol",
			wantOrigin: telemetry.OriginEnvVar,
		},
		{
			name: "protocol default",
			envVars: map[string]string{
				"DD_METRICS_OTEL_ENABLED": "true",
			},
			configName: "otel_exporter_otlp_metrics_protocol",
			wantOrigin: telemetry.OriginDefault,
		},
		{
			name: "endpoint via DD_AGENT_HOST",
			envVars: map[string]string{
				"DD_METRICS_OTEL_ENABLED": "true",
				"DD_AGENT_HOST":           "agent-host",
			},
			configName: "otel_exporter_otlp_metrics_endpoint",
			wantOrigin: telemetry.OriginEnvVar,
		},
		{
			name: "endpoint default",
			envVars: map[string]string{
				"DD_METRICS_OTEL_ENABLED": "true",
			},
			configName: "otel_exporter_otlp_metrics_endpoint",
			wantOrigin: telemetry.OriginDefault,
		},
		{
			name: "temporality via env var",
			envVars: map[string]string{
				"DD_METRICS_OTEL_ENABLED":                           "true",
				"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE": "cumulative",
			},
			configName: "otel_exporter_otlp_metrics_temporality_preference",
			wantOrigin: telemetry.OriginEnvVar,
		},
		{
			name: "temporality default (delta for Datadog)",
			envVars: map[string]string{
				"DD_METRICS_OTEL_ENABLED": "true",
			},
			configName: "otel_exporter_otlp_metrics_temporality_preference",
			wantOrigin: telemetry.OriginDefault,
		},
		{
			name: "exporter via env var",
			envVars: map[string]string{
				"DD_METRICS_OTEL_ENABLED": "true",
				"OTEL_METRICS_EXPORTER":   "prometheus",
			},
			configName: "otel_metrics_exporter",
			wantOrigin: telemetry.OriginEnvVar,
		},
		{
			name: "exporter default",
			envVars: map[string]string{
				"DD_METRICS_OTEL_ENABLED": "true",
			},
			configName: "otel_metrics_exporter",
			wantOrigin: telemetry.OriginDefault,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := new(telemetrytest.RecordClient)
			defer telemetry.MockClient(recorder)()

			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			mp, err := NewMeterProvider()
			if err != nil {
				t.Fatalf("unexpected error creating MeterProvider: %v", err)
			}
			defer Shutdown(t.Context(), mp)

			found := false
			for _, cfg := range recorder.Configuration {
				if cfg.Name == tt.configName {
					found = true
					assert.Equal(t, tt.wantOrigin, cfg.Origin, "config %s has wrong origin", tt.configName)
					break
				}
			}
			assert.True(t, found, "expected config %s not found in telemetry", tt.configName)
		})
	}
}
