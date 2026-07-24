// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package provider resolves configuration values from multiple sources in priority order
// and reports telemetry for each value found.
package provider

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	configtelemetry "github.com/DataDog/dd-trace-go/v2/internal/config/configtelemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/config/schema"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// Provider resolves configuration values from an ordered list of sources.
// Sources are listed in descending priority order: the first source wins.
type Provider struct {
	sources          []LookupSource
	deferTelemetry   bool
	pendingTelemetry []ConfigEvent
}

// New returns a Provider configured with the following source list, in descending order of priority.
func New() *Provider {
	return &Provider{
		sources: []configSource{
			newDeclarativeConfigSource(managedFilePath, telemetry.OriginManagedStableConfig),
			new(envConfigSource),
			new(otelEnvConfigSource),
			newDeclarativeConfigSource(localFilePath, telemetry.OriginLocalStableConfig),
		},
	}
}

// NewDeferred returns a Provider that buffers compatibility telemetry until
// FlushTelemetry is called. Configuration generations use it so candidates
// that lose publication or fail construction never emit telemetry.
func NewDeferred() *Provider {
	p := New()
	p.deferTelemetry = true
	return p
}

// FlushTelemetry reports buffered compatibility events exactly once.
func (p *Provider) FlushTelemetry() {
	events := p.pendingTelemetry
	p.pendingTelemetry = nil
	p.deferTelemetry = false
	reportCompatibilityEvents(events)
}

// Resolve resolves def without synchronously reporting configuration telemetry.
// Attempts are returned from lowest to highest priority, including absent,
// non-applicable, and invalid sources. Provider diagnostics are retained in
// Resolved.Events.
func Resolve[T any](p *Provider, def schema.RawDefinition, defValue T, parse schema.Parser[T]) schema.Resolved[T] {
	resolved, _ := resolve(p, def, nil, defValue, parse)
	return resolved
}

// ResolveWithBinding resolves def and returns local events decorated with the
// explicit consumer binding. Callers decide when and how to report the events.
func ResolveWithBinding[T any](
	p *Provider,
	def schema.RawDefinition,
	binding schema.ConsumerBinding,
	defValue T,
	parse schema.Parser[T],
) (schema.Resolved[T], []ConfigEvent) {
	return resolve(p, def, &binding, defValue, parse)
}

func resolve[T any](
	p *Provider,
	def schema.RawDefinition,
	binding *schema.ConsumerBinding,
	defValue T,
	parse schema.Parser[T],
) (schema.Resolved[T], []ConfigEvent) {
	result := schema.Resolved[T]{
		Winner: schema.Winner[T]{
			Value:       snapshotValue(defValue),
			Origin:      telemetry.OriginDefault,
			DefaultUsed: true,
		},
		Attempts: make([]schema.SourceAttempt, 0, len(p.sources)),
	}
	providerEvents := make([]ConfigEvent, 0, len(p.sources))
	events := make([]ConfigEvent, 0, len(p.sources)+1)

	for i := len(p.sources) - 1; i >= 0; i-- {
		source := p.sources[i]
		if def.Sources == schema.SourceEnvironment && !isEnvironmentSource(source) {
			continue
		}

		raw, present, applicable, sourceErr, sourceEvents := lookup(source, def.Key)
		providerEvents = append(providerEvents, cloneEvents(sourceEvents)...)
		sourceOrdinal := uint16(i)
		events = append(events, decorateProviderEvents(sourceEvents, binding, def, sourceOrdinal)...)
		attempt := schema.SourceAttempt{
			Raw:      raw,
			Present:  present,
			Origin:   source.origin(),
			ConfigID: sourceConfigID(source),
		}
		reportValue := present && applicable && sourceErr == nil
		if present && applicable {
			if sourceErr != nil {
				attempt.Err = sourceErr
			} else {
				value, err := parse(raw)
				attempt.Valid = err == nil
				attempt.Err = err
				if err == nil {
					result.Winner = schema.Winner[T]{
						Value:    snapshotValue(value),
						Origin:   attempt.Origin,
						ConfigID: attempt.ConfigID,
					}
				}
			}
		}
		result.Attempts = append(result.Attempts, attempt)
		if binding != nil {
			events = append(events, configEvent(*binding, def, attempt, raw, reportValue, sourceOrdinal))
		}
	}

	if binding != nil {
		events = append(events, ConfigEvent{
			Kind:          EventConfiguration,
			BindingID:     binding.ID,
			Name:          def.Key,
			Value:         snapshotValue(defValue),
			Present:       true,
			Valid:         true,
			Origin:        telemetry.OriginDefault,
			SourceOrdinal: uint16(len(p.sources)),
			Policy:        def.Telemetry,
			Cadence:       cadenceFor(*binding),
			ReportValue:   true,
		})
	}
	result.Events = cloneEvents(providerEvents)
	return result, cloneEvents(events)
}

