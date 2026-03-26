// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package provider

import (
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

// newTestProvider creates a Provider with custom sources for testing.
func newTestProvider(sources ...configSource) *Provider {
	return &Provider{sources: sources}
}

type testConfigSource struct {
	entries     map[string]string
	originValue telemetry.Origin
}

func newTestConfigSource(entries map[string]string, origin telemetry.Origin) *testConfigSource {
	if entries == nil {
		entries = make(map[string]string)
	}
	return &testConfigSource{
		entries:     entries,
		originValue: origin,
	}
}

func (s *testConfigSource) get(key string) string {
	return s.entries[key]
}

func (s *testConfigSource) origin() telemetry.Origin {
	return s.originValue
}

// matchConfig is a helper to create a matcher for telemetry configurations that ignores exact SeqID.
func matchConfig(name, value string, origin telemetry.Origin, id string) func([]telemetry.Configuration) bool {
	return func(configs []telemetry.Configuration) bool {
		if len(configs) != 1 {
			return false
		}
		c := configs[0]
		return c.Name == name && c.Value == value && c.Origin == origin && c.ID == id && c.SeqID > 0
	}
}

// matchDefaultConfig is a helper to create a matcher for default telemetry configurations.
// Defaults are identified by origin == OriginDefault; SeqID ordering is tested in configtelemetry_test.go.
func matchDefaultConfig(name string, value any) func([]telemetry.Configuration) bool {
	return func(configs []telemetry.Configuration) bool {
		if len(configs) != 1 {
			return false
		}
		c := configs[0]
		return c.Name == name && reflect.DeepEqual(c.Value, value) && c.Origin == telemetry.OriginDefault && c.ID == telemetry.EmptyID
	}
}

// seqIDCapture captures SeqIDs from telemetry calls for ordering verification.
type seqIDCapture struct {
	seqIDs map[string]uint64
}

func newSeqIDCapture() *seqIDCapture {
	return &seqIDCapture{seqIDs: make(map[string]uint64)}
}

func (s *seqIDCapture) key(name, value string, origin telemetry.Origin) string {
	return name + ":" + value + ":" + string(origin)
}

func (s *seqIDCapture) captureMatcher(name, value string, origin telemetry.Origin, id string) func([]telemetry.Configuration) bool {
	return func(configs []telemetry.Configuration) bool {
		if len(configs) != 1 {
			return false
		}
		c := configs[0]
		if c.Name == name && c.Value == value && c.Origin == origin && c.ID == id {
			s.seqIDs[s.key(name, value, origin)] = c.SeqID
			return true
		}
		return false
	}
}

func (s *seqIDCapture) get(name, value string, origin telemetry.Origin) uint64 {
	return s.seqIDs[s.key(name, value, origin)]
}

func TestGetMethods(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		p := newTestProvider(newTestConfigSource(nil, telemetry.OriginEnvVar))
		assert.Equal(t, "value", p.GetString("DD_SERVICE", "value"))
		assert.Equal(t, true, p.GetBool("DD_TRACE_DEBUG", true))
		assert.Equal(t, 1, p.GetInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 1))
		assert.Equal(t, 1.0, p.GetFloat("DD_TRACE_SAMPLE_RATE", 1.0))
		assert.Equal(t, "", p.GetString("DD_TRACE_AGENT_URL", ""))
	})
	t.Run("non-defaults", func(t *testing.T) {
		entries := map[string]string{
			"DD_SERVICE":                       "string",
			"DD_TRACE_DEBUG":                   "true",
			"DD_TRACE_PARTIAL_FLUSH_MIN_SPANS": "1",
			"DD_TRACE_SAMPLE_RATE":             "1.0",
			"DD_TRACE_AGENT_URL":               "https://localhost:8126",
		}
		p := newTestProvider(newTestConfigSource(entries, telemetry.OriginEnvVar))
		assert.Equal(t, "string", p.GetString("DD_SERVICE", "value"))
		assert.Equal(t, true, p.GetBool("DD_TRACE_DEBUG", false))
		assert.Equal(t, 1, p.GetInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 0))
		assert.Equal(t, 1.0, p.GetFloat("DD_TRACE_SAMPLE_RATE", 0.0))
		assert.Equal(t, "https://localhost:8126", p.GetString("DD_TRACE_AGENT_URL", ""))
	})
	t.Run("GetBool accepts various boolean formats", func(t *testing.T) {
		testCases := []struct {
			value    string
			expected bool
		}{
			{"1", true},
			{"0", false},
			{"true", true},
			{"false", false},
			{"TRUE", true},
			{"FALSE", false},
			{"True", true},
			{"False", false},
			{"t", true},
			{"f", false},
			{"T", true},
			{"F", false},
		}

		for _, tc := range testCases {
			entries := map[string]string{"TEST_BOOL": tc.value}
			p := newTestProvider(newTestConfigSource(entries, telemetry.OriginEnvVar))
			result := p.GetBool("TEST_BOOL", !tc.expected)
			assert.Equal(t, tc.expected, result, "Expected %q to parse as %v", tc.value, tc.expected)
		}
	})
	t.Run("GetBool returns default for invalid values", func(t *testing.T) {
		invalidValues := []string{"yes", "no", "2", "-1", "invalid", ""}

		for _, val := range invalidValues {
			entries := map[string]string{"TEST_BOOL": val}
			p := newTestProvider(newTestConfigSource(entries, telemetry.OriginEnvVar))
			assert.Equal(t, true, p.GetBool("TEST_BOOL", true), "Expected default (true) for invalid value %q", val)
			assert.Equal(t, false, p.GetBool("TEST_BOOL", false), "Expected default (false) for invalid value %q", val)
		}
	})
}

