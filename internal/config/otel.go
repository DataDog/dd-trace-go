// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"maps"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/config/schema"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	defaultOTelLogExporterTimeout    = 30 * time.Second
	defaultOTelMetricExporterTimeout = 30 * time.Second
)

var (
	otelLogBinding = ConsumerBinding{
		ID: "otel.log", Consumer: "ddtrace/opentelemetry/log LoggerProvider",
		Keys: []string{
			"DD_AGENT_HOST",
			"DD_ENV",
			"DD_HOSTNAME",
			"DD_SERVICE",
			"DD_TAGS",
			"DD_TRACE_AGENT_URL",
			"DD_TRACE_REPORT_HOSTNAME",
			"DD_VERSION",
			"OTEL_BLRP_EXPORT_TIMEOUT",
			"OTEL_BLRP_MAX_EXPORT_BATCH_SIZE",
			"OTEL_BLRP_MAX_QUEUE_SIZE",
			"OTEL_BLRP_SCHEDULE_DELAY",
			"OTEL_EXPORTER_OTLP_ENDPOINT",
			"OTEL_EXPORTER_OTLP_HEADERS",
			"OTEL_EXPORTER_OTLP_INSECURE",
			"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
			"OTEL_EXPORTER_OTLP_LOGS_HEADERS",
			"OTEL_EXPORTER_OTLP_LOGS_INSECURE",
			"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL",
			"OTEL_EXPORTER_OTLP_LOGS_TIMEOUT",
			"OTEL_EXPORTER_OTLP_PROTOCOL",
			"OTEL_EXPORTER_OTLP_TIMEOUT",
			"OTEL_RESOURCE_ATTRIBUTES",
		},
		Sampling: SampleConstructor, EnvironmentOnly: true,
	}
	otelMetricBinding = ConsumerBinding{
		ID: "otel.metric", Consumer: "ddtrace/opentelemetry/metric MeterProvider",
		Keys: []string{
			"DD_AGENT_HOST",
			"DD_ENV",
			"DD_HOSTNAME",
			"DD_METRICS_OTEL_ENABLED",
			"DD_SERVICE",
			"DD_TAGS",
			"DD_TRACE_AGENT_URL",
			"DD_TRACE_REPORT_HOSTNAME",
			"DD_VERSION",
			"OTEL_EXPORTER_OTLP_ENDPOINT",
			"OTEL_EXPORTER_OTLP_HEADERS",
			"OTEL_EXPORTER_OTLP_INSECURE",
			"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
			"OTEL_EXPORTER_OTLP_METRICS_HEADERS",
			"OTEL_EXPORTER_OTLP_METRICS_INSECURE",
			"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL",
			"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE",
			"OTEL_EXPORTER_OTLP_METRICS_TIMEOUT",
			"OTEL_EXPORTER_OTLP_PROTOCOL",
			"OTEL_EXPORTER_OTLP_TIMEOUT",
			"OTEL_METRICS_EXPORTER",
			"OTEL_METRIC_EXPORT_INTERVAL",
			"OTEL_METRIC_EXPORT_TIMEOUT",
			"OTEL_RESOURCE_ATTRIBUTES",
			"OTEL_SERVICE_NAME",
		},
		Sampling: SampleConstructor, EnvironmentOnly: true,
	}
)

