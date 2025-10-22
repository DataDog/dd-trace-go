// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"testing"

	"net/url"

	"github.com/stretchr/testify/assert"
)

func newTestConfigProvider(sources ...ConfigSource) *ConfigProvider {
	return &ConfigProvider{
		sources: sources,
	}
}

type testConfigSource struct {
	entries map[string]string
}

func newTestConfigSource() *testConfigSource {
	t := &testConfigSource{
		entries: make(map[string]string),
	}

	t.entries["STRING_KEY"] = "string"
	t.entries["BOOL_KEY"] = "true"
	t.entries["INT_KEY"] = "1"
	t.entries["FLOAT_KEY"] = "1.0"
	t.entries["URL_KEY"] = "https://localhost:8126"

	return t
}

func (s *testConfigSource) Get(key string) string {
	return s.entries[key]
}

func TestGetMethods(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		provider := newTestConfigProvider(newTestConfigSource())
		assert.Equal(t, "value", provider.getString("NONEXISTENT_KEY", "value"))
		assert.Equal(t, false, provider.getBool("NONEXISTENT_KEY", false))
		assert.Equal(t, 0, provider.getInt("NONEXISTENT_KEY", 0))
		assert.Equal(t, 0.0, provider.getFloat("NONEXISTENT_KEY", 0.0))
		assert.Equal(t, &url.URL{Scheme: "https", Host: "otherhost:1234"}, provider.getURL("NONEXISTENT_KEY", &url.URL{Scheme: "https", Host: "otherhost:1234"}))
	})
	t.Run("non-defaults", func(t *testing.T) {
		provider := newTestConfigProvider(newTestConfigSource())
		assert.Equal(t, "string", provider.getString("STRING_KEY", "value"))
		assert.Equal(t, true, provider.getBool("BOOL_KEY", false))
		assert.Equal(t, 1, provider.getInt("INT_KEY", 0))
		assert.Equal(t, 1.0, provider.getFloat("FLOAT_KEY", 0.0))
		assert.Equal(t, &url.URL{Scheme: "https", Host: "localhost:8126"}, provider.getURL("URL_KEY", &url.URL{Scheme: "https", Host: "localhost:8126"}))
	})
}
