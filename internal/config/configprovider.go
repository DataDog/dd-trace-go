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

func (p *configProvider) getString(key string, def string) string {
	// TODO: Eventually, iterate over all sources and report telemetry
	for _, source := range p.sources {
		if v := source.get(key); v != "" {
			var id string
			if s, ok := source.(idAwareConfigSource); ok {
				id = s.getID()
			}
			telemetry.RegisterAppConfigs(telemetry.Configuration{Name: key, Value: v, Origin: source.origin(), ID: id})
			return v
		}
	}
	telemetry.RegisterAppConfigs(telemetry.Configuration{Name: key, Value: def, Origin: telemetry.OriginDefault, ID: telemetry.EmptyID})
	return def
}

func (p *configProvider) getBool(key string, def bool) bool {
	for _, source := range p.sources {
		if v := source.get(key); v != "" {
			var id string
			if s, ok := source.(idAwareConfigSource); ok {
				id = s.getID()
			}
			boolVal, err := strconv.ParseBool(v)
			if err == nil {
				telemetry.RegisterAppConfigs(telemetry.Configuration{Name: key, Value: v, Origin: source.origin(), ID: id})
				return boolVal
			}
		}
	}
	telemetry.RegisterAppConfigs(telemetry.Configuration{Name: key, Value: def, Origin: telemetry.OriginDefault, ID: telemetry.EmptyID})
	return def
}

func (p *configProvider) getInt(key string, def int) int {
	for _, source := range p.sources {
		if v := source.get(key); v != "" {
			var id string
			if s, ok := source.(idAwareConfigSource); ok {
				id = s.getID()
			}
			intVal, err := strconv.Atoi(v)
			if err == nil {
				telemetry.RegisterAppConfigs(telemetry.Configuration{Name: key, Value: v, Origin: source.origin(), ID: id})
				return intVal
			}
		}
	}
	telemetry.RegisterAppConfigs(telemetry.Configuration{Name: key, Value: def, Origin: telemetry.OriginDefault, ID: telemetry.EmptyID})
	return def
}

func (p *configProvider) getMap(key string, def map[string]string) map[string]string {
	for _, source := range p.sources {
		if v := source.get(key); v != "" {
			var id string
			if s, ok := source.(idAwareConfigSource); ok {
				id = s.getID()
			}
			m := parseMapString(v)
			if len(m) > 0 {
				telemetry.RegisterAppConfigs(telemetry.Configuration{Name: key, Value: v, Origin: source.origin(), ID: id})
				return m
			}
		}
	}
	telemetry.RegisterAppConfigs(telemetry.Configuration{Name: key, Value: def, Origin: telemetry.OriginDefault, ID: telemetry.EmptyID})
	return def
}

func (p *configProvider) getDuration(key string, def time.Duration) time.Duration {
	for _, source := range p.sources {
		if v := source.get(key); v != "" {
			var id string
			if s, ok := source.(idAwareConfigSource); ok {
				id = s.getID()
			}
			d, err := time.ParseDuration(v)
			if err == nil {
				telemetry.RegisterAppConfigs(telemetry.Configuration{Name: key, Value: v, Origin: source.origin(), ID: id})
				return d
			}
		}
	}
	telemetry.RegisterAppConfigs(telemetry.Configuration{Name: key, Value: def, Origin: telemetry.OriginDefault, ID: telemetry.EmptyID})
	return def
}

func (p *configProvider) getFloat(key string, def float64) float64 {
	for _, source := range p.sources {
		if v := source.get(key); v != "" {
			var id string
			if s, ok := source.(idAwareConfigSource); ok {
				id = s.getID()
			}
			floatVal, err := strconv.ParseFloat(v, 64)
			if err == nil {
				telemetry.RegisterAppConfigs(telemetry.Configuration{Name: key, Value: v, Origin: source.origin(), ID: id})
				return floatVal
			}
		}
	}
	telemetry.RegisterAppConfigs(telemetry.Configuration{Name: key, Value: def, Origin: telemetry.OriginDefault, ID: telemetry.EmptyID})
	return def
}

func (p *configProvider) getURL(key string, def *url.URL) *url.URL {
	for _, source := range p.sources {
		if v := source.get(key); v != "" {
			var id string
			if s, ok := source.(idAwareConfigSource); ok {
				id = s.getID()
			}
			u, err := url.Parse(v)
			if err == nil {
				telemetry.RegisterAppConfigs(telemetry.Configuration{Name: key, Value: v, Origin: source.origin(), ID: id})
				return u
			}
		}
	}
	telemetry.RegisterAppConfigs(telemetry.Configuration{Name: key, Value: def, Origin: telemetry.OriginDefault, ID: telemetry.EmptyID})
	return def
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
func parseMapString(str string) map[string]string {
	result := make(map[string]string)

	// Determine separator (comma or space)
	sep := " "
	if strings.Contains(str, ",") {
		sep = ","
	}

	// Parse each key:value pair
	for _, pair := range strings.Split(str, sep) {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}

		// Split on colon delimiter
		kv := strings.SplitN(pair, ":", 2)
		key := strings.TrimSpace(kv[0])
		if key == "" {
			continue
		}

		var val string
		if len(kv) == 2 {
			val = strings.TrimSpace(kv[1])
		}
		result[key] = val
	}

	return result
}