func init() {
	registerRaw(RawDefinition{Key: "OTEL_BLRP_EXPORT_TIMEOUT", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_BLRP_MAX_QUEUE_SIZE", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_BLRP_SCHEDULE_DELAY", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", Sources: SourceEnvironment, Telemetry: TelemetrySanitizeURL})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_LOGS_HEADERS", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_LOGS_INSECURE", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_METRICS_TIMEOUT", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_INSECURE", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_METRICS_INSECURE", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_PROTOCOL", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_EXPORTER_OTLP_TIMEOUT", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_METRIC_EXPORT_INTERVAL", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_METRIC_EXPORT_TIMEOUT", Sources: SourceEnvironment, Telemetry: TelemetryReport})

	registerBinding(ConsumerBinding{
		ID: "otel.log", Consumer: "ddtrace/opentelemetry/log LoggerProvider",
		Keys: []string{
			"DD_AGENT_HOST",
			"DD_ENV",
			"DD_HOSTNAME",
			"DD_SERVICE",
			"DD_TAGS",
			"DD_TRACE_AGENT_URL",
			"DD_TRACE_REPORT_HOSTNAME",
			"DD_VERSION",
			"OTEL_BLRP_EXPORT_TIMEOUT",
			"OTEL_BLRP_MAX_EXPORT_BATCH_SIZE",
			"OTEL_BLRP_MAX_QUEUE_SIZE",
			"OTEL_BLRP_SCHEDULE_DELAY",
			"OTEL_EXPORTER_OTLP_ENDPOINT",
			"OTEL_EXPORTER_OTLP_HEADERS",
			"OTEL_EXPORTER_OTLP_INSECURE",
			"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
			"OTEL_EXPORTER_OTLP_LOGS_HEADERS",
			"OTEL_EXPORTER_OTLP_LOGS_INSECURE",
			"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL",
			"OTEL_EXPORTER_OTLP_LOGS_TIMEOUT",
			"OTEL_EXPORTER_OTLP_PROTOCOL",
			"OTEL_EXPORTER_OTLP_TIMEOUT",
			"OTEL_RESOURCE_ATTRIBUTES",
		},
		Sampling: SampleConstructor, EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "otel.metric", Consumer: "ddtrace/opentelemetry/metric MeterProvider",
		Keys: []string{
			"DD_AGENT_HOST",
			"DD_ENV",
			"DD_HOSTNAME",
			"DD_METRICS_OTEL_ENABLED",
			"DD_SERVICE",
			"DD_TAGS",
			"DD_TRACE_AGENT_URL",
			"DD_TRACE_REPORT_HOSTNAME",
			"DD_VERSION",
			"OTEL_EXPORTER_OTLP_ENDPOINT",
			"OTEL_EXPORTER_OTLP_HEADERS",
			"OTEL_EXPORTER_OTLP_INSECURE",
			"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
			"OTEL_EXPORTER_OTLP_METRICS_HEADERS",
			"OTEL_EXPORTER_OTLP_METRICS_INSECURE",
			"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL",
			"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE",
			"OTEL_EXPORTER_OTLP_METRICS_TIMEOUT",
			"OTEL_EXPORTER_OTLP_PROTOCOL",
			"OTEL_EXPORTER_OTLP_TIMEOUT",
			"OTEL_METRICS_EXPORTER",
			"OTEL_METRIC_EXPORT_INTERVAL",
			"OTEL_METRIC_EXPORT_TIMEOUT",
			"OTEL_RESOURCE_ATTRIBUTES",
			"OTEL_SERVICE_NAME",
		},
		Sampling: SampleConstructor, EnvironmentOnly: true,
	})
}

// OTelRawSetting preserves one raw environment input and its presence. Value
// remains unparsed so explicit empty and whitespace-only values are visible.
type OTelRawSetting struct {
	Value   string
	Present bool
	Valid   bool
	Origin  Origin
}

// OTelExporterRawSnapshot keeps generic and signal-specific inputs separate.
type OTelExporterRawSnapshot struct {
	Endpoint OTelRawSetting
	Protocol OTelRawSetting
	Headers  OTelRawSetting
	Insecure OTelRawSetting
	Timeout  OTelRawSetting
}

// OTelLogSnapshot contains one constructor-scoped sample for OTel logs.
type OTelLogSnapshot struct {
	Generic OTelExporterRawSnapshot
	Signal  OTelExporterRawSnapshot

	AgentURL           string
	AgentHost          string
	Service            string
	Environment        string
	Version            string
	Tags               map[string]string
	ResourceAttributes map[string]string
	Hostname           string
	HasHostname        bool

	Protocol           string
	Headers            map[string]string
	ExporterTimeout    time.Duration
	MaxQueueSize       int
	ScheduleDelay      time.Duration
	BatchExportTimeout time.Duration
	MaxExportBatchSize int
}

