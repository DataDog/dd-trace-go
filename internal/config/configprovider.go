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
)

var provider = DefaultConfigProvider()

type ConfigProvider struct {
	sources []ConfigSource // In order of priority
}

func (p *ConfigProvider) getString(key string, def string) string {
	// TODO: Eventually, iterate over all sources and report telemetry
	for _, source := range p.sources {
		if v := source.Get(key); v != "" {
			return v
		}
	}
	return def
}

func (p *ConfigProvider) getBool(key string, def bool) bool {
	for _, source := range p.sources {
		if v := source.Get(key); v != "" {
			if v == "true" {
				return true
			} else if v == "false" {
				return false
			}
		}
	}
	return def
}

func (p *ConfigProvider) getInt(key string, def int) int {
	for _, source := range p.sources {
		if v := source.Get(key); v != "" {
			v, err := strconv.Atoi(v)
			if err == nil {
				return v
			}
		}
	}
	return def
}

func (p *ConfigProvider) getInt64(key string, def int64) int64 {
	for _, source := range p.sources {
		if v := source.Get(key); v != "" {
			v, err := strconv.ParseInt(v, 10, 64)
			if err == nil {
				return v
			}
		}
	}
	return def
}

func (p *ConfigProvider) getMap(key string, def map[string]string) map[string]string {
	for _, source := range p.sources {
		if v := source.Get(key); v != "" {
			m := parseMapString(v)
			if len(m) > 0 {
				return m
			}
		}
	}
	return def
}

func (p *ConfigProvider) getDuration(key string, def time.Duration) time.Duration {
	for _, source := range p.sources {
		if v := source.Get(key); v != "" {
			d, err := time.ParseDuration(v)
			if err == nil {
				return d
			}
		}
	}
	return def
}

func (p *ConfigProvider) getFloat(key string, def float64) float64 {
	for _, source := range p.sources {
		if v := source.Get(key); v != "" {
			v, err := strconv.ParseFloat(v, 64)
			if err == nil {
				return v
			}
		}
	}
	return def
}

func (p *ConfigProvider) getURL(key string, def *url.URL) *url.URL {
	for _, source := range p.sources {
		if v := source.Get(key); v != "" {
			u, err := url.Parse(v)
			if err == nil {
				return u
			}
		}
	}
	return def
}

func DefaultConfigProvider() *ConfigProvider {
	return &ConfigProvider{
		sources: []ConfigSource{
			ManagedDeclarativeConfig,
			new(envConfigSource),
			LocalDeclarativeConfig,
		},
	}
}

type ConfigSource interface {
	Get(key string) string
}

// normalizeKey is a helper function for ConfigSource implementations to normalize the key to a valid environment variable name.
func normalizeKey(key string) string {
	// Try to convert key to a valid environment variable name
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
