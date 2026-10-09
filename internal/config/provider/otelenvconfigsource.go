// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package provider

import (
	"fmt"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

const (
	ddPrefix   = "config_datadog:"
	otelPrefix = "config_opentelemetry:"
)

type otelEnvConfigSource struct{}

func (o *otelEnvConfigSource) get(key string) string {
	ddKey := normalizeKey(key)
	entry := otelConfigs[ddKey]
	if entry == nil {
		return ""
	}
	otVal := env.Get(entry.ot)
	if otVal == "" {
		return ""
	}
	if ddVal := env.Get(ddKey); ddVal != "" {
		log.Warn("Both %q and %q are set, using %s=%s", entry.ot, ddKey, entry.ot, ddVal)
		telemetryTags := []string{ddPrefix + strings.ToLower(ddKey), otelPrefix + strings.ToLower(entry.ot)}
		telemetry.Count(telemetry.NamespaceTracers, "otel.env.hiding", telemetryTags).Submit(1)
	}
	if entry.remapper == nil {
		return otVal
	}
	val, err := entry.remapper(otVal)
	if err != nil {
		reportInvalidOTelEnv(ddKey, entry.ot, err)
		return ""
	}
	return val
}

func reportInvalidOTelEnv(ddKey, otKey string, err error) {
	log.Warn("%s", err.Error())
	telemetryTags := []string{ddPrefix + strings.ToLower(ddKey), otelPrefix + strings.ToLower(otKey)}
	telemetry.Count(telemetry.NamespaceTracers, "otel.env.invalid", telemetryTags).Submit(1)
}

func (o *otelEnvConfigSource) origin() telemetry.Origin {
	return telemetry.OriginEnvVar
}

type otelDDEnv struct {
	ot       string
	remapper func(string) (string, error)
}

var otelConfigs = map[string]*otelDDEnv{
	"DD_SERVICE": {
		ot:       "OTEL_SERVICE_NAME",
		remapper: mapService,
	},
	"DD_RUNTIME_METRICS_ENABLED": {
		ot:       "OTEL_METRICS_EXPORTER",
		remapper: mapMetrics,
	},
	"DD_METRICS_OTEL_ENABLED": {
		ot:       "OTEL_METRICS_EXPORTER",
		remapper: mapOtelMetrics,
	},
	"DD_TRACE_DEBUG": {
		ot:       "OTEL_LOG_LEVEL",
		remapper: mapLogLevel,
	},
	"DD_TRACE_ENABLED": {
		ot:       "OTEL_TRACES_EXPORTER",
		remapper: mapEnabled,
	},
	"DD_TRACE_SAMPLE_RATE": {
		ot:       "OTEL_TRACES_SAMPLER",
		remapper: mapSampleRate,
	},
	"DD_TRACE_PROPAGATION_STYLE": {
		ot:       "OTEL_PROPAGATORS",
		remapper: mapPropagationStyle,
	},
	"DD_TAGS": {
		ot: "OTEL_RESOURCE_ATTRIBUTES",
	},
}

var ddTagsMapping = map[string]string{
	"service.name":           "service",
	"deployment.environment": "env",
	"service.version":        "version",
}

var unsupportedSamplerMapping = map[string]string{
	"always_on":    "parentbased_always_on",
	"always_off":   "parentbased_always_off",
	"traceidratio": "parentbased_traceidratio",
}

var propagationMapping = map[string]string{
	"tracecontext": "tracecontext",
	"b3":           "b3 single header",
	"b3multi":      "b3multi",
	"datadog":      "datadog",
	"none":         "none",
}

// mapService maps OTEL_SERVICE_NAME to DD_SERVICE
func mapService(ot string) (string, error) {
	return ot, nil
}

// mapMetrics maps OTEL_METRICS_EXPORTER to DD_RUNTIME_METRICS_ENABLED.
func mapMetrics(ot string) (string, error) {
	ot = strings.TrimSpace(strings.ToLower(ot))
	if ot == "none" {
		return "false", nil
	}
	if ot == "otlp" || strings.Contains(ot, "otlp") {
		return "", nil
	}
	return "", fmt.Errorf("the following configuration is not supported: OTEL_METRICS_EXPORTER=%v", ot)
}

// mapOtelMetrics maps OTEL_METRICS_EXPORTER to DD_METRICS_OTEL_ENABLED.
func mapOtelMetrics(ot string) (string, error) {
	if strings.TrimSpace(strings.ToLower(ot)) == "none" {
		return "false", nil
	}
	return "", nil
}

// mapLogLevel maps OTEL_LOG_LEVEL to DD_TRACE_DEBUG
func mapLogLevel(ot string) (string, error) {
	if strings.TrimSpace(strings.ToLower(ot)) == "debug" {
		return "true", nil
	}
	return "", fmt.Errorf("the following configuration is not supported: OTEL_LOG_LEVEL=%v", ot)
}

// mapEnabled maps OTEL_TRACES_EXPORTER to DD_TRACE_ENABLED
func mapEnabled(ot string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(ot)) {
	case "none":
		return "false", nil
	case "otlp":
		return "true", nil // Handled separately by otlpExportMode
	default:
		return "", fmt.Errorf("the following configuration is not supported: OTEL_TRACES_EXPORTER=%v", ot)
	}
}

