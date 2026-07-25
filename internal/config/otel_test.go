// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveOTelLogSnapshotKeepsRawFallbackInputsDistinct(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://generic.example/base")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", " GRPC ")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/json")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "generic=one")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "specific=two")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_INSECURE", "false")

	snapshot := ResolveOTelLogSnapshot()

	assert.Equal(t, "https://generic.example/base", snapshot.Generic.Endpoint.Value)
	assert.True(t, snapshot.Generic.Endpoint.Present)
	assert.Empty(t, snapshot.Signal.Endpoint.Value)
	assert.True(t, snapshot.Signal.Endpoint.Present, "an explicit empty signal value must remain observable")
	assert.Equal(t, " GRPC ", snapshot.Generic.Protocol.Value)
	assert.Equal(t, "http/json", snapshot.Signal.Protocol.Value)
	assert.Equal(t, "generic=one", snapshot.Generic.Headers.Value)
	assert.Equal(t, "specific=two", snapshot.Signal.Headers.Value)
	assert.Equal(t, "true", snapshot.Generic.Insecure.Value)
	assert.Equal(t, "false", snapshot.Signal.Insecure.Value)
	assert.Equal(t, "http/json", snapshot.Protocol)
	require.Equal(t, map[string]string{"specific": "two"}, snapshot.Headers)
}

func TestResolveOTelLogSnapshotParsesEffectiveValues(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", "not-a-number")
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "1250")
	t.Setenv("OTEL_BLRP_MAX_QUEUE_SIZE", "0")
	t.Setenv("OTEL_BLRP_SCHEDULE_DELAY", "-5")
	t.Setenv("OTEL_BLRP_EXPORT_TIMEOUT", "bad")
	t.Setenv("OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", "17")
	t.Setenv("DD_TAGS", "team:logs,service:tag-service")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=otel-service,otel.key=otel-value")

	snapshot := ResolveOTelLogSnapshot()

	assert.False(t, snapshot.Signal.Timeout.Valid)
	assert.Equal(t, 1250*time.Millisecond, snapshot.ExporterTimeout,
		"an invalid signal timeout must fall back to the valid generic timeout")
	assert.Equal(t, 2048, snapshot.MaxQueueSize, "non-positive queue sizes use the runtime default")
	assert.Equal(t, -5*time.Millisecond, snapshot.ScheduleDelay,
		"schedule delay preserves the existing signed integer parser")
	assert.Equal(t, 30*time.Second, snapshot.BatchExportTimeout)
	assert.Equal(t, 17, snapshot.MaxExportBatchSize)
	assert.Equal(t, map[string]string{"service": "tag-service", "team": "logs"}, snapshot.Tags)
	assert.Equal(t, map[string]string{
		"otel.key":     "otel-value",
		"service.name": "otel-service",
	}, snapshot.ResourceAttributes)
}

func TestResolveOTelLogSnapshotPreservesLargeMillisecondTimeouts(t *testing.T) {
	const milliseconds = int64(2147483648)
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", "2147483648")
	t.Setenv("OTEL_BLRP_SCHEDULE_DELAY", "2147483648")
	t.Setenv("OTEL_BLRP_EXPORT_TIMEOUT", "2147483648")

	snapshot := ResolveOTelLogSnapshot()
	want := time.Duration(milliseconds) * time.Millisecond

	assert.Equal(t, want, snapshot.ExporterTimeout)
	assert.Equal(t, want, snapshot.ScheduleDelay)
	assert.Equal(t, want, snapshot.BatchExportTimeout)
}

func TestParseMillisecondsExactDoesNotNarrowToPlatformInt(t *testing.T) {
	milliseconds, ok := parseMillisecondsExact("2147483648")

	require.True(t, ok)
	assert.IsType(t, int64(0), milliseconds,
		"the parser result must remain 64-bit before conversion to time.Duration")
	assert.EqualValues(t, int64(2147483648), milliseconds)
}

func TestResolveOTelLogSnapshotPreservesWhitespaceSelection(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "   ")
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "1200")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", "   ")

	snapshot := ResolveOTelLogSnapshot()

	assert.Equal(t, "", snapshot.Protocol,
		"a non-empty whitespace signal protocol is selected before normalization")
	assert.Equal(t, 1200*time.Millisecond, snapshot.ExporterTimeout,
		"an invalid whitespace signal timeout falls back to generic")
	assert.True(t, snapshot.Signal.Protocol.Present)
	assert.True(t, snapshot.Signal.Timeout.Present)
}

