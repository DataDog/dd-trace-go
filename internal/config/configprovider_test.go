// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"os"
	"testing"

	"net/url"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
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

func newTestConfigSource(entries map[string]string) *testConfigSource {
	if entries == nil {
		entries = make(map[string]string)
	}
	return &testConfigSource{
		entries: entries,
	}
}

func (s *testConfigSource) Get(key string) string {
	return s.entries[key]
}

func TestGetMethods(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		// Test that defaults are used when the queried key does not exist
		provider := newTestConfigProvider(newTestConfigSource(nil))
		assert.Equal(t, "value", provider.getString("DD_SERVICE", "value"))
		assert.Equal(t, true, provider.getBool("DD_TRACE_DEBUG", true))
		assert.Equal(t, 1, provider.getInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 1))
		assert.Equal(t, 1.0, provider.getFloat("DD_TRACE_SAMPLE_RATE", 1.0))
		assert.Equal(t, &url.URL{Scheme: "http", Host: "localhost:8126"}, provider.getURL("DD_TRACE_AGENT_URL", &url.URL{Scheme: "http", Host: "localhost:8126"}))
	})
	t.Run("non-defaults", func(t *testing.T) {
		// Test that non-defaults are used when the queried key exists
		entries := map[string]string{
			"DD_SERVICE":                       "string",
			"DD_TRACE_DEBUG":                   "true",
			"DD_TRACE_PARTIAL_FLUSH_MIN_SPANS": "1",
			"DD_TRACE_SAMPLE_RATE":             "1.0",
			"DD_TRACE_AGENT_URL":               "https://localhost:8126",
		}
		provider := newTestConfigProvider(newTestConfigSource(entries))
		assert.Equal(t, "string", provider.getString("DD_SERVICE", "value"))
		assert.Equal(t, true, provider.getBool("DD_TRACE_DEBUG", false))
		assert.Equal(t, 1, provider.getInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 0))
		assert.Equal(t, 1.0, provider.getFloat("DD_TRACE_SAMPLE_RATE", 0.0))
		assert.Equal(t, &url.URL{Scheme: "https", Host: "localhost:8126"}, provider.getURL("DD_TRACE_AGENT_URL", &url.URL{Scheme: "https", Host: "localhost:8126"}))
	})
}