func TestNew(t *testing.T) {
	t.Run("Settings only exist in EnvConfigSource", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "string")
		t.Setenv("DD_TRACE_DEBUG", "true")
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "1")
		t.Setenv("DD_TRACE_SAMPLE_RATE", "1.0")
		t.Setenv("DD_TRACE_AGENT_URL", "https://localhost:8126")

		p := New()

		assert.Equal(t, "string", p.GetString("DD_SERVICE", "value"))
		assert.Equal(t, true, p.GetBool("DD_TRACE_DEBUG", false))
		assert.Equal(t, 1, p.GetInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 0))
		assert.Equal(t, 1.0, p.GetFloat("DD_TRACE_SAMPLE_RATE", 0.0))
		assert.Equal(t, "https://localhost:8126", p.GetString("DD_TRACE_AGENT_URL", ""))

		assert.Equal(t, "value", p.GetString("DD_ENV", "value"))
	})

	t.Run("Settings only exist in OtelEnvConfigSource", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "string")
		t.Setenv("OTEL_LOG_LEVEL", "debug")
		t.Setenv("OTEL_TRACES_SAMPLER", "parentbased_always_on")
		t.Setenv("OTEL_TRACES_EXPORTER", "1.0")
		t.Setenv("OTEL_PROPAGATORS", "https://localhost:8126")
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "key1=value1,key2=value2")

		p := New()

		assert.Equal(t, "string", p.GetString("DD_SERVICE", "value"))
		assert.Equal(t, true, p.GetBool("DD_TRACE_DEBUG", false))
		assert.Equal(t, 1.0, p.GetFloat("DD_TRACE_SAMPLE_RATE", 0))
		assert.Equal(t, 1.0, p.GetFloat("DD_TRACE_SAMPLE_RATE", 0.0))
		assert.Equal(t, "key1:value1,key2:value2", p.GetString("DD_TAGS", "key:value"))
	})
	t.Run("Settings only exist in localDeclarativeConfigSource", func(t *testing.T) {
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

		tempLocalSource := newDeclarativeConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
		p := newTestProvider(
			newDeclarativeConfigSource(managedFilePath, telemetry.OriginManagedStableConfig),
			new(envConfigSource),
			new(otelEnvConfigSource),
			tempLocalSource,
		)

		assert.Equal(t, "local", p.GetString("DD_SERVICE", "value"))
		assert.Equal(t, true, p.GetBool("DD_TRACE_DEBUG", false))
		assert.Equal(t, 1, p.GetInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 0))
		assert.Equal(t, 1.0, p.GetFloat("DD_TRACE_SAMPLE_RATE", 0.0))
		assert.Equal(t, "https://localhost:8126", p.GetString("DD_TRACE_AGENT_URL", ""))

		assert.Equal(t, "value", p.GetString("DD_ENV", "value"))
	})

	t.Run("Settings only exist in managed declarativeConfigSource", func(t *testing.T) {
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

		tempManagedSource := newDeclarativeConfigSource(tempManagedPath, telemetry.OriginManagedStableConfig)
		p := newTestProvider(
			tempManagedSource,
			new(envConfigSource),
			new(otelEnvConfigSource),
			newDeclarativeConfigSource(localFilePath, telemetry.OriginLocalStableConfig),
		)

		assert.Equal(t, "managed", p.GetString("DD_SERVICE", "value"))
		assert.Equal(t, true, p.GetBool("DD_TRACE_DEBUG", false))
		assert.Equal(t, 1, p.GetInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 0))
		assert.Equal(t, 1.0, p.GetFloat("DD_TRACE_SAMPLE_RATE", 0.0))
		assert.Equal(t, "https://localhost:8126", p.GetString("DD_TRACE_AGENT_URL", ""))

		assert.Equal(t, "value", p.GetString("DD_ENV", "value"))
	})
	t.Run("Settings exist in all ConfigSources", func(t *testing.T) {
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

		t.Setenv("DD_SERVICE", "env_service")
		t.Setenv("DD_TRACE_DEBUG", "false")
		t.Setenv("DD_ENV", "env_environment")
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "false")
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "100")

		t.Setenv("OTEL_SERVICE_NAME", "otel_service")
		t.Setenv("OTEL_LOG_LEVEL", "debug")
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=otel_env,service.version=0.5.0")
		t.Setenv("OTEL_TRACES_SAMPLER", "traceidratio")
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.8")

		tempLocalPath := "local.yml"
		err := os.WriteFile(tempLocalPath, []byte(localYaml), 0644)
		assert.NoError(t, err)
		defer os.Remove(tempLocalPath)

		tempLocalSource := newDeclarativeConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)

		tempManagedPath := "managed.yml"
		err = os.WriteFile(tempManagedPath, []byte(managedYaml), 0644)
		assert.NoError(t, err)
		defer os.Remove(tempManagedPath)

		tempManagedSource := newDeclarativeConfigSource(tempManagedPath, telemetry.OriginManagedStableConfig)

		p := newTestProvider(
			tempManagedSource,
			new(envConfigSource),
			new(otelEnvConfigSource),
			tempLocalSource,
		)

		assert.Equal(t, "managed_service", p.GetString("DD_SERVICE", "default"),
			"DD_SERVICE: Managed should win over DD Env, OTEL, and Local")
		assert.Equal(t, true, p.GetBool("DD_TRACE_DEBUG", false),
			"DD_TRACE_DEBUG: Managed should win over DD Env, OTEL, and Local")

		assert.Equal(t, "1.0.0", p.GetString("DD_VERSION", "default"),
			"DD_VERSION: Managed should win over Local")
		assert.Equal(t, true, p.GetBool("DD_TRACE_PARTIAL_FLUSH_ENABLED", false),
			"DD_TRACE_PARTIAL_FLUSH_ENABLED: Managed should win over DD Env")

		assert.Equal(t, "env_environment", p.GetString("DD_ENV", "default"),
			"DD_ENV: DD Env should win over OTEL and Local")

		assert.Equal(t, 100, p.GetInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 0),
			"DD_TRACE_PARTIAL_FLUSH_MIN_SPANS: DD Env should win (only source)")

		assert.Equal(t, 0.8, p.GetFloat("DD_TRACE_SAMPLE_RATE", 0.0),
			"DD_TRACE_SAMPLE_RATE: OTEL should win over Local")

		assert.Equal(t, true, p.GetBool("DD_TRACE_STARTUP_LOGS", false),
			"DD_TRACE_STARTUP_LOGS: Local should win (only source)")

		assert.Equal(t, "default", p.GetString("DD_TRACE_AGENT_URL", "default"),
			"Unconfigured setting should return default")
	})
}