func TestResolveOTelLogSnapshotIsFreshAndDefensive(t *testing.T) {
	t.Setenv("DD_SERVICE", "first")
	t.Setenv("DD_TAGS", "team:first")
	first := ResolveOTelLogSnapshot()

	first.Tags["team"] = "mutated"
	t.Setenv("DD_SERVICE", "second")
	t.Setenv("DD_TAGS", "team:second")
	second := ResolveOTelLogSnapshot()

	assert.Equal(t, "first", first.Service)
	assert.Equal(t, "second", second.Service)
	assert.Equal(t, map[string]string{"team": "second"}, second.Tags)
}

func TestResolveOTelMetricSnapshotKeepsRawFallbackInputsDistinct(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://generic.example/base")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", " GRPC ")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "generic=one")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", "specific=two")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_INSECURE", "false")

	snapshot := ResolveOTelMetricSnapshot()

	assert.Equal(t, "https://generic.example/base", snapshot.Generic.Endpoint.Value)
	assert.True(t, snapshot.Generic.Endpoint.Present)
	assert.Empty(t, snapshot.Signal.Endpoint.Value)
	assert.True(t, snapshot.Signal.Endpoint.Present, "an explicit empty signal value must remain observable")
	assert.Equal(t, "http/protobuf", snapshot.Generic.Protocol.Value)
	assert.Equal(t, " GRPC ", snapshot.Signal.Protocol.Value)
	assert.Equal(t, "generic=one", snapshot.Generic.Headers.Value)
	assert.Equal(t, "specific=two", snapshot.Signal.Headers.Value)
	assert.Equal(t, "true", snapshot.Generic.Insecure.Value)
	assert.Equal(t, "false", snapshot.Signal.Insecure.Value)
	assert.Equal(t, "grpc", snapshot.Protocol)
}

func TestResolveOTelMetricSnapshotPreservesRuntimeAndReaderParsing(t *testing.T) {
	t.Setenv("DD_METRICS_OTEL_ENABLED", "1")
	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "1")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TIMEOUT", "2")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "-5")
	t.Setenv("OTEL_METRIC_EXPORT_TIMEOUT", "invalid")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", " cumulative ")

	snapshot := ResolveOTelMetricSnapshot()

	assert.True(t, snapshot.StandaloneEnabled)
	assert.Equal(t, 30*time.Second, snapshot.ExporterTimeout,
		"the package's explicit exporter timeout overrides both SDK timeout variables")
	assert.Equal(t, -5*time.Millisecond, snapshot.ReaderInterval)
	assert.Equal(t, 7500*time.Millisecond, snapshot.ReaderTimeout)
	assert.Equal(t, "CUMULATIVE", snapshot.TemporalityPreference)
}

func TestResolveOTelMetricSnapshotStandaloneEnablement(t *testing.T) {
	tests := []struct {
		name     string
		enabled  string
		exporter string
		want     bool
	}{
		{name: "true", enabled: "true", want: true},
		{name: "one", enabled: "1", want: true},
		{name: "trimmed and case insensitive", enabled: " TrUe ", want: true},
		{name: "false", enabled: "false", want: false},
		{name: "none exporter wins", enabled: "true", exporter: " NoNe ", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DD_METRICS_OTEL_ENABLED", tt.enabled)
			t.Setenv("OTEL_METRICS_EXPORTER", tt.exporter)
			assert.Equal(t, tt.want, ResolveOTelMetricSnapshot().StandaloneEnabled)
		})
	}
}

func TestResolveOTelMetricSnapshotIsFreshAndDefensive(t *testing.T) {
	t.Setenv("DD_SERVICE", "first")
	t.Setenv("DD_TAGS", "team:first")
	first := ResolveOTelMetricSnapshot()

	first.Tags["team"] = "mutated"
	t.Setenv("DD_SERVICE", "second")
	t.Setenv("DD_TAGS", "team:second")
	second := ResolveOTelMetricSnapshot()

	assert.Equal(t, "first", first.Service)
	assert.Equal(t, "second", second.Service)
	assert.Equal(t, map[string]string{"team": "second"}, second.Tags)
}