func lookup(source LookupSource, key string) (raw string, present bool, applicable bool, err error, events []ConfigEvent) {
	if source, ok := source.(eventLookupSource); ok {
		return source.lookupWithEvents(key)
	}
	raw, present = source.lookup(key)
	return raw, present, present, nil, nil
}

func isEnvironmentSource(source LookupSource) bool {
	environment, ok := source.(environmentConfigSource)
	return ok && environment.environmentSource()
}

func sourceConfigID(source LookupSource) string {
	if source, ok := source.(idAwareConfigSource); ok {
		return source.getID()
	}
	return telemetry.EmptyID
}

func decorateProviderEvents(events []ConfigEvent, binding *schema.ConsumerBinding, def schema.RawDefinition, sourceOrdinal uint16) []ConfigEvent {
	decorated := cloneEvents(events)
	if binding == nil {
		return decorated
	}
	for i := range decorated {
		decorated[i].BindingID = binding.ID
		decorated[i].SourceOrdinal = sourceOrdinal
		decorated[i].Policy = def.Telemetry
		decorated[i].Cadence = cadenceFor(*binding)
	}
	return decorated
}

// snapshotValue copies the mutable value shapes currently produced by provider
// getters and binding parsers. It deliberately avoids reflection-based copying
// of arbitrary caller types.
func snapshotValue[T any](value T) T {
	original := value
	switch value := any(value).(type) {
	case map[string]string:
		if value == nil {
			return any((map[string]string)(nil)).(T)
		}
		copy := make(map[string]string, len(value))
		for key, item := range value {
			copy[key] = item
		}
		return any(copy).(T)
	case []string:
		if value == nil {
			return any(([]string)(nil)).(T)
		}
		cloned := make([]string, len(value))
		copy(cloned, value)
		return any(cloned).(T)
	case []byte:
		if value == nil {
			return any(([]byte)(nil)).(T)
		}
		cloned := make([]byte, len(value))
		copy(cloned, value)
		return any(cloned).(T)
	default:
		return original
	}
}

func cloneEvents(events []ConfigEvent) []ConfigEvent {
	if events == nil {
		return nil
	}
	cloned := make([]ConfigEvent, len(events))
	for i, event := range events {
		cloned[i] = event
		switch value := event.Value.(type) {
		case map[string]string:
			cloned[i].Value = snapshotValue(value)
		case []string:
			cloned[i].Value = snapshotValue(value)
		case []byte:
			cloned[i].Value = snapshotValue(value)
		}
	}
	return cloned
}

func configEvent(
	binding schema.ConsumerBinding,
	def schema.RawDefinition,
	attempt schema.SourceAttempt,
	value any,
	reportValue bool,
	sourceOrdinal uint16,
) ConfigEvent {
	return ConfigEvent{
		Kind:          EventConfiguration,
		BindingID:     binding.ID,
		Name:          def.Key,
		Value:         value,
		Present:       attempt.Present,
		Valid:         attempt.Valid,
		Err:           attempt.Err,
		Origin:        attempt.Origin,
		ConfigID:      attempt.ConfigID,
		SourceOrdinal: sourceOrdinal,
		Policy:        def.Telemetry,
		Cadence:       cadenceFor(binding),
		ReportValue:   reportValue,
	}
}

func compatibilityBinding(key string) schema.ConsumerBinding {
	return schema.ConsumerBinding{
		ID:       "provider.compatibility." + normalizeKey(key),
		Consumer: "provider",
		Keys:     []string{key},
		Sampling: schema.SampleConstructor,
	}
}

func compatibilityDefinition(key string) schema.RawDefinition {
	return schema.RawDefinition{
		Key:       key,
		Sources:   schema.SourceStable,
		Telemetry: schema.TelemetryReport,
	}
}

func resolveCompatibility[T any](p *Provider, key string, def T, parse schema.Parser[T]) schema.Resolved[T] {
	result, events := ResolveWithBinding(p, compatibilityDefinition(key), compatibilityBinding(key), def, parse)
	if p.deferTelemetry {
		p.pendingTelemetry = append(p.pendingTelemetry, cloneEvents(events)...)
		return result
	}
	reportCompatibilityEvents(events)
	return result
}

func reportCompatibilityEvents(events []ConfigEvent) {
	for _, event := range events {
		switch event.Kind {
		case EventConfiguration:
			if !event.ReportValue {
				continue
			}
			if event.Origin == telemetry.OriginDefault {
				configtelemetry.ReportDefault(event.Name, event.Value)
				continue
			}
			raw, _ := event.Value.(string)
			if raw == "" {
				continue
			}
			configtelemetry.ReportWithID(event.Name, raw, event.Origin, event.ConfigID)
		case EventOTelEnvHiding:
			if event.CompatibilityReport {
				reportOTelMetric("otel.env.hiding", event.Name, event.OTelName)
			}
		case EventOTelEnvInvalid:
			if event.CompatibilityReport {
				reportOTelMetric("otel.env.invalid", event.Name, event.OTelName)
			}
		}
	}
}