func TestDefaultConfigProvider(t *testing.T) {
	t.Run("Settings only exist in EnvConfigSource", func(t *testing.T) {
		// Setup: environment variables of each type
		t.Setenv("DD_SERVICE", "string")
		t.Setenv("DD_TRACE_DEBUG", "true")
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "1")
		t.Setenv("DD_TRACE_SAMPLE_RATE", "1.0")
		t.Setenv("DD_TRACE_AGENT_URL", "https://localhost:8126")
		// TODO: Add more types as we go along

		provider := DefaultConfigProvider()

		// Configured values are returned correctly
		assert.Equal(t, "string", provider.getString("DD_SERVICE", "value"))
		assert.Equal(t, true, provider.getBool("DD_TRACE_DEBUG", false))
		assert.Equal(t, 1, provider.getInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 0))
		assert.Equal(t, 1.0, provider.getFloat("DD_TRACE_SAMPLE_RATE", 0.0))
		assert.Equal(t, &url.URL{Scheme: "https", Host: "localhost:8126"}, provider.getURL("DD_TRACE_AGENT_URL", &url.URL{Scheme: "https", Host: "localhost:8126"}))

		// Defaults are returned for settings that are not configured
		assert.Equal(t, "value", provider.getString("DD_ENV", "value"))
	})
	t.Run("Settings only exist in LocalDeclarativeConfigSource", func(t *testing.T) {
		const localYaml = `
apm_configuration_default:
  DD_SERVICE: local
  DD_TRACE_DEBUG: true
  DD_TRACE_PARTIAL_FLUSH_MIN_SPANS: "1"
  DD_TRACE_SAMPLE_RATE: 1.0
  DD_TRACE_AGENT_URL: https://localhost:8126
`

		tempLocalPath := "local.yml"
		err := os.WriteFile(tempLocalPath, []byte(localYaml), 0644)
		assert.NoError(t, err)
		defer os.Remove(tempLocalPath)

		LocalDeclarativeConfig = newDeclarativeConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
		defer func() {
			LocalDeclarativeConfig = newDeclarativeConfigSource(localFilePath, telemetry.OriginLocalStableConfig)
		}()

		provider := DefaultConfigProvider()

		assert.Equal(t, "local", provider.getString("DD_SERVICE", "value"))
		assert.Equal(t, true, provider.getBool("DD_TRACE_DEBUG", false))
		assert.Equal(t, 1, provider.getInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 0))
		assert.Equal(t, 1.0, provider.getFloat("DD_TRACE_SAMPLE_RATE", 0.0))
		assert.Equal(t, &url.URL{Scheme: "https", Host: "localhost:8126"}, provider.getURL("DD_TRACE_AGENT_URL", &url.URL{Scheme: "https", Host: "localhost:8126"}))

		// Defaults are returned for settings that are not configured
		assert.Equal(t, "value", provider.getString("DD_ENV", "value"))
	})

	t.Run("Settings only exist in ManagedDeclarativeConfigSource", func(t *testing.T) {
		const managedYaml = `
apm_configuration_default:
  DD_SERVICE: managed
  DD_TRACE_DEBUG: true
  DD_TRACE_PARTIAL_FLUSH_MIN_SPANS: "1"
  DD_TRACE_SAMPLE_RATE: 1.0
  DD_TRACE_AGENT_URL: https://localhost:8126`

		tempManagedPath := "managed.yml"
		err := os.WriteFile(tempManagedPath, []byte(managedYaml), 0644)
		assert.NoError(t, err)
		defer os.Remove(tempManagedPath)

		ManagedDeclarativeConfig = newDeclarativeConfigSource(tempManagedPath, telemetry.OriginManagedStableConfig)
		defer func() {
			ManagedDeclarativeConfig = newDeclarativeConfigSource(managedFilePath, telemetry.OriginManagedStableConfig)
		}()

		provider := DefaultConfigProvider()

		assert.Equal(t, "managed", provider.getString("DD_SERVICE", "value"))
		assert.Equal(t, true, provider.getBool("DD_TRACE_DEBUG", false))
		assert.Equal(t, 1, provider.getInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 0))
		assert.Equal(t, 1.0, provider.getFloat("DD_TRACE_SAMPLE_RATE", 0.0))
		assert.Equal(t, &url.URL{Scheme: "https", Host: "localhost:8126"}, provider.getURL("DD_TRACE_AGENT_URL", &url.URL{Scheme: "https", Host: "localhost:8126"}))

		// Defaults are returned for settings that are not configured
		assert.Equal(t, "value", provider.getString("DD_ENV", "value"))
	})
	t.Run("Settings exist in all ConfigSources", func(t *testing.T) {
		localYaml := `
apm_configuration_default:
  DD_SERVICE: local
  DD_TRACE_DEBUG: false
  DD_TRACE_HOSTNAME: otherhost
  DD_TRACE_PARTIAL_FLUSH_MIN_SPANS: "1"`

		managedYaml := `
apm_configuration_default:
  DD_SERVICE: managed
  DD_TRACE_DEBUG: true
  DD_TRACE_LOG_TO_STDOUT: true
  DD_VERSION: 1.0.0`

		t.Setenv("DD_SERVICE", "env")
		t.Setenv("DD_TRACE_LOG_TO_STDOUT", "false")
		t.Setenv("DD_ENV", "dev")
		t.Setenv("DD_TRACE_HOSTNAME", "otherhost")

		tempLocalPath := "local.yml"
		err := os.WriteFile(tempLocalPath, []byte(localYaml), 0644)
		assert.NoError(t, err)
		defer os.Remove(tempLocalPath)

		LocalDeclarativeConfig = newDeclarativeConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
		defer func() {
			LocalDeclarativeConfig = newDeclarativeConfigSource(localFilePath, telemetry.OriginLocalStableConfig)
		}()

		tempManagedPath := "managed.yml"
		err = os.WriteFile(tempManagedPath, []byte(managedYaml), 0644)
		assert.NoError(t, err)
		defer os.Remove(tempManagedPath)

		ManagedDeclarativeConfig = newDeclarativeConfigSource(tempManagedPath, telemetry.OriginManagedStableConfig)
		defer func() {
			ManagedDeclarativeConfig = newDeclarativeConfigSource(managedFilePath, telemetry.OriginManagedStableConfig)
		}()

		provider := DefaultConfigProvider()
		assert.Equal(t, "managed", provider.getString("DD_SERVICE", "value"))
		assert.Equal(t, true, provider.getBool("DD_TRACE_DEBUG", false))
		assert.Equal(t, "otherhost", provider.getString("DD_TRACE_HOSTNAME", "value"))
		assert.Equal(t, 1, provider.getInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 0))
		assert.Equal(t, "dev", provider.getString("DD_ENV", "value"))
		assert.Equal(t, "1.0.0", provider.getString("DD_VERSION", "0"))
		assert.Equal(t, true, provider.getBool("DD_TRACE_LOG_TO_STDOUT", false))

		// Defaults are returned for settings that are not configured
		assert.Equal(t, false, provider.getBool("DD_TRACE_STARTUP_LOGS", false))
	})
}