func TestOTelSnapshotTelemetryOmitsHeadersAndSanitizesEndpoints(t *testing.T) {
	recorder := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(recorder)()

	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "authorization=SENTINEL_GENERIC")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "authorization=SENTINEL_LOGS")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", "authorization=SENTINEL_METRICS")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://generic-user:generic-secret@generic.example/base")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "https://logs-user:logs-secret@logs.example/path")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "https://metrics-user:metrics-secret@metrics.example/path")

	_, reportLogs := PrepareOTelLogSnapshot()
	_, reportMetrics := PrepareOTelMetricSnapshot()
	reportLogs()
	reportLogs()
	reportMetrics()
	reportMetrics()

	for _, cfg := range recorder.Configuration {
		assert.NotContains(t, cfg.Name, "HEADERS")
		text := fmt.Sprint(cfg.Value)
		assert.NotContains(t, text, "SENTINEL_")
		assert.NotContains(t, text, "secret")
	}
	assertConfigurationValue(t, recorder.Configuration, "OTEL_EXPORTER_OTLP_ENDPOINT", "https://generic.example/base")
	assertConfigurationValue(t, recorder.Configuration, "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "https://logs.example/path")
	assertConfigurationValue(t, recorder.Configuration, "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "https://metrics.example/path")
}

func TestOTelLogTelemetryPreservesLegacyConfigurationSet(t *testing.T) {
	for _, key := range otelLogBinding.Keys {
		t.Setenv(key, "")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_INSECURE", "false")
	recorder := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(recorder)()

	_, report := PrepareOTelLogSnapshot()
	report()

	assertConfigurationSet(t, recorder.Configuration, map[string]any{
		"OTEL_BLRP_EXPORT_TIMEOUT":        30000,
		"OTEL_BLRP_MAX_EXPORT_BATCH_SIZE": 512,
		"OTEL_BLRP_MAX_QUEUE_SIZE":        2048,
		"OTEL_BLRP_SCHEDULE_DELAY":        1000,
		"OTEL_EXPORTER_OTLP_LOGS_TIMEOUT": 10000,
		"OTEL_EXPORTER_OTLP_TIMEOUT":      10000,
	})
	for _, configuration := range recorder.Configuration {
		assert.Equal(t, telemetry.OriginDefault, configuration.Origin, configuration.Name)
	}
}

func TestOTelMetricTelemetryPreservesLegacyConfigurationSet(t *testing.T) {
	for _, key := range otelMetricBinding.Keys {
		t.Setenv(key, "")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "invalid")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", " GRPC ")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://user:secret@generic.example/base")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", " HTTP/PROTOBUF ")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "https://user:secret@metrics.example/path")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_INSECURE", "false")
	recorder := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(recorder)()

	_, report := PrepareOTelMetricSnapshot()
	report()

	assertConfigurationSet(t, recorder.Configuration, map[string]any{
		"OTEL_EXPORTER_OTLP_ENDPOINT":         "https://generic.example/base",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "https://metrics.example/path",
		"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL": "http/protobuf",
		"OTEL_EXPORTER_OTLP_METRICS_TIMEOUT":  10000,
		"OTEL_EXPORTER_OTLP_PROTOCOL":         "grpc",
		"OTEL_METRIC_EXPORT_INTERVAL":         10000,
		"OTEL_METRIC_EXPORT_TIMEOUT":          7500,
	})
	for _, configuration := range recorder.Configuration {
		if configuration.Name == "OTEL_EXPORTER_OTLP_METRICS_TIMEOUT" ||
			configuration.Name == "OTEL_METRIC_EXPORT_INTERVAL" ||
			configuration.Name == "OTEL_METRIC_EXPORT_TIMEOUT" {
			assert.Equal(t, telemetry.OriginDefault, configuration.Origin, configuration.Name)
		} else {
			assert.Equal(t, telemetry.OriginEnvVar, configuration.Origin, configuration.Name)
		}
	}
}

func TestOTelHeaderDefinitionsAreOmitted(t *testing.T) {
	raw, _ := RegisteredDefinitions()
	policies := make(map[string]TelemetryPolicy)
	for _, definition := range raw {
		if strings.Contains(definition.Key, "OTLP") && strings.HasSuffix(definition.Key, "_HEADERS") {
			policies[definition.Key] = definition.Telemetry
		}
	}

	assert.Equal(t, TelemetryOmit, policies["OTEL_EXPORTER_OTLP_HEADERS"])
	assert.Equal(t, TelemetryOmit, policies["OTEL_EXPORTER_OTLP_LOGS_HEADERS"])
	assert.Equal(t, TelemetryOmit, policies["OTEL_EXPORTER_OTLP_METRICS_HEADERS"])
}

func assertConfigurationSet(t *testing.T, configurations []telemetry.Configuration, want map[string]any) {
	t.Helper()
	got := make(map[string]any, len(configurations))
	for _, configuration := range configurations {
		if _, duplicate := got[configuration.Name]; duplicate {
			t.Fatalf("duplicate configuration telemetry for %s", configuration.Name)
		}
		got[configuration.Name] = configuration.Value
	}
	assert.Equal(t, want, got)
}

func assertConfigurationValue(t *testing.T, configurations []telemetry.Configuration, name string, want any) {
	t.Helper()
	for _, configuration := range configurations {
		if configuration.Name == name {
			assert.Equal(t, want, configuration.Value)
			return
		}
	}
	t.Errorf("configuration %s was not reported", name)
}