// OTelMetricSnapshot contains one constructor-scoped sample for OTel metrics.
type OTelMetricSnapshot struct {
	Generic OTelExporterRawSnapshot
	Signal  OTelExporterRawSnapshot

	AgentURL           string
	AgentHost          string
	Service            string
	Environment        string
	Version            string
	OTelService        string
	Tags               map[string]string
	ResourceAttributes map[string]string
	Hostname           string
	HasHostname        bool

	StandaloneEnabled     bool
	MetricsExporter       string
	Protocol              string
	Headers               map[string]string
	ExporterTimeout       time.Duration
	ReaderInterval        time.Duration
	ReaderTimeout         time.Duration
	TemporalityPreference string
}

// ResolveOTelLogSnapshot samples one OTel log configuration without reporting.
func ResolveOTelLogSnapshot() OTelLogSnapshot {
	snapshot, _ := PrepareOTelLogSnapshot()
	return snapshot
}

// PrepareOTelLogSnapshot samples one OTel log configuration and returns an
// idempotent reporter so callers can publish runtime state before telemetry.
func PrepareOTelLogSnapshot() (OTelLogSnapshot, func()) {
	otelProvider := newEnvironmentProvider()
	ddProvider := newDirectEnvProvider()
	var events []ConfigEvent

	resolveString := func(key string) (string, OTelRawSetting) {
		p := ddProvider
		if strings.HasPrefix(key, "OTEL_") {
			p = otelProvider
		}
		resolved, local := resolveStringWithProvider(p, registeredDefinition(key), otelLogBinding)
		events = append(events, local...)
		return resolved.Winner.Value, rawSetting(resolved)
	}
	resolveMillis := func(key string, defaultValue int) (int, OTelRawSetting) {
		resolved, local := resolveBoundWithProvider(
			otelProvider,
			registeredDefinition(key),
			otelLogBinding,
			defaultValue,
			parseOTelMilliseconds,
		)
		events = append(events, winnerConfigEvents(local, key, resolved.Winner, true)...)
		return resolved.Winner.Value, rawSetting(resolved)
	}
	resolveInt := func(key string, defaultValue int) (int, OTelRawSetting) {
		resolved, local := resolveBoundWithProvider(
			otelProvider,
			registeredDefinition(key),
			otelLogBinding,
			defaultValue,
			func(raw string) (int, error) {
				return strconv.Atoi(strings.TrimSpace(raw))
			},
		)
		events = append(events, winnerConfigEvents(local, key, resolved.Winner, true)...)
		return resolved.Winner.Value, rawSetting(resolved)
	}

	agentURL, _ := resolveString("DD_TRACE_AGENT_URL")
	agentHost, _ := resolveString("DD_AGENT_HOST")
	service, _ := resolveString("DD_SERVICE")
	environment, _ := resolveString("DD_ENV")
	version, _ := resolveString("DD_VERSION")
	rawTags, _ := resolveString("DD_TAGS")
	rawResourceAttributes, _ := resolveString("OTEL_RESOURCE_ATTRIBUTES")
	reportHostname, _ := resolveString("DD_TRACE_REPORT_HOSTNAME")
	ddHostname, _ := resolveString("DD_HOSTNAME")

	_, genericEndpointRaw := resolveString("OTEL_EXPORTER_OTLP_ENDPOINT")
	genericProtocol, genericProtocolRaw := resolveString("OTEL_EXPORTER_OTLP_PROTOCOL")
	genericHeaders, genericHeadersRaw := resolveString("OTEL_EXPORTER_OTLP_HEADERS")
	_, genericInsecureRaw := resolveString("OTEL_EXPORTER_OTLP_INSECURE")
	_, genericTimeoutRaw := resolveMillis("OTEL_EXPORTER_OTLP_TIMEOUT", 10000)

	_, signalEndpointRaw := resolveString("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT")
	signalProtocol, signalProtocolRaw := resolveString("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL")
	signalHeaders, signalHeadersRaw := resolveString("OTEL_EXPORTER_OTLP_LOGS_HEADERS")
	_, signalInsecureRaw := resolveString("OTEL_EXPORTER_OTLP_LOGS_INSECURE")
	_, signalTimeoutRaw := resolveMillis("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", 10000)

	maxQueueSize, maxQueueRaw := resolveInt("OTEL_BLRP_MAX_QUEUE_SIZE", 2048)
	scheduleDelay, scheduleRaw := resolveMillis("OTEL_BLRP_SCHEDULE_DELAY", 1000)
	batchExportTimeout, batchTimeoutRaw := resolveMillis("OTEL_BLRP_EXPORT_TIMEOUT", 30000)
	maxExportBatchSize, maxBatchRaw := resolveInt("OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", 512)
	scheduleDelayMilliseconds := int64(scheduleDelay)
	batchExportTimeoutMilliseconds := int64(batchExportTimeout)

	if value, ok := parsePositiveIntExact(maxQueueRaw.Value); ok {
		maxQueueSize = value
	} else {
		maxQueueSize = 2048
	}
	if value, ok := parseMillisecondsExact(scheduleRaw.Value); ok {
		scheduleDelayMilliseconds = value
	} else {
		scheduleDelayMilliseconds = 1000
	}
	if value, ok := parseMillisecondsExact(batchTimeoutRaw.Value); ok {
		batchExportTimeoutMilliseconds = value
	} else {
		batchExportTimeoutMilliseconds = 30000
	}
	if value, ok := parsePositiveIntExact(maxBatchRaw.Value); ok {
		maxExportBatchSize = value
	} else {
		maxExportBatchSize = 512
	}

	tags := internal.ParseTagString(rawTags)
	resourceAttributes := parseOTelResourceAttributes(rawResourceAttributes)
	hostname, hasHostname := resolveOTelHostname(resourceAttributes, reportHostname, ddHostname)

	snapshot := OTelLogSnapshot{
		Generic: OTelExporterRawSnapshot{
			Endpoint: genericEndpointRaw,
			Protocol: genericProtocolRaw,
			Headers:  genericHeadersRaw,
			Insecure: genericInsecureRaw,
			Timeout:  genericTimeoutRaw,
		},
		Signal: OTelExporterRawSnapshot{
			Endpoint: signalEndpointRaw,
			Protocol: signalProtocolRaw,
			Headers:  signalHeadersRaw,
			Insecure: signalInsecureRaw,
			Timeout:  signalTimeoutRaw,
		},
		AgentURL:           agentURL,
		AgentHost:          agentHost,
		Service:            service,
		Environment:        environment,
		Version:            version,
		Tags:               maps.Clone(tags),
		ResourceAttributes: maps.Clone(resourceAttributes),
		Hostname:           hostname,
		HasHostname:        hasHostname,
		Protocol:           effectiveProtocol(signalProtocol, genericProtocol, "http/json"),
		Headers:            parseOTelLogHeaders(firstNonEmpty(signalHeaders, genericHeaders)),
		ExporterTimeout: effectiveOTelLogTimeout(
			signalTimeoutRaw.Value,
			genericTimeoutRaw.Value,
		),
		MaxQueueSize:       maxQueueSize,
		ScheduleDelay:      time.Duration(scheduleDelayMilliseconds) * time.Millisecond,
		BatchExportTimeout: time.Duration(batchExportTimeoutMilliseconds) * time.Millisecond,
		MaxExportBatchSize: maxExportBatchSize,
	}
	return snapshot, otelSnapshotReporter(otelLogTelemetryEvents(events))
}

