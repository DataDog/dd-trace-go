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
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestConfigProvider(sources ...ConfigSource) *ConfigProvider {
	return &ConfigProvider{
		sources: sources,
	}
}

type testConfigSource struct {
	entries map[string]string
	origin  telemetry.Origin
}

func newTestConfigSource(entries map[string]string, origin telemetry.Origin) *testConfigSource {
	if entries == nil {
		entries = make(map[string]string)
	}
	return &testConfigSource{
		entries: entries,
		origin:  origin,
	}
}

func (s *testConfigSource) Get(key string) string {
	return s.entries[key]
}

func (s *testConfigSource) Origin() telemetry.Origin {
	return s.origin
}

func TestGetMethods(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		// Test that defaults are used when the queried key does not exist
		provider := newTestConfigProvider(newTestConfigSource(nil, telemetry.OriginEnvVar))
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
		provider := newTestConfigProvider(newTestConfigSource(entries, telemetry.OriginEnvVar))
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

	t.Run("Settings only exist in OtelEnvConfigSource", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "string")
		t.Setenv("OTEL_LOG_LEVEL", "debug")
		t.Setenv("OTEL_TRACES_SAMPLER", "parentbased_always_on")
		t.Setenv("OTEL_TRACES_EXPORTER", "1.0")
		t.Setenv("OTEL_PROPAGATORS", "https://localhost:8126")
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "key1=value1,key2=value2")

		provider := DefaultConfigProvider()

		assert.Equal(t, "string", provider.getString("DD_SERVICE", "value"))
		assert.Equal(t, true, provider.getBool("DD_TRACE_DEBUG", false))
		assert.Equal(t, 1.0, provider.getFloat("DD_TRACE_SAMPLE_RATE", 0))
		assert.Equal(t, 1.0, provider.getFloat("DD_TRACE_SAMPLE_RATE", 0.0))
		assert.Equal(t, &url.URL{Scheme: "https", Host: "localhost:8126"}, provider.getURL("DD_TRACE_AGENT_URL", &url.URL{Scheme: "https", Host: "localhost:8126"}))
		assert.Equal(t, "key1:value1,key2:value2", provider.getString("DD_TAGS", "key:value"))
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
		// Priority order (highest to lowest):
		// 1. ManagedDeclarativeConfig
		// 2. EnvConfigSource (DD_* env vars)
		// 3. OtelEnvConfigSource (OTEL_* env vars)
		// 4. LocalDeclarativeConfig

		// Setup: Configure the same keys across multiple sources with different values
		// to verify that the correct precedence is applied

		localYaml := `
apm_configuration_default:
  DD_SERVICE: local_service           # Set in all 4 sources - should lose to Managed
  DD_TRACE_DEBUG: false                # Set in all 4 sources - should lose to Managed
  DD_ENV: local_env                    # Set in 3 sources (Local, DD Env, OTEL) - should lose to DD Env
  DD_VERSION: 0.1.0                    # Set in 2 sources (Local, Managed) - should lose to Managed
  DD_TRACE_SAMPLE_RATE: 0.1            # Set in 2 sources (Local, OTEL) - should lose to OTEL
  DD_TRACE_STARTUP_LOGS: true          # Only in Local - should WIN (lowest priority available)
`

		managedYaml := `
apm_configuration_default:
  DD_SERVICE: managed_service          # Set in all 4 sources - should WIN (highest priority)
  DD_TRACE_DEBUG: true                 # Set in all 4 sources - should WIN (highest priority)
  DD_VERSION: 1.0.0                    # Set in 2 sources (Local, Managed) - should WIN
  DD_TRACE_PARTIAL_FLUSH_ENABLED: true # Set in 2 sources (Managed, DD Env) - should WIN
`

		// DD Env vars - priority level 2
		t.Setenv("DD_SERVICE", "env_service")               // Set in all 4 sources - should lose to Managed
		t.Setenv("DD_TRACE_DEBUG", "false")                 // Set in all 4 sources - should lose to Managed
		t.Setenv("DD_ENV", "env_environment")               // Set in 3 sources - should WIN (higher than OTEL and Local)
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "false") // Set in 2 sources - should lose to Managed
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "100") // Only in DD Env - should WIN

		// OTEL Env vars - priority level 3
		t.Setenv("OTEL_SERVICE_NAME", "otel_service")                                                 // Set in all 4 sources (maps to DD_SERVICE) - should lose to Managed
		t.Setenv("OTEL_LOG_LEVEL", "debug")                                                           // Set in all 4 sources (maps to DD_TRACE_DEBUG) - should lose to Managed
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=otel_env,service.version=0.5.0") // Set in 3 sources - should lose to DD Env for DD_ENV, but provide version if not in higher sources
		t.Setenv("OTEL_TRACES_SAMPLER", "traceidratio")                                               // Set in 2 sources (OTEL, Local) - should WIN over Local (maps to DD_TRACE_SAMPLE_RATE)
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.8")                                                    // Provides sample rate value of 0.8

		// Create config files
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

		// Assertions grouped by which source should win

		// Managed Config wins (set in all 4 sources)
		assert.Equal(t, "managed_service", provider.getString("DD_SERVICE", "default"),
			"DD_SERVICE: Managed should win over DD Env, OTEL, and Local")
		assert.Equal(t, true, provider.getBool("DD_TRACE_DEBUG", false),
			"DD_TRACE_DEBUG: Managed should win over DD Env, OTEL, and Local")

		// Managed Config wins (set in 2 sources: Managed + one other)
		assert.Equal(t, "1.0.0", provider.getString("DD_VERSION", "default"),
			"DD_VERSION: Managed should win over Local")
		assert.Equal(t, true, provider.getBool("DD_TRACE_PARTIAL_FLUSH_ENABLED", false),
			"DD_TRACE_PARTIAL_FLUSH_ENABLED: Managed should win over DD Env")

		// DD Env wins (set in 3 sources: DD Env, OTEL, Local)
		assert.Equal(t, "env_environment", provider.getString("DD_ENV", "default"),
			"DD_ENV: DD Env should win over OTEL and Local")

		// DD Env wins (only in DD Env)
		assert.Equal(t, 100, provider.getInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 0),
			"DD_TRACE_PARTIAL_FLUSH_MIN_SPANS: DD Env should win (only source)")

		// OTEL Env wins (set in 2 sources: OTEL, Local)
		assert.Equal(t, 0.8, provider.getFloat("DD_TRACE_SAMPLE_RATE", 0.0),
			"DD_TRACE_SAMPLE_RATE: OTEL should win over Local")

		// Local Config wins (only in Local)
		assert.Equal(t, true, provider.getBool("DD_TRACE_STARTUP_LOGS", false),
			"DD_TRACE_STARTUP_LOGS: Local should win (only source)")

		// Defaults are returned for settings not configured anywhere
		assert.Equal(t, "default", provider.getString("DD_TRACE_AGENT_URL", "default"),
			"Unconfigured setting should return default")
	})
}