var errInvalidValue = errors.New("invalid configuration value")

func validatorParser[T any](parse schema.Parser[T], validate func(T) bool) schema.Parser[T] {
	return func(raw string) (T, error) {
		value, err := parse(raw)
		if err != nil {
			return value, err
		}
		if validate != nil && !validate(value) {
			return value, errInvalidValue
		}
		return value, nil
	}
}

func compatibilityStringParser(validate func(string) bool) schema.Parser[string] {
	return validatorParser(func(raw string) (string, error) {
		return raw, nil
	}, func(value string) bool {
		return value != "" && (validate == nil || validate(value))
	})
}

func (p *Provider) GetString(key string, def string) string {
	v, _ := p.GetStringWithOrigin(key, def)
	return v
}

// GetStringWithOrigin is like GetString but also returns the origin of the winning
// configuration source. Use this when the caller needs to know where the value
// came from (e.g. to pass to DynamicConfig).
func (p *Provider) GetStringWithOrigin(key string, def string) (string, telemetry.Origin) {
	result := resolveCompatibility(p, key, def, compatibilityStringParser(nil))
	return result.Winner.Value, result.Winner.Origin
}

func (p *Provider) GetStringWithValidator(key string, def string, validate func(string) bool) string {
	result := resolveCompatibility(p, key, def, compatibilityStringParser(validate))
	return result.Winner.Value
}

func (p *Provider) GetBool(key string, def bool) bool {
	v, _ := p.GetBoolWithOrigin(key, def)
	return v
}

// GetBoolWithOrigin is like GetBool but also returns the origin of the winning
// configuration source. Use this when the caller needs to know where the value
// came from (e.g. to pass to DynamicConfig).
func (p *Provider) GetBoolWithOrigin(key string, def bool) (bool, telemetry.Origin) {
	result := resolveCompatibility(p, key, def, strconv.ParseBool)
	return result.Winner.Value, result.Winner.Origin
}

func (p *Provider) GetInt(key string, def int) int {
	return resolveCompatibility(p, key, def, strconv.Atoi).Winner.Value
}

func (p *Provider) GetIntWithValidator(key string, def int, validate func(int) bool) int {
	return resolveCompatibility(p, key, def, validatorParser(strconv.Atoi, validate)).Winner.Value
}

func (p *Provider) GetMap(key string, def map[string]string, delimiter string) map[string]string {
	return resolveCompatibility(p, key, def, func(v string) (map[string]string, error) {
		m := parseMapString(v, delimiter)
		if len(m) == 0 {
			return m, errInvalidValue
		}
		return m, nil
	}).Winner.Value
}

func (p *Provider) GetDuration(key string, def time.Duration) time.Duration {
	return resolveCompatibility(p, key, def, time.ParseDuration).Winner.Value
}

func (p *Provider) GetFloat(key string, def float64) float64 {
	return resolveCompatibility(p, key, def, func(v string) (float64, error) {
		return strconv.ParseFloat(v, 64)
	}).Winner.Value
}

func (p *Provider) GetFloatWithValidator(key string, def float64, validate func(float64) bool) float64 {
	v, _ := p.GetFloatWithValidatorOrigin(key, def, validate)
	return v
}

// GetFloatWithValidatorOrigin is like GetFloatWithValidator but also returns the
// origin of the winning configuration source. Use this when the caller needs to
// know where the value came from (e.g. to pass to DynamicConfig).
func (p *Provider) GetFloatWithValidatorOrigin(key string, def float64, validate func(float64) bool) (float64, telemetry.Origin) {
	result := resolveCompatibility(p, key, def, validatorParser(func(v string) (float64, error) {
		return strconv.ParseFloat(v, 64)
	}, validate))
	return result.Winner.Value, result.Winner.Origin
}

// IsSet returns true if any configuration source provides a non-empty value for the key.
//
// TODO: populate an isSet field on the Provider at the time of iterating over
// sources instead of re-querying them here.
func (p *Provider) IsSet(key string) bool {
	for _, source := range p.sources {
		if raw, _ := source.lookup(key); raw != "" {
			return true
		}
	}
	return false
}

// normalizeKey normalizes the key to a valid environment variable name.
func normalizeKey(key string) string {
	if strings.HasPrefix(key, "DD_") || strings.HasPrefix(key, "OTEL_") {
		return key
	}
	return "DD_" + strings.ToUpper(key)
}

// parseMapString parses a string containing key-value pairs separated by comma or space.
// It prioritizes the Datadog delimiter (:) over the OTel delimiter (=)
func parseMapString(str string, delimiter string) map[string]string {
	result := make(map[string]string)
	internal.ForEachStringTag(str, delimiter, func(key, val string) {
		result[key] = val
	})
	return result
}