// ResolveOTelMetricSnapshot samples one OTel metric configuration without reporting.
func ResolveOTelMetricSnapshot() OTelMetricSnapshot {
	snapshot, _ := PrepareOTelMetricSnapshot()
	return snapshot
}

// PrepareOTelMetricSnapshot samples one OTel metric configuration and returns
// an idempotent reporter for publication after successful construction.
func PrepareOTelMetricSnapshot() (OTelMetricSnapshot, func()) {
	otelProvider := newEnvironmentProvider()
	ddProvider := newDirectEnvProvider()
	var events []ConfigEvent

	resolveString := func(key string) (string, OTelRawSetting) {
		p := ddProvider
		if strings.HasPrefix(key, "OTEL_") {
			p = otelProvider
		}
		resolved, local := resolveStringWithProvider(p, registeredDefinition(key), otelMetricBinding)
		events = append(events, local...)
		return resolved.Winner.Value, rawSetting(resolved)
	}
	resolveMillis := func(key string, defaultValue int) (int, OTelRawSetting) {
		resolved, local := resolveBoundWithProvider(
			otelProvider,
			registeredDefinition(key),
			otelMetricBinding,
			defaultValue,
			parseOTelMilliseconds,
		)
		events = append(events, winnerConfigEvents(local, key, resolved.Winner, true)...)
		return resolved.Winner.Value, rawSetting(resolved)
	}

	agentURL, _ := resolveString("DD_TRACE_AGENT_URL")
	agentHost, _ := resolveString("DD_AGENT_HOST")
	service, _ := resolveString("DD_SERVICE")
	environment, _ := resolveString("DD_ENV")
	version, _ := resolveString("DD_VERSION")
	otelService, _ := resolveString("OTEL_SERVICE_NAME")
	rawTags, _ := resolveString("DD_TAGS")
	rawResourceAttributes, _ := resolveString("OTEL_RESOURCE_ATTRIBUTES")
	reportHostname, _ := resolveString("DD_TRACE_REPORT_HOSTNAME")
	ddHostname, _ := resolveString("DD_HOSTNAME")
	enabled, _ := resolveString("DD_METRICS_OTEL_ENABLED")
	metricsExporter, _ := resolveString("OTEL_METRICS_EXPORTER")

	_, genericEndpointRaw := resolveString("OTEL_EXPORTER_OTLP_ENDPOINT")
	genericProtocol, genericProtocolRaw := resolveString("OTEL_EXPORTER_OTLP_PROTOCOL")
	_, genericHeadersRaw := resolveString("OTEL_EXPORTER_OTLP_HEADERS")
	_, genericInsecureRaw := resolveString("OTEL_EXPORTER_OTLP_INSECURE")
	_, genericTimeoutRaw := resolveMillis("OTEL_EXPORTER_OTLP_TIMEOUT", 10000)

	_, signalEndpointRaw := resolveString("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")
	signalProtocol, signalProtocolRaw := resolveString("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL")
	_, signalHeadersRaw := resolveString("OTEL_EXPORTER_OTLP_METRICS_HEADERS")
	_, signalInsecureRaw := resolveString("OTEL_EXPORTER_OTLP_METRICS_INSECURE")
	_, signalTimeoutRaw := resolveMillis("OTEL_EXPORTER_OTLP_METRICS_TIMEOUT", 10000)
	temporalityPreference, _ := resolveString("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE")
	readerInterval, _ := resolveMillis("OTEL_METRIC_EXPORT_INTERVAL", 10000)
	readerTimeout, _ := resolveMillis("OTEL_METRIC_EXPORT_TIMEOUT", 7500)

	tags := internal.ParseTagString(rawTags)
	resourceAttributes := parseOTelResourceAttributes(rawResourceAttributes)
	hostname, hasHostname := resolveOTelHostname(resourceAttributes, reportHostname, ddHostname)

	snapshot := OTelMetricSnapshot{
		Generic: OTelExporterRawSnapshot{
			Endpoint: genericEndpointRaw,
			Protocol: genericProtocolRaw,
			Headers:  genericHeadersRaw,
			Insecure: genericInsecureRaw,
			Timeout:  genericTimeoutRaw,
		},
		Signal: OTelExporterRawSnapshot{
			Endpoint: signalEndpointRaw,
			Protocol: signalProtocolRaw,
			Headers:  signalHeadersRaw,
			Insecure: signalInsecureRaw,
			Timeout:  signalTimeoutRaw,
		},
		AgentURL:              agentURL,
		AgentHost:             agentHost,
		Service:               service,
		Environment:           environment,
		Version:               version,
		OTelService:           otelService,
		Tags:                  maps.Clone(tags),
		ResourceAttributes:    maps.Clone(resourceAttributes),
		Hostname:              hostname,
		HasHostname:           hasHostname,
		StandaloneEnabled:     standaloneOTelMetricsEnabled(enabled, metricsExporter),
		MetricsExporter:       metricsExporter,
		Protocol:              effectiveProtocol(signalProtocol, genericProtocol, "http/protobuf"),
		Headers:               parseOTelSDKHeaders(signalHeadersRaw.Value, genericHeadersRaw.Value),
		ExporterTimeout:       defaultOTelMetricExporterTimeout,
		ReaderInterval:        time.Duration(readerInterval) * time.Millisecond,
		ReaderTimeout:         time.Duration(readerTimeout) * time.Millisecond,
		TemporalityPreference: strings.ToUpper(strings.TrimSpace(temporalityPreference)),
	}
	return snapshot, otelSnapshotReporter(otelMetricTelemetryEvents(events, genericTimeoutRaw))
}