func TestConfigProviderTelemetryRegistration(t *testing.T) {
	t.Run("env source reports telemetry for all getters", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		// Expectations: value is the raw string from the source; ID is empty
		telemetryClient.On("RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_SERVICE", Value: "service", Origin: telemetry.OriginEnvVar, ID: telemetry.EmptyID}}).Return()
		telemetryClient.On("RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_DEBUG", Value: "true", Origin: telemetry.OriginEnvVar, ID: telemetry.EmptyID}}).Return()
		telemetryClient.On("RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", Value: "100", Origin: telemetry.OriginEnvVar, ID: telemetry.EmptyID}}).Return()
		telemetryClient.On("RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_SAMPLE_RATE", Value: "0.5", Origin: telemetry.OriginEnvVar, ID: telemetry.EmptyID}}).Return()
		telemetryClient.On("RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_AGENT_URL", Value: "http://localhost:8126", Origin: telemetry.OriginEnvVar, ID: telemetry.EmptyID}}).Return()
		telemetryClient.On("RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_SERVICE_MAPPING", Value: "old:new", Origin: telemetry.OriginEnvVar, ID: telemetry.EmptyID}}).Return()
		telemetryClient.On("RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_ABANDONED_SPAN_TIMEOUT", Value: "10s", Origin: telemetry.OriginEnvVar, ID: telemetry.EmptyID}}).Return()
		defer telemetry.MockClient(telemetryClient)()

		source := newTestConfigSource(map[string]string{
			"DD_SERVICE":                       "service",
			"DD_TRACE_DEBUG":                   "true",
			"DD_TRACE_PARTIAL_FLUSH_MIN_SPANS": "100",
			"DD_TRACE_SAMPLE_RATE":             "0.5",
			"DD_TRACE_AGENT_URL":               "http://localhost:8126",
			"DD_SERVICE_MAPPING":               "old:new",
			"DD_TRACE_ABANDONED_SPAN_TIMEOUT":  "10s",
		}, telemetry.OriginEnvVar)
		provider := newTestConfigProvider(source)

		_ = provider.getString("DD_SERVICE", "default")
		_ = provider.getBool("DD_TRACE_DEBUG", false)
		_ = provider.getInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 0)
		_ = provider.getFloat("DD_TRACE_SAMPLE_RATE", 0.0)
		_ = provider.getURL("DD_TRACE_AGENT_URL", nil)
		_ = provider.getMap("DD_SERVICE_MAPPING", nil)
		_ = provider.getDuration("DD_TRACE_ABANDONED_SPAN_TIMEOUT", 0)

		telemetryClient.AssertCalled(t, "RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_SERVICE", Value: "service", Origin: telemetry.OriginEnvVar, ID: telemetry.EmptyID}})
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_DEBUG", Value: "true", Origin: telemetry.OriginEnvVar, ID: telemetry.EmptyID}})
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", Value: "100", Origin: telemetry.OriginEnvVar, ID: telemetry.EmptyID}})
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_SAMPLE_RATE", Value: "0.5", Origin: telemetry.OriginEnvVar, ID: telemetry.EmptyID}})
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_AGENT_URL", Value: "http://localhost:8126", Origin: telemetry.OriginEnvVar, ID: telemetry.EmptyID}})
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_SERVICE_MAPPING", Value: "old:new", Origin: telemetry.OriginEnvVar, ID: telemetry.EmptyID}})
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_ABANDONED_SPAN_TIMEOUT", Value: "10s", Origin: telemetry.OriginEnvVar, ID: telemetry.EmptyID}})
	})

	t.Run("declarative source reports telemetry with ID", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		// Values expected as raw strings, with OriginLocalStableConfig and ID from file
		yaml := `config_id: 123
apm_configuration_default:
  DD_SERVICE: svc
  DD_TRACE_DEBUG: true
  DD_TRACE_PARTIAL_FLUSH_MIN_SPANS: "7"
  DD_TRACE_SAMPLE_RATE: 0.9
  DD_TRACE_AGENT_URL: http://127.0.0.1:8126
  DD_SERVICE_MAPPING: a:b
  DD_TRACE_ABANDONED_SPAN_TIMEOUT: 2s
`
		temp := "decl.yml"
		require.NoError(t, os.WriteFile(temp, []byte(yaml), 0644))
		defer os.Remove(temp)

		telemetryClient.On("RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_SERVICE", Value: "svc", Origin: telemetry.OriginLocalStableConfig, ID: "123"}}).Return()
		telemetryClient.On("RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_DEBUG", Value: "true", Origin: telemetry.OriginLocalStableConfig, ID: "123"}}).Return()
		telemetryClient.On("RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", Value: "7", Origin: telemetry.OriginLocalStableConfig, ID: "123"}}).Return()
		telemetryClient.On("RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_SAMPLE_RATE", Value: "0.9", Origin: telemetry.OriginLocalStableConfig, ID: "123"}}).Return()
		telemetryClient.On("RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_AGENT_URL", Value: "http://127.0.0.1:8126", Origin: telemetry.OriginLocalStableConfig, ID: "123"}}).Return()
		telemetryClient.On("RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_SERVICE_MAPPING", Value: "a:b", Origin: telemetry.OriginLocalStableConfig, ID: "123"}}).Return()
		telemetryClient.On("RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_ABANDONED_SPAN_TIMEOUT", Value: "2s", Origin: telemetry.OriginLocalStableConfig, ID: "123"}}).Return()
		defer telemetry.MockClient(telemetryClient)()

		decl := newDeclarativeConfigSource(temp, telemetry.OriginLocalStableConfig)
		provider := newTestConfigProvider(decl)

		_ = provider.getString("DD_SERVICE", "default")
		_ = provider.getBool("DD_TRACE_DEBUG", false)
		_ = provider.getInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 0)
		_ = provider.getFloat("DD_TRACE_SAMPLE_RATE", 0.0)
		_ = provider.getURL("DD_TRACE_AGENT_URL", nil)
		_ = provider.getMap("DD_SERVICE_MAPPING", nil)
		_ = provider.getDuration("DD_TRACE_ABANDONED_SPAN_TIMEOUT", 0)

		telemetryClient.AssertCalled(t, "RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_SERVICE", Value: "svc", Origin: telemetry.OriginLocalStableConfig, ID: "123"}})
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_DEBUG", Value: "true", Origin: telemetry.OriginLocalStableConfig, ID: "123"}})
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", Value: "7", Origin: telemetry.OriginLocalStableConfig, ID: "123"}})
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_SAMPLE_RATE", Value: "0.9", Origin: telemetry.OriginLocalStableConfig, ID: "123"}})
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_AGENT_URL", Value: "http://127.0.0.1:8126", Origin: telemetry.OriginLocalStableConfig, ID: "123"}})
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_SERVICE_MAPPING", Value: "a:b", Origin: telemetry.OriginLocalStableConfig, ID: "123"}})
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", []telemetry.Configuration{{Name: "DD_TRACE_ABANDONED_SPAN_TIMEOUT", Value: "2s", Origin: telemetry.OriginLocalStableConfig, ID: "123"}})
	})

	t.Run("source priority with config IDs", func(t *testing.T) {
		// Test that when multiple sources exist, only the winning source's
		// telemetry (including its config ID) is registered

		yamlManaged := `config_id: managed-123
apm_configuration_default:
  DD_SERVICE: managed-service
`
		yamlLocal := `config_id: local-456
apm_configuration_default:
  DD_SERVICE: local-service
  DD_ENV: local-env
`
		tempManaged := "test_managed.yml"
		tempLocal := "test_local.yml"

		require.NoError(t, os.WriteFile(tempManaged, []byte(yamlManaged), 0644))
		require.NoError(t, os.WriteFile(tempLocal, []byte(yamlLocal), 0644))
		defer os.Remove(tempManaged)
		defer os.Remove(tempLocal)

		managedSource := newDeclarativeConfigSource(tempManaged, telemetry.OriginManagedStableConfig)
		localSource := newDeclarativeConfigSource(tempLocal, telemetry.OriginLocalStableConfig)

		// Managed has higher priority than Local
		provider := newTestConfigProvider(managedSource, localSource)

		// For DD_SERVICE: managed wins, so telemetry gets ID "managed-123"
		result := provider.getString("DD_SERVICE", "default")
		assert.Equal(t, "managed-service", result)

		// For DD_ENV: local wins (managed doesn't have it), so telemetry gets ID "local-456"
		env := provider.getString("DD_ENV", "default")
		assert.Equal(t, "local-env", env)
	})
}