func TestProviderTelemetryRegistration(t *testing.T) {
	t.Run("env source reports telemetry for all getters", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		telemetryClient.On("RegisterAppConfigs", mock.Anything).Return().Maybe()
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
		p := newTestProvider(source)

		_ = p.GetString("DD_SERVICE", "default")
		_ = p.GetBool("DD_TRACE_DEBUG", false)
		_ = p.GetInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 0)
		_ = p.GetFloat("DD_TRACE_SAMPLE_RATE", 0.0)
		_ = p.GetString("DD_TRACE_AGENT_URL", "")
		_ = p.GetMap("DD_SERVICE_MAPPING", nil, internal.DDTagsDelimiter)
		_ = p.GetDuration("DD_TRACE_ABANDONED_SPAN_TIMEOUT", 0)

		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchConfig("DD_SERVICE", "service", telemetry.OriginEnvVar, telemetry.EmptyID)))
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchConfig("DD_TRACE_DEBUG", "true", telemetry.OriginEnvVar, telemetry.EmptyID)))
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchConfig("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "100", telemetry.OriginEnvVar, telemetry.EmptyID)))
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchConfig("DD_TRACE_SAMPLE_RATE", "0.5", telemetry.OriginEnvVar, telemetry.EmptyID)))
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchConfig("DD_TRACE_AGENT_URL", "http://localhost:8126", telemetry.OriginEnvVar, telemetry.EmptyID)))
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchConfig("DD_SERVICE_MAPPING", "old:new", telemetry.OriginEnvVar, telemetry.EmptyID)))
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchConfig("DD_TRACE_ABANDONED_SPAN_TIMEOUT", "10s", telemetry.OriginEnvVar, telemetry.EmptyID)))
	})

	t.Run("declarative source reports telemetry with ID", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		telemetryClient.On("RegisterAppConfigs", mock.Anything).Return().Maybe()
		defer telemetry.MockClient(telemetryClient)()

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

		decl := newDeclarativeConfigSource(temp, telemetry.OriginLocalStableConfig)
		p := newTestProvider(decl)

		_ = p.GetString("DD_SERVICE", "default")
		_ = p.GetBool("DD_TRACE_DEBUG", false)
		_ = p.GetInt("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 0)
		_ = p.GetFloat("DD_TRACE_SAMPLE_RATE", 0.0)
		_ = p.GetString("DD_TRACE_AGENT_URL", "")
		_ = p.GetMap("DD_SERVICE_MAPPING", nil, internal.DDTagsDelimiter)
		_ = p.GetDuration("DD_TRACE_ABANDONED_SPAN_TIMEOUT", 0)

		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchConfig("DD_SERVICE", "svc", telemetry.OriginLocalStableConfig, "123")))
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchConfig("DD_TRACE_DEBUG", "true", telemetry.OriginLocalStableConfig, "123")))
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchConfig("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "7", telemetry.OriginLocalStableConfig, "123")))
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchConfig("DD_TRACE_SAMPLE_RATE", "0.9", telemetry.OriginLocalStableConfig, "123")))
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchConfig("DD_TRACE_AGENT_URL", "http://127.0.0.1:8126", telemetry.OriginLocalStableConfig, "123")))
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchConfig("DD_SERVICE_MAPPING", "a:b", telemetry.OriginLocalStableConfig, "123")))
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchConfig("DD_TRACE_ABANDONED_SPAN_TIMEOUT", "2s", telemetry.OriginLocalStableConfig, "123")))
	})

	t.Run("source priority with config IDs and SeqID", func(t *testing.T) {
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

		capture := newSeqIDCapture()
		telemetryClient := new(telemetrytest.MockClient)
		telemetryClient.On("RegisterAppConfigs", mock.Anything).Return().Maybe()
		defer telemetry.MockClient(telemetryClient)()

		tempManagedSource := newDeclarativeConfigSource(tempManaged, telemetry.OriginManagedStableConfig)
		envSource := newTestConfigSource(map[string]string{"DD_SERVICE": "env-service"}, telemetry.OriginEnvVar)
		tempLocalSource := newDeclarativeConfigSource(tempLocal, telemetry.OriginLocalStableConfig)

		p := newTestProvider(tempManagedSource, envSource, tempLocalSource)

		result := p.GetString("DD_SERVICE", "default-service")
		assert.Equal(t, "managed-service", result, "Managed (highest priority) should win")

		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(capture.captureMatcher("DD_SERVICE", "managed-service", telemetry.OriginManagedStableConfig, "managed-123")))
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(capture.captureMatcher("DD_SERVICE", "env-service", telemetry.OriginEnvVar, telemetry.EmptyID)))
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(capture.captureMatcher("DD_SERVICE", "local-service", telemetry.OriginLocalStableConfig, "local-456")))
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchDefaultConfig("DD_SERVICE", "default-service")))

		managedSeq := capture.get("DD_SERVICE", "managed-service", telemetry.OriginManagedStableConfig)
		envSeq := capture.get("DD_SERVICE", "env-service", telemetry.OriginEnvVar)
		localSeq := capture.get("DD_SERVICE", "local-service", telemetry.OriginLocalStableConfig)
		assert.Greater(t, managedSeq, envSeq, "Managed (highest priority) should have higher SeqID than Env")
		assert.Greater(t, envSeq, localSeq, "Env should have higher SeqID than Local (lowest priority)")

		env := p.GetString("DD_ENV", "default-env")
		assert.Equal(t, "local-env", env)

		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchConfig("DD_ENV", "local-env", telemetry.OriginLocalStableConfig, "local-456")))
		telemetryClient.AssertCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchDefaultConfig("DD_ENV", "default-env")))
	})

	t.Run("still reports defaults via telemetry when key missing or invalid", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)

		strKey, strDef := "DD_SERVICE", "default_service"
		boolKey, boolDef := "DD_TRACE_DEBUG", true
		intKey, intDef := "DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", 7
		floatKey, floatDef := "DD_TRACE_SAMPLE_RATE", 0.25
		durKey, durDef := "DD_TRACE_ABANDONED_SPAN_TIMEOUT", 42*time.Second
		mapKey, mapDef := "DD_SERVICE_MAPPING", map[string]string{"a": "b"}

		telemetryClient.On("RegisterAppConfigs", mock.MatchedBy(matchDefaultConfig(strKey, strDef))).Return()
		telemetryClient.On("RegisterAppConfigs", mock.MatchedBy(matchDefaultConfig(boolKey, boolDef))).Return()
		telemetryClient.On("RegisterAppConfigs", mock.MatchedBy(matchDefaultConfig(intKey, intDef))).Return()
		telemetryClient.On("RegisterAppConfigs", mock.MatchedBy(matchDefaultConfig(floatKey, floatDef))).Return()
		telemetryClient.On("RegisterAppConfigs", mock.MatchedBy(matchDefaultConfig(durKey, durDef))).Return()
		telemetryClient.On("RegisterAppConfigs", mock.MatchedBy(matchDefaultConfig(mapKey, mapDef))).Return()
		defer telemetry.MockClient(telemetryClient)()

		p := newTestProvider(newTestConfigSource(map[string]string{}, telemetry.OriginEnvVar))

		assert.Equal(t, strDef, p.GetString(strKey, strDef))
		assert.Equal(t, boolDef, p.GetBool(boolKey, boolDef))
		assert.Equal(t, intDef, p.GetInt(intKey, intDef))
		assert.Equal(t, floatDef, p.GetFloat(floatKey, floatDef))
		assert.Equal(t, durDef, p.GetDuration(durKey, durDef))
		assert.Equal(t, mapDef, p.GetMap(mapKey, mapDef, internal.DDTagsDelimiter))

		telemetryClient.AssertExpectations(t)
	})
}