func rawSetting[T any](resolved schema.Resolved[T]) OTelRawSetting {
	for _, attempt := range resolved.Attempts {
		if attempt.Present {
			return OTelRawSetting{
				Value:   attempt.Raw,
				Present: true,
				Valid:   attempt.Valid,
				Origin:  attempt.Origin,
			}
		}
	}
	return OTelRawSetting{}
}

func parseOTelMilliseconds(raw string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(raw))
}

func parseMillisecondsExact(raw string) (int64, bool) {
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func parsePositiveIntExact(raw string) (int, bool) {
	value, err := strconv.Atoi(raw)
	return value, err == nil && value > 0
}

func effectiveOTelLogTimeout(signal, generic string) time.Duration {
	if milliseconds, ok := parseMillisecondsExact(signal); ok {
		return time.Duration(milliseconds) * time.Millisecond
	}
	if milliseconds, ok := parseMillisecondsExact(generic); ok {
		return time.Duration(milliseconds) * time.Millisecond
	}
	return defaultOTelLogExporterTimeout
}

func effectiveProtocol(signal, generic, fallback string) string {
	return strings.ToLower(strings.TrimSpace(firstNonEmpty(signal, generic, fallback)))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func parseOTelLogHeaders(raw string) map[string]string {
	headers := make(map[string]string)
	for entry := range strings.SplitSeq(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "" {
			headers[key] = strings.TrimSpace(value)
		}
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func parseOTelSDKHeaders(signal, generic string) map[string]string {
	raw := strings.TrimSpace(generic)
	if specific := strings.TrimSpace(signal); specific != "" {
		raw = specific
	}
	headers := make(map[string]string)
	for _, header := range strings.Split(raw, ",") {
		name, value, found := strings.Cut(header, "=")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		if !validOTelHeaderKey(name) {
			continue
		}
		decoded, err := url.PathUnescape(value)
		if err != nil {
			continue
		}
		headers[name] = strings.TrimSpace(decoded)
	}
	return headers
}

func validOTelHeaderKey(key string) bool {
	if key == "" {
		return false
	}
	for _, char := range key {
		if char > unicode.MaxASCII ||
			!(unicode.IsLetter(char) ||
				unicode.IsDigit(char) ||
				strings.ContainsRune("!#$%&'*+-.^_`|~", char)) {
			return false
		}
	}
	return true
}

func parseOTelResourceAttributes(raw string) map[string]string {
	attributes := make(map[string]string)
	internal.ForEachStringTag(raw, internal.OtelTagsDelimeter, func(key, value string) {
		attributes[key] = value
	})
	return attributes
}

func resolveOTelHostname(resourceAttributes map[string]string, reportHostname, ddHostname string) (string, bool) {
	if hostname := resourceAttributes["host.name"]; hostname != "" {
		return hostname, true
	}
	if reportHostname != "true" {
		return "", false
	}
	if ddHostname != "" {
		return ddHostname, true
	}
	hostname, err := os.Hostname()
	if err != nil {
		log.Warn("unable to look up hostname: %s", err.Error())
		return "", false
	}
	return hostname, hostname != ""
}

func standaloneOTelMetricsEnabled(enabled, exporter string) bool {
	if strings.EqualFold(strings.TrimSpace(exporter), "none") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(enabled)) {
	case "true", "1":
		return true
	default:
		return false
	}
}

func otelLogTelemetryEvents(events []ConfigEvent) []ConfigEvent {
	always := map[string]struct{}{
		"OTEL_BLRP_EXPORT_TIMEOUT":        {},
		"OTEL_BLRP_MAX_EXPORT_BATCH_SIZE": {},
		"OTEL_BLRP_MAX_QUEUE_SIZE":        {},
		"OTEL_BLRP_SCHEDULE_DELAY":        {},
		"OTEL_EXPORTER_OTLP_LOGS_TIMEOUT": {},
		"OTEL_EXPORTER_OTLP_TIMEOUT":      {},
	}
	conditional := map[string]struct{}{
		"OTEL_EXPORTER_OTLP_ENDPOINT":      {},
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": {},
		"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": {},
		"OTEL_EXPORTER_OTLP_PROTOCOL":      {},
	}
	return filterOTelTelemetryEvents(events, always, conditional)
}

func otelMetricTelemetryEvents(events []ConfigEvent, genericTimeout OTelRawSetting) []ConfigEvent {
	always := map[string]struct{}{
		"OTEL_EXPORTER_OTLP_METRICS_TIMEOUT": {},
		"OTEL_METRIC_EXPORT_INTERVAL":        {},
		"OTEL_METRIC_EXPORT_TIMEOUT":         {},
	}
	if genericTimeout.Present && genericTimeout.Valid && genericTimeout.Value != "" {
		always["OTEL_EXPORTER_OTLP_TIMEOUT"] = struct{}{}
	}
	conditional := map[string]struct{}{
		"OTEL_EXPORTER_OTLP_ENDPOINT":         {},
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": {},
		"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL": {},
		"OTEL_EXPORTER_OTLP_PROTOCOL":         {},
	}
	return filterOTelTelemetryEvents(events, always, conditional)
}

func filterOTelTelemetryEvents(
	events []ConfigEvent,
	always map[string]struct{},
	conditional map[string]struct{},
) []ConfigEvent {
	filtered := make([]ConfigEvent, 0, len(always)+len(conditional))
	for _, event := range events {
		if event.Kind != EventConfiguration {
			filtered = append(filtered, event)
			continue
		}
		if _, ok := always[event.Name]; ok {
			filtered = append(filtered, event)
			continue
		}
		if _, ok := conditional[event.Name]; !ok || !event.Present || !event.Valid {
			continue
		}
		raw, ok := event.Value.(string)
		if !ok || raw == "" {
			continue
		}
		if strings.HasSuffix(event.Name, "_PROTOCOL") {
			event.Value = strings.ToLower(strings.TrimSpace(raw))
		}
		filtered = append(filtered, event)
	}
	return filtered
}

func otelSnapshotReporter(events []ConfigEvent) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			reportInstrumentationEvents(events)
		})
	}
}
