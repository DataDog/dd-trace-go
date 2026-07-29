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

func (o *otelEnvConfigSource) lookup(key string) (string, bool) {
	raw, present, _, _, _ := o.lookupWithEvents(key)
	return raw, present
}

func (o *otelEnvConfigSource) get(key string) string {
	raw, _ := o.lookup(key)
	return raw
}

func (o *otelEnvConfigSource) lookupRaw(key string) (string, bool) {
	entry := otelConfigs[normalizeKey(key)]
	if entry == nil {
		return "", false
	}
	return env.Lookup(entry.ot)
}

func (o *otelEnvConfigSource) lookupSamplerArgument() string {
	if value := env.Get("OTEL_TRACES_SAMPLER_ARG"); value != "" {
		return value
	}
	return "1.0"
}

func (o *otelEnvConfigSource) lookupWithEvents(key string) (string, bool, bool, error, []ConfigEvent) {
	ddKey := normalizeKey(key)
	entry := otelConfigs[ddKey]
	if entry == nil {
		return "", false, false, nil, nil
	}
	otVal, present := o.lookupRaw(ddKey)
	if !present {
		return "", false, false, nil, nil
	}
	var events []ConfigEvent
	if ddVal, ddPresent := env.Lookup(ddKey); ddPresent {
		if ddVal != "" {
			if otVal != "" {
				log.Warn("Both %q and %q are set, using %s=%s", entry.ot, ddKey, ddKey, ddVal)
			}
			events = append(events, ConfigEvent{
				Kind: EventOTelEnvHiding, Name: ddKey, OTelName: entry.ot,
				CompatibilityReport: otVal != "",
			})
			return otVal, true, false, nil, events
		}
		events = append(events, ConfigEvent{
			Kind: EventOTelEnvHiding, Name: ddKey, OTelName: entry.ot,
			CompatibilityReport: false,
		})
	}
	if otVal == "" && !entry.emptyResultApplicable {
		return otVal, true, false, nil, events
	}
	val, err := entry.remapper(otVal)
	if err != nil {
		log.Warn("%s", err.Error())
		events = append(events, ConfigEvent{
			Kind: EventOTelEnvInvalid, Name: ddKey, OTelName: entry.ot, Err: err,
			CompatibilityReport: otVal != "",
		})
		return otVal, true, true, err, events
	}
	applicable := val != "" || entry.emptyResultApplicable
	return val, true, applicable, nil, events
}

func (o *otelEnvConfigSource) origin() telemetry.Origin {
	return telemetry.OriginEnvVar
}

func (o *otelEnvConfigSource) environmentSource() bool {
	return true
}

func reportOTelMetric(metric, ddKey, otelKey string) {
	telemetryTags := []string{ddPrefix + strings.ToLower(ddKey), otelPrefix + strings.ToLower(otelKey)}
	telemetry.Count(telemetry.NamespaceTracers, metric, telemetryTags).Submit(1)
}

// CanonicalOTelMapping returns the registry-owned names when otelKey is the
// registered OTel compatibility source for ddKey.
func CanonicalOTelMapping(ddKey, otelKey string) (string, string, bool) {
	if !strings.HasPrefix(otelKey, "OTEL_") {
		return "", "", false
	}
	normalized := normalizeKey(ddKey)
	entry := otelConfigs[normalized]
	if entry == nil || entry.ot != otelKey {
		return "", "", false
	}
	for canonicalDD, registered := range otelConfigs {
		if registered == entry {
			return canonicalDD, entry.ot, true
		}
	}
	return "", "", false
}

// IsKnownOTelMapping reports whether otelKey is the registered OTel
// compatibility source for ddKey.
func IsKnownOTelMapping(ddKey, otelKey string) bool {
	_, _, ok := CanonicalOTelMapping(ddKey, otelKey)
	return ok
}

type otelDDEnv struct {
	ot                    string
	remapper              func(string) (string, error)
	emptyResultApplicable bool
}

var otelConfigs = map[string]*otelDDEnv{
	"DD_SERVICE": {
		ot:                    "OTEL_SERVICE_NAME",
		remapper:              mapService,
		emptyResultApplicable: true,
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
		ot:       "OTEL_RESOURCE_ATTRIBUTES",
		remapper: mapDDTags,
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
		"parentbased_traceidratio": new(otelEnvConfigSource).lookupSamplerArgument(),
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

// mapDDTags maps OTEL_RESOURCE_ATTRIBUTES to DD_TAGS
func mapDDTags(ot string) (string, error) {
	ddTags := make([]string, 0)
	internal.ForEachStringTag(ot, internal.OtelTagsDelimeter, func(key, val string) {
		// replace otel delimiter with dd delimiter and normalize tag names
		if ddkey, ok := ddTagsMapping[key]; ok {
			ddTags = append([]string{ddkey + internal.DDTagsDelimiter + val}, ddTags...)
		} else {
			ddTags = append(ddTags, key+internal.DDTagsDelimiter+val)
		}
	})

	if len(ddTags) > 10 {
		log.Warn("The following resource attributes have been dropped: %v. Only the first 10 resource attributes will be applied: %s", ddTags[10:], ddTags[:10]) //nolint:gocritic // Slice logging for debugging
		ddTags = ddTags[:10]
	}

	return strings.Join(ddTags, ","), nil
}
