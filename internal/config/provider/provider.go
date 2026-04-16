// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package provider resolves configuration values from multiple sources in priority order
// and reports telemetry for each value found.
package provider

import (
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	configtelemetry "github.com/DataDog/dd-trace-go/v2/internal/config/configtelemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// configSource is a single origin of configuration key-value pairs.
type configSource interface {
	get(key string) string
	origin() telemetry.Origin
}

// idAwareConfigSource is a configSource that also carries a config_id, used by
// declarative config sources to associate values with a specific config set.
type idAwareConfigSource interface {
	configSource
	getID() string
}

// Provider resolves configuration values from an ordered list of sources.
// Sources are listed in descending priority order: the first source wins.
type Provider struct {
	sources []configSource
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

// get is the core resolution helper shared by all typed getters.
// It iterates sources in reverse priority order so that higher-priority sources
// overwrite lower-priority ones, reports telemetry for every source that has a value,
// and returns the highest-priority successfully-parsed value, or def if none parse.
func get[T any](p *Provider, key string, def T, parse func(string) (T, bool)) T {
	v, _ := getWithOrigin(p, key, def, parse)
	return v
}

// getWithOrigin is like get but also returns the origin of the winning source.
func getWithOrigin[T any](p *Provider, key string, def T, parse func(string) (T, bool)) (T, telemetry.Origin) {
	var final *T
	var winningOrigin telemetry.Origin
	for i := len(p.sources) - 1; i >= 0; i-- {
		source := p.sources[i]
		v := source.get(key)
		if v != "" {
			var id string
			if s, ok := source.(idAwareConfigSource); ok {
				id = s.getID()
			}
			configtelemetry.ReportWithID(key, v, source.origin(), id)
			if parsed, ok := parse(v); ok {
				final = &parsed
				winningOrigin = source.origin()
			}
		}
	}
	configtelemetry.ReportDefault(key, def)
	if final != nil {
		return *final, winningOrigin
	}
	return def, telemetry.OriginDefault
}

func (p *Provider) GetString(key string, def string) string {
	return get(p, key, def, func(v string) (string, bool) {
		return v, true
	})
}

func (p *Provider) GetStringWithValidator(key string, def string, validate func(string) bool) string {
	return get(p, key, def, func(v string) (string, bool) {
		if validate != nil && !validate(v) {
			return "", false
		}
		return v, true
	})
}

func (p *Provider) GetBool(key string, def bool) bool {
	return get(p, key, def, func(v string) (bool, bool) {
		boolVal, err := strconv.ParseBool(v)
		if err == nil {
			return boolVal, true
		}
		return false, false
	})
}

func (p *Provider) GetInt(key string, def int) int {
	return get(p, key, def, func(v string) (int, bool) {
		intVal, err := strconv.Atoi(v)
		return intVal, err == nil
	})
}

func (p *Provider) GetIntWithValidator(key string, def int, validate func(int) bool) int {
	return get(p, key, def, func(v string) (int, bool) {
		intVal, err := strconv.Atoi(v)
		if err == nil {
			if validate != nil && !validate(intVal) {
				return 0, false
			}
			return intVal, true
		}
		return 0, false
	})
}

func (p *Provider) GetMap(key string, def map[string]string, delimiter string) map[string]string {
	return get(p, key, def, func(v string) (map[string]string, bool) {
		m := parseMapString(v, delimiter)
		return m, len(m) > 0
	})
}

func (p *Provider) GetDuration(key string, def time.Duration) time.Duration {
	return get(p, key, def, func(v string) (time.Duration, bool) {
		d, err := time.ParseDuration(v)
		return d, err == nil
	})
}

func (p *Provider) GetFloat(key string, def float64) float64 {
	return get(p, key, def, func(v string) (float64, bool) {
		floatVal, err := strconv.ParseFloat(v, 64)
		return floatVal, err == nil
	})
}

func (p *Provider) GetFloatWithValidator(key string, def float64, validate func(float64) bool) float64 {
	v, _ := p.GetFloatWithValidatorOrigin(key, def, validate)
	return v
}

// GetFloatWithValidatorOrigin is like GetFloatWithValidator but also returns the
// origin of the winning configuration source. Use this when the caller needs to
// know where the value came from (e.g. to pass to DynamicConfig).
func (p *Provider) GetFloatWithValidatorOrigin(key string, def float64, validate func(float64) bool) (float64, telemetry.Origin) {
	return getWithOrigin(p, key, def, func(v string) (float64, bool) {
		floatVal, err := strconv.ParseFloat(v, 64)
		if err == nil {
			if validate != nil && !validate(floatVal) {
				return 0, false
			}
			return floatVal, true
		}
		return 0, false
	})
}

// IsSet returns true if any configuration source provides a non-empty value for the key.
//
// TODO: populate an isSet field on the Provider at the time of iterating over
// sources instead of re-querying them here.
func (p *Provider) IsSet(key string) bool {
	for _, source := range p.sources {
		if source.get(key) != "" {
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
