// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package provider

import (
	"fmt"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/config/schema"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// ResolveTracerOTelCompatibility preserves the tracer's legacy six-setting
// OTel compatibility contract while keeping all raw reads in provider sources.
// Only the debug binding may read managed and local stable configuration.
func ResolveTracerOTelCompatibility(
	p *Provider,
	def schema.RawDefinition,
	binding schema.ConsumerBinding,
) (schema.Resolved[string], []ConfigEvent) {
	result := schema.Resolved[string]{
		Winner: schema.Winner[string]{
			Origin:      telemetry.OriginDefault,
			DefaultUsed: true,
		},
		Attempts: make([]schema.SourceAttempt, 0, len(p.sources)),
	}
	entry := otelConfigs[normalizeKey(def.Key)]
	if entry == nil {
		return result, nil
	}

	if !binding.EnvironmentOnly {
		if resolveTracerOTelSource(&result, p.sources[schema.SourceOrdinalManagedStable], def.Key) {
			events := []ConfigEvent{tracerOTelConfigurationEvent(
				binding, def, result.Attempts[len(result.Attempts)-1],
				result.Winner.Value, schema.SourceOrdinalManagedStable,
			)}
			return result, events
		}
	}

	environment := p.sources[schema.SourceOrdinalEnvironment]
	ddRaw, ddPresent := environment.lookup(def.Key)
	ddAttempt := tracerOTelAttempt(environment, ddRaw, ddPresent)
	result.Attempts = append(result.Attempts, ddAttempt)

	otelSource := p.sources[schema.SourceOrdinalOTelEnvironment]
	otelRaw, otelPresent := lookupTracerOTelRaw(otelSource, def.Key)
	otelAttempt := tracerOTelAttempt(otelSource, otelRaw, otelPresent)
	result.Attempts = append(result.Attempts, otelAttempt)

	if ddRaw != "" {
		result.Winner = tracerOTelWinner(ddRaw, ddAttempt)
		events := []ConfigEvent{tracerOTelConfigurationEvent(
			binding, def, ddAttempt, ddRaw, schema.SourceOrdinalEnvironment,
		)}
		if otelRaw != "" {
			log.Warn("Both %q and %q are set, using %s=%s", entry.ot, def.Key, def.Key, ddRaw)
			diagnostic := tracerOTelDiagnostic(
				EventOTelEnvHiding, binding, def, entry.ot, nil,
			)
			result.Events = []ConfigEvent{diagnostic}
			events = append(events, diagnostic)
		}
		return result, events
	}

	events := make([]ConfigEvent, 0, 2)
	if otelRaw != "" {
		value, err := tracerOTelRemap(def.Key, entry, otelRaw)
		otelAttempt.Valid = err == nil
		otelAttempt.Err = err
		result.Attempts[len(result.Attempts)-1] = otelAttempt
		if err != nil {
			log.Warn("%s", err.Error())
			diagnostic := tracerOTelDiagnostic(
				EventOTelEnvInvalid, binding, def, entry.ot, err,
			)
			result.Events = []ConfigEvent{diagnostic}
			events = append(events, diagnostic)
		} else if value != "" {
			result.Winner = tracerOTelWinner(value, otelAttempt)
			otelDef := def
			otelDef.Key = entry.ot
			events = append(events, tracerOTelConfigurationEvent(
				binding, otelDef, otelAttempt, value, schema.SourceOrdinalOTelEnvironment,
			))
			return result, events
		}
	}

	if !binding.EnvironmentOnly {
		before := len(result.Attempts)
		if resolveTracerOTelSource(&result, p.sources[schema.SourceOrdinalLocalStable], def.Key) {
			events = append(events, tracerOTelConfigurationEvent(
				binding, def, result.Attempts[before],
				result.Winner.Value, schema.SourceOrdinalLocalStable,
			))
		}
	}
	return result, events
}

func resolveTracerOTelSource(
	result *schema.Resolved[string],
	source LookupSource,
	key string,
) bool {
	raw, present := source.lookup(key)
	attempt := tracerOTelAttempt(source, raw, present)
	result.Attempts = append(result.Attempts, attempt)
	if raw == "" {
		return false
	}
	result.Winner = tracerOTelWinner(raw, attempt)
	return true
}

func tracerOTelAttempt(source LookupSource, raw string, present bool) schema.SourceAttempt {
	return schema.SourceAttempt{
		Raw:      raw,
		Present:  present,
		Valid:    raw != "",
		Origin:   source.origin(),
		ConfigID: sourceConfigID(source),
	}
}

func tracerOTelWinner(value string, attempt schema.SourceAttempt) schema.Winner[string] {
	return schema.Winner[string]{
		Value:    value,
		Origin:   attempt.Origin,
		ConfigID: attempt.ConfigID,
	}
}

func lookupTracerOTelRaw(source LookupSource, key string) (string, bool) {
	rawSource, ok := source.(interface {
		lookupRaw(string) (string, bool)
	})
	if !ok {
		return "", false
	}
	return rawSource.lookupRaw(key)
}

func tracerOTelRemap(ddKey string, entry *otelDDEnv, raw string) (string, error) {
	if normalizeKey(ddKey) == "DD_TRACE_ENABLED" {
		if strings.TrimSpace(strings.ToLower(raw)) == "none" {
			return "false", nil
		}
		return "", fmt.Errorf("the following configuration is not supported: OTEL_METRICS_EXPORTER=%v", raw)
	}
	return entry.remapper(raw)
}

func tracerOTelConfigurationEvent(
	binding schema.ConsumerBinding,
	def schema.RawDefinition,
	attempt schema.SourceAttempt,
	value string,
	sourceOrdinal uint16,
) ConfigEvent {
	return configEvent(binding, def, attempt, value, true, sourceOrdinal)
}

func tracerOTelDiagnostic(
	kind EventKind,
	binding schema.ConsumerBinding,
	def schema.RawDefinition,
	otelKey string,
	err error,
) ConfigEvent {
	return scrubEvent(def.Telemetry, ConfigEvent{
		Kind:                kind,
		BindingID:           binding.ID,
		Name:                def.Key,
		Err:                 err,
		SourceOrdinal:       schema.SourceOrdinalOTelEnvironment,
		Policy:              def.Telemetry,
		Cadence:             cadenceFor(binding),
		CompatibilityReport: true,
		OTelName:            otelKey,
	})
}