// otelTraceIDRatio returns the value of OTEL_TRACES_SAMPLER_ARG if set, otherwise "1.0"
func otelTraceIDRatio() string {
	if v := env.Get("OTEL_TRACES_SAMPLER_ARG"); v != "" {
		return v
	}
	return "1.0"
}

// mapSampleRate maps OTEL_TRACES_SAMPLER to DD_TRACE_SAMPLE_RATE
func mapSampleRate(ot string) (string, error) {
	ot = strings.TrimSpace(strings.ToLower(ot))
	if v, ok := unsupportedSamplerMapping[ot]; ok {
		log.Warn("The following configuration is not supported: OTEL_TRACES_SAMPLER=%s. %s will be used", ot, v)
		ot = v
	}

	var samplerMapping = map[string]string{
		"parentbased_always_on":    "1.0",
		"parentbased_always_off":   "0.0",
		"parentbased_traceidratio": otelTraceIDRatio(),
	}
	if v, ok := samplerMapping[ot]; ok {
		return v, nil
	}
	return "", fmt.Errorf("unknown sampling configuration %v", ot)
}

// mapPropagationStyle maps OTEL_PROPAGATORS to DD_TRACE_PROPAGATION_STYLE
func mapPropagationStyle(ot string) (string, error) {
	ot = strings.TrimSpace(strings.ToLower(ot))
	supportedStyles := make([]string, 0)
	for otStyle := range strings.SplitSeq(ot, ",") {
		otStyle = strings.TrimSpace(otStyle)
		if _, ok := propagationMapping[otStyle]; ok {
			supportedStyles = append(supportedStyles, propagationMapping[otStyle])
		} else {
			log.Warn("Invalid configuration: %q is not supported. This propagation style will be ignored.", otStyle)
		}
	}
	return strings.Join(supportedStyles, ","), nil
}

// getTags returns raw, OTEL_RESOURCE_ATTRIBUTES in DD_TAGS format for
// telemetry; tags, the decoded attributes with OTel reserved names mapped to
// DD tag names; and ok, whether OTEL_RESOURCE_ATTRIBUTES is set.
func (o *otelEnvConfigSource) getTags() (raw string, tags map[string]string, ok bool) {
	v := o.get("DD_TAGS")
	if v == "" {
		return "", nil, false
	}
	var reserved, others [][2]string
	err := internal.ForEachOTelResourceAttribute(v, func(key, val string) {
		// decoded commas would split the tag in DD_TAGS and DogStatsD formats
		key, val = strings.ReplaceAll(key, ",", "_"), strings.ReplaceAll(val, ",", "_")
		if ddKey, ok := ddTagsMapping[key]; ok {
			reserved = append(reserved, [2]string{ddKey, val})
		} else {
			others = append(others, [2]string{key, val})
		}
	})
	if err != nil {
		reportInvalidOTelEnv("DD_TAGS", otelConfigs["DD_TAGS"].ot, err)
	}
	// reserved names go first so native DD tag names take precedence
	pairs := append(reserved, others...)
	ddTags := make([]string, 0, len(pairs))
	tags = make(map[string]string, len(pairs))
	for _, pair := range pairs {
		ddTags = append(ddTags, pair[0]+internal.DDTagsDelimiter+pair[1])
		tags[pair[0]] = pair[1]
	}
	return strings.Join(ddTags, ","), tags, true
}
