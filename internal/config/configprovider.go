// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

var provider = defaultconfigProvider()

type configProvider struct {
	sources []configSource // In order of priority
}

type configSource interface {
	get(key string) string
	origin() telemetry.Origin
}

type idAwareConfigSource interface {
	configSource
	getID() string
}

func defaultconfigProvider() *configProvider {
	return &configProvider{
		sources: []configSource{
			newDeclarativeConfigSource(managedFilePath, telemetry.OriginManagedStableConfig),
			new(envConfigSource),
			new(otelEnvConfigSource),
			newDeclarativeConfigSource(localFilePath, telemetry.OriginLocalStableConfig),
		},
	}
}

// get is a generic helper that iterates through config sources and parses values, returning the first successfully parsed value.
// The parse function should return the parsed value and true if parsing succeeded, or false otherwise.
//
// Telemetry Reporting:
//   - Reports telemetry for ALL non-empty values found across ALL sources, regardless of priority
//   - SeqID reflects priority: highest priority source gets len(sources), decreasing to 1 for lowest priority
func get[T any](p *configProvider, key string, def T, parse func(string) (T, bool)) T {
	var final *T
	seqId := uint64(len(p.sources))
	for _, source := range p.sources {
		if v := source.get(key); v != "" {
			var id string
			if s, ok := source.(idAwareConfigSource); ok {
				id = s.getID()
			}
			telemetry.RegisterAppConfigs(telemetry.Configuration{Name: key, Value: v, Origin: source.origin(), ID: id, SeqID: seqId})
			if parsed, ok := parse(v); ok {
				if final == nil {
					final = &parsed
				}
			}
		}
		seqId--
	}
	telemetry.RegisterAppConfigs(telemetry.Configuration{Name: key, Value: def, Origin: telemetry.OriginDefault, ID: telemetry.EmptyID, SeqID: 0})
	if final != nil {
		return *final
	}
	return def
}

func (p *configProvider) getString(key string, def string) string {
	return get(p, key, def, func(v string) (string, bool) {
		return v, true
	})
}

func (p *configProvider) getBool(key string, def bool) bool {
	return get(p, key, def, func(v string) (bool, bool) {
		boolVal, err := strconv.ParseBool(v)
		if err == nil {
			return boolVal, true
		}
		return false, false
	})
}

func (p *configProvider) getInt(key string, def int) int {
	return get(p, key, def, func(v string) (int, bool) {
		intVal, err := strconv.Atoi(v)
		return intVal, err == nil
	})
}

func (p *configProvider) getIntWithValidator(key string, def int, validate func(int) bool) int {
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

func (p *configProvider) getMap(key string, def map[string]string) map[string]string {
	return get(p, key, def, func(v string) (map[string]string, bool) {
		m := parseMapString(v)
		return m, len(m) > 0
	})
}

func (p *configProvider) getDuration(key string, def time.Duration) time.Duration {
	return get(p, key, def, func(v string) (time.Duration, bool) {
		d, err := time.ParseDuration(v)
		return d, err == nil
	})
}

func (p *configProvider) getFloat(key string, def float64) float64 {
	return get(p, key, def, func(v string) (float64, bool) {
		floatVal, err := strconv.ParseFloat(v, 64)
		return floatVal, err == nil
	})
}

func (p *configProvider) getFloatWithValidator(key string, def float64, validate func(float64) bool) float64 {
	return get(p, key, def, func(v string) (float64, bool) {
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

func (p *configProvider) getURL(key string, def *url.URL) *url.URL {
	return get(p, key, def, func(v string) (*url.URL, bool) {
		u, err := url.Parse(v)
		return u, err == nil
	})
}

// normalizeKey is a helper function for configSource implementations to normalize the key to a valid environment variable name.
func normalizeKey(key string) string {
	if strings.HasPrefix(key, "DD_") || strings.HasPrefix(key, "OTEL_") {
		return key
	}
	return "DD_" + strings.ToUpper(key)
}

// parseMapString parses a string containing key:value pairs separated by comma or space.
// Format: "key1:value1,key2:value2" or "key1:value1 key2:value2"
// Uses internal.ForEachStringTag to ensure consistent parsing with other tag-like env vars.
func parseMapString(str string) map[string]string {
	result := make(map[string]string)
	internal.ForEachStringTag(str, internal.DDTagsDelimiter, func(key, val string) {
		result[key] = val
	})
	return result
}
