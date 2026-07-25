// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package provider

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/config/schema"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

// newTestProvider creates a Provider with custom sources for testing.
func newTestProvider(sources ...configSource) *Provider {
	return &Provider{sources: sources}
}

func TestNewSourceOrderMatchesSchemaOrdinals(t *testing.T) {
	p := New()

	require.Len(t, p.sources, int(schema.SourceOrdinalDefault))
	assert.Equal(t, telemetry.OriginManagedStableConfig, p.sources[schema.SourceOrdinalManagedStable].origin())
	_, ok := p.sources[schema.SourceOrdinalEnvironment].(*envConfigSource)
	require.True(t, ok)
	_, ok = p.sources[schema.SourceOrdinalOTelEnvironment].(*otelEnvConfigSource)
	require.True(t, ok)
	assert.Equal(t, telemetry.OriginLocalStableConfig, p.sources[schema.SourceOrdinalLocalStable].origin())
	assert.Equal(t, schema.SourceOrdinalDefault, schema.SourceOrdinalMax)
}

func TestNewEnvironmentPreservesOrdinalsWithoutDeclarativeSources(t *testing.T) {
	p := NewEnvironment()

	require.Len(t, p.sources, int(schema.SourceOrdinalDefault))
	assert.IsType(t, omittedConfigSource{}, p.sources[schema.SourceOrdinalManagedStable])
	assert.IsType(t, new(envConfigSource), p.sources[schema.SourceOrdinalEnvironment])
	assert.IsType(t, new(otelEnvConfigSource), p.sources[schema.SourceOrdinalOTelEnvironment])
	assert.IsType(t, omittedConfigSource{}, p.sources[schema.SourceOrdinalLocalStable])
	for _, source := range p.sources {
		_, declarative := source.(*declarativeConfigSource)
		assert.False(t, declarative,
			"environment-only construction must not stat or parse stable configuration files")
	}
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

func (s *testConfigSource) lookup(key string) (string, bool) {
	raw, present := s.entries[key]
	return raw, present
}

func (s *testConfigSource) origin() telemetry.Origin {
	return s.originValue
}

type resolveTestSource struct {
	raw         string
	present     bool
	originValue telemetry.Origin
	configID    string
	environment bool
}

type diagnosticTestSource struct {
	*resolveTestSource
	events []ConfigEvent
}

func (s *diagnosticTestSource) lookupWithEvents(string) (string, bool, bool, error, []ConfigEvent) {
	return s.raw, s.present, s.present, nil, s.events
}

func (s *resolveTestSource) get(string) string {
	return s.raw
}

func (s *resolveTestSource) lookup(string) (string, bool) {
	return s.raw, s.present
}

func (s *resolveTestSource) origin() telemetry.Origin {
	return s.originValue
}

func (s *resolveTestSource) getID() string {
	return s.configID
}

func (s *resolveTestSource) environmentSource() bool {
	return s.environment
}

type countingLookupSource struct {
	raw         string
	present     bool
	originValue telemetry.Origin
	configID    string
	lookups     int
}

func (s *countingLookupSource) lookup(string) (string, bool) {
	s.lookups++
	return s.raw, s.present
}

func (s *countingLookupSource) origin() telemetry.Origin {
	return s.originValue
}

func (s *countingLookupSource) getID() string {
	return s.configID
}

func (s *countingLookupSource) environmentSource() bool {
	return s.originValue == telemetry.OriginEnvVar
}

func eventKinds(events []ConfigEvent) []EventKind {
	kinds := make([]EventKind, len(events))
	for i, event := range events {
		kinds[i] = event.Kind
	}
	return kinds
}

func testDefinition(key string, policy schema.SourcePolicy) schema.RawDefinition {
	return schema.RawDefinition{Key: key, Sources: policy, Telemetry: schema.TelemetryReport}
}

func parseTestInt(raw string) (int, error) {
	return strconv.Atoi(raw)
}

func parseTestString(raw string) (string, error) {
	return raw, nil
}

func TestResolveKeepsAllSourceAttempts(t *testing.T) {
	p := newTestProvider(
		&resolveTestSource{raw: "invalid", present: true, originValue: telemetry.OriginManagedStableConfig, configID: "managed-id"},
		&resolveTestSource{raw: "7", present: true, originValue: telemetry.OriginEnvVar, environment: true},
		&resolveTestSource{present: false, originValue: telemetry.OriginEnvVar},
		&resolveTestSource{raw: "12", present: true, originValue: telemetry.OriginLocalStableConfig, configID: "local-id"},
	)

	got := Resolve(p, testDefinition("DD_VALUE", schema.SourceStable), 3, parseTestInt)

	require.Equal(t, 7, got.Winner.Value)
	require.Equal(t, telemetry.OriginEnvVar, got.Winner.Origin)
	require.Empty(t, got.Winner.ConfigID)
	require.False(t, got.Winner.DefaultUsed)
	require.Equal(t, []schema.SourceAttempt{
		{Raw: "12", Present: true, Valid: true, Origin: telemetry.OriginLocalStableConfig, ConfigID: "local-id"},
		{Present: false, Valid: false, Origin: telemetry.OriginEnvVar},
		{Raw: "7", Present: true, Valid: true, Origin: telemetry.OriginEnvVar},
		{Raw: "invalid", Present: true, Valid: false, Origin: telemetry.OriginManagedStableConfig, ConfigID: "managed-id", Err: strconv.ErrSyntax},
	}, normalizeAttemptErrors(got.Attempts))
	require.Error(t, got.Attempts[3].Err)
}

func TestResolveAppSecEnablementFallsThroughInvalidManagedToValidEnvironment(t *testing.T) {
	p := newTestProvider(
		&resolveTestSource{raw: "managed-invalid", present: true, originValue: telemetry.OriginManagedStableConfig, configID: "managed-id"},
		&resolveTestSource{raw: "true", present: true, originValue: telemetry.OriginEnvVar, environment: true},
	)
	def := schema.RawDefinition{
		Key: "DD_APPSEC_ENABLED", Sources: schema.SourceStable, Telemetry: schema.TelemetryReport,
	}
	binding := schema.ConsumerBinding{
		ID: "appsec.enablement", Consumer: "internal/appsec/config.IsEnabledByEnvironment",
		Keys: []string{"DD_APPSEC_ENABLED"}, Sampling: schema.SampleProductStart,
	}

	got, events := ResolveWithBinding(p, def, binding, false, strconv.ParseBool)

	require.True(t, got.Winner.Value)
	require.Equal(t, telemetry.OriginEnvVar, got.Winner.Origin)
	require.False(t, got.Winner.DefaultUsed)
	require.Len(t, got.Attempts, 2)
	require.True(t, got.Attempts[0].Present)
	require.True(t, got.Attempts[0].Valid)
	require.Equal(t, telemetry.OriginEnvVar, got.Attempts[0].Origin)
	require.True(t, got.Attempts[1].Present)
	require.False(t, got.Attempts[1].Valid)
	require.Equal(t, telemetry.OriginManagedStableConfig, got.Attempts[1].Origin)
	require.Error(t, got.Attempts[1].Err)
	require.Len(t, events, 3)
	require.Equal(t, telemetry.OriginEnvVar, events[0].Origin)
	require.True(t, events[0].Valid)
	require.Equal(t, telemetry.OriginManagedStableConfig, events[1].Origin)
	require.False(t, events[1].Valid)
	require.Error(t, events[1].Err)
	require.Equal(t, telemetry.OriginDefault, events[2].Origin)
}

func TestResolveSourcePolicies(t *testing.T) {
	p := newTestProvider(
		&resolveTestSource{raw: "managed", present: true, originValue: telemetry.OriginManagedStableConfig},
		&resolveTestSource{raw: "environment", present: true, originValue: telemetry.OriginEnvVar, environment: true},
		&resolveTestSource{raw: "otel", present: true, originValue: telemetry.OriginEnvVar},
		&resolveTestSource{raw: "local", present: true, originValue: telemetry.OriginLocalStableConfig},
	)

	stable := Resolve(p, testDefinition("DD_VALUE", schema.SourceStable), "default", parseTestString)
	require.Equal(t, "managed", stable.Winner.Value)
	require.Equal(t, []string{"local", "otel", "environment", "managed"}, attemptRawValues(stable.Attempts))

	environment := Resolve(p, testDefinition("DD_VALUE", schema.SourceEnvironment), "default", parseTestString)
	require.Equal(t, "environment", environment.Winner.Value)
	require.Len(t, environment.Attempts, 1)
	require.Equal(t, "environment", environment.Attempts[0].Raw)
}

func TestResolveWithBindingNarrowsStableDefinitionToEnvironment(t *testing.T) {
	p := newTestProvider(
		&resolveTestSource{raw: "managed", present: true, originValue: telemetry.OriginManagedStableConfig, configID: "managed-id"},
		&resolveTestSource{raw: "environment", present: true, originValue: telemetry.OriginEnvVar, environment: true},
		&resolveTestSource{raw: "local", present: true, originValue: telemetry.OriginLocalStableConfig, configID: "local-id"},
	)
	def := testDefinition("DD_SERVICE", schema.SourceStable)
	stableBinding := schema.ConsumerBinding{
		ID: "tracer.service", Consumer: "tracer",
		Keys: []string{"DD_SERVICE"}, Sampling: schema.SampleTracerConstruction,
	}
	environmentBinding := schema.ConsumerBinding{
		ID: "naming.service", Consumer: "naming",
		Keys: []string{"DD_SERVICE"}, Sampling: schema.SamplePackageInit,
		EnvironmentOnly: true,
	}

	stable, stableEvents := ResolveWithBinding(p, def, stableBinding, "default", parseTestString)
	require.Equal(t, "managed", stable.Winner.Value)
	require.Equal(t, []string{"local", "environment", "managed"}, attemptRawValues(stable.Attempts))
	require.Len(t, stableEvents, 4)

	environment, environmentEvents := ResolveWithBinding(p, def, environmentBinding, "default", parseTestString)
	require.Equal(t, "environment", environment.Winner.Value)
	require.Equal(t, telemetry.OriginEnvVar, environment.Winner.Origin)
	require.Equal(t, []string{"environment"}, attemptRawValues(environment.Attempts))
	require.Len(t, environmentEvents, 2)
	require.Equal(t, "environment", environmentEvents[0].Value)
	require.Equal(t, telemetry.OriginEnvVar, environmentEvents[0].Origin)
	require.Empty(t, environmentEvents[0].ConfigID)
	require.Equal(t, schema.SourceOrdinalEnvironment, environmentEvents[0].SourceOrdinal)
}

func TestResolveWithBindingEnvironmentOnlyIncludesOTelEnvironment(t *testing.T) {
	oldService, servicePresent := os.LookupEnv("DD_SERVICE")
	require.NoError(t, os.Unsetenv("DD_SERVICE"))
	t.Cleanup(func() {
		if servicePresent {
			require.NoError(t, os.Setenv("DD_SERVICE", oldService))
		} else {
			require.NoError(t, os.Unsetenv("DD_SERVICE"))
		}
	})
	t.Setenv("OTEL_SERVICE_NAME", "otel-service")
	p := newTestProvider(
		&resolveTestSource{raw: "managed", present: true, originValue: telemetry.OriginManagedStableConfig, configID: "managed-id"},
		&envConfigSource{},
		&otelEnvConfigSource{},
		&resolveTestSource{raw: "local", present: true, originValue: telemetry.OriginLocalStableConfig, configID: "local-id"},
	)
	binding := schema.ConsumerBinding{
		ID: "tracer.service", Consumer: "tracer",
		Keys: []string{"DD_SERVICE"}, Sampling: schema.SampleTracerConstruction,
		EnvironmentOnly: true,
	}

	resolved, _ := ResolveWithBinding(
		p,
		testDefinition("DD_SERVICE", schema.SourceStable),
		binding,
		"default",
		parseTestString,
	)
	require.Equal(t, "otel-service", resolved.Winner.Value)
	require.Equal(t, telemetry.OriginEnvVar, resolved.Winner.Origin)
	require.Len(t, resolved.Attempts, 2)
	require.Equal(t, "otel-service", resolved.Attempts[0].Raw)
	require.False(t, resolved.Attempts[1].Present)
}

func TestResolveTracerOTelCompatibilityPreservesLegacyDebugPrecedence(t *testing.T) {
	def := schema.RawDefinition{
		Key:       "DD_TRACE_DEBUG",
		Sources:   schema.SourceStable,
		Telemetry: schema.TelemetryReport,
	}
	binding := schema.ConsumerBinding{
		ID:       "tracer.otel.debug",
		Consumer: "tracer",
		Keys:     []string{"DD_TRACE_DEBUG", "OTEL_LOG_LEVEL"},
		Sampling: schema.SampleConstructor,
	}

	t.Run("managed stable short-circuits every lower source", func(t *testing.T) {
		logger := new(log.RecordLogger)
		defer log.UseLogger(logger)()
		t.Setenv("OTEL_LOG_LEVEL", "invalid")
		managed := &countingLookupSource{
			raw: "true", present: true,
			originValue: telemetry.OriginManagedStableConfig, configID: "managed-id",
		}
		environment := &countingLookupSource{
			raw: "false", present: true, originValue: telemetry.OriginEnvVar,
		}
		local := &countingLookupSource{
			raw: "false", present: true,
			originValue: telemetry.OriginLocalStableConfig, configID: "local-id",
		}
		p := newTestProvider(managed, environment, new(otelEnvConfigSource), local)

		resolved, events := ResolveTracerOTelCompatibility(p, def, binding)

		require.Equal(t, "true", resolved.Winner.Value)
		require.Equal(t, telemetry.OriginManagedStableConfig, resolved.Winner.Origin)
		require.Equal(t, "managed-id", resolved.Winner.ConfigID)
		require.Equal(t, 1, managed.lookups)
		require.Zero(t, environment.lookups)
		require.Zero(t, local.lookups)
		require.Empty(t, logger.Logs())
		require.Len(t, resolved.Attempts, 1)
		require.Len(t, events, 1)
		require.Equal(t, EventConfiguration, events[0].Kind)
		require.Equal(t, schema.SourceOrdinalManagedStable, events[0].SourceOrdinal)
	})

	t.Run("Datadog environment short-circuits remapping and local stable", func(t *testing.T) {
		logger := new(log.RecordLogger)
		defer log.UseLogger(logger)()
		rec := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(rec)()
		t.Setenv("OTEL_LOG_LEVEL", "invalid")
		managed := &countingLookupSource{originValue: telemetry.OriginManagedStableConfig}
		environment := &countingLookupSource{
			raw: "false", present: true, originValue: telemetry.OriginEnvVar,
		}
		local := &countingLookupSource{
			raw: "true", present: true,
			originValue: telemetry.OriginLocalStableConfig, configID: "local-id",
		}
		p := newTestProvider(managed, environment, new(otelEnvConfigSource), local)

		resolved, events := ResolveTracerOTelCompatibility(p, def, binding)

		require.Equal(t, "false", resolved.Winner.Value)
		require.Equal(t, telemetry.OriginEnvVar, resolved.Winner.Origin)
		require.Zero(t, local.lookups)
		require.Len(t, logger.Logs(), 1)
		require.Contains(t, logger.Logs()[0], "using DD_TRACE_DEBUG=false")
		require.Len(t, resolved.Events, 1)
		require.Equal(t, EventOTelEnvHiding, resolved.Events[0].Kind)
		require.Equal(t, []EventKind{EventConfiguration, EventOTelEnvHiding}, eventKinds(events))
		require.Zero(t, rec.Count(
			telemetry.NamespaceTracers,
			"otel.env.hiding",
			[]string{"config_datadog:dd_trace_debug", "config_opentelemetry:otel_log_level"},
		).Get(), "provider resolution must stage diagnostics for the Reporter")
	})

	t.Run("invalid OTel falls through to local stable", func(t *testing.T) {
		logger := new(log.RecordLogger)
		defer log.UseLogger(logger)()
		t.Setenv("OTEL_LOG_LEVEL", "info")
		managed := &countingLookupSource{originValue: telemetry.OriginManagedStableConfig}
		environment := &countingLookupSource{originValue: telemetry.OriginEnvVar}
		local := &countingLookupSource{
			raw: "true", present: true,
			originValue: telemetry.OriginLocalStableConfig, configID: "local-id",
		}
		p := newTestProvider(managed, environment, new(otelEnvConfigSource), local)

		resolved, events := ResolveTracerOTelCompatibility(p, def, binding)

		require.Equal(t, "true", resolved.Winner.Value)
		require.Equal(t, telemetry.OriginLocalStableConfig, resolved.Winner.Origin)
		require.Equal(t, "local-id", resolved.Winner.ConfigID)
		require.Equal(t, 1, local.lookups)
		require.Len(t, logger.Logs(), 1)
		require.Contains(t, logger.Logs()[0], "OTEL_LOG_LEVEL=info")
		require.Equal(t, []EventKind{EventOTelEnvInvalid, EventConfiguration}, eventKinds(events))
		require.Equal(t, schema.SourceOrdinalOTelEnvironment, events[0].SourceOrdinal)
		require.Equal(t, schema.SourceOrdinalLocalStable, events[1].SourceOrdinal)
	})

	t.Run("explicit empty OTel silently falls through to local stable", func(t *testing.T) {
		logger := new(log.RecordLogger)
		defer log.UseLogger(logger)()
		t.Setenv("OTEL_LOG_LEVEL", "")
		p := newTestProvider(
			&countingLookupSource{originValue: telemetry.OriginManagedStableConfig},
			&countingLookupSource{originValue: telemetry.OriginEnvVar},
			new(otelEnvConfigSource),
			&countingLookupSource{
				raw: "true", present: true,
				originValue: telemetry.OriginLocalStableConfig, configID: "local-id",
			},
		)

		resolved, events := ResolveTracerOTelCompatibility(p, def, binding)

		require.Equal(t, "true", resolved.Winner.Value)
		require.Equal(t, telemetry.OriginLocalStableConfig, resolved.Winner.Origin)
		require.Empty(t, logger.Logs())
		require.Equal(t, []EventKind{EventConfiguration}, eventKinds(events))
		require.Empty(t, resolved.Events)
	})
}

func TestResolveTracerOTelCompatibilityPreservesOmitPolicy(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=checkout")
	def := schema.RawDefinition{
		Key:       "DD_TAGS",
		Sources:   schema.SourceStable,
		Telemetry: schema.TelemetryOmit,
	}
	binding := schema.ConsumerBinding{
		ID:              "tracer.otel.resource-attributes",
		Consumer:        "tracer",
		Keys:            []string{"DD_TAGS", "OTEL_RESOURCE_ATTRIBUTES"},
		Sampling:        schema.SampleConstructor,
		EnvironmentOnly: true,
	}
	p := newTestProvider(
		&countingLookupSource{originValue: telemetry.OriginManagedStableConfig},
		&countingLookupSource{originValue: telemetry.OriginEnvVar},
		new(otelEnvConfigSource),
		&countingLookupSource{originValue: telemetry.OriginLocalStableConfig},
	)

	resolved, events := ResolveTracerOTelCompatibility(p, def, binding)

	require.Equal(t, "service:checkout", resolved.Winner.Value)
	require.Len(t, events, 1)
	require.Equal(t, schema.TelemetryOmit, events[0].Policy)
	require.True(t, events[0].ReportValue)
}

func TestResolveWithBindingScrubsSensitiveEventValuesAtCreation(t *testing.T) {
	binding := schema.ConsumerBinding{
		ID: "sensitive", Consumer: "test",
		Keys:     []string{"DD_API_KEY", "DD_GIT_REPOSITORY_URL"},
		Sampling: schema.SampleConstructor,
	}

	apiKey, apiEvents := ResolveWithBinding(
		newTestProvider(&resolveTestSource{
			raw: "secret-api-key", present: true, originValue: telemetry.OriginEnvVar,
		}),
		schema.RawDefinition{
			Key: "DD_API_KEY", Sources: schema.SourceStable, Telemetry: schema.TelemetryOmit,
		},
		binding,
		"",
		parseTestString,
	)
	require.Equal(t, "secret-api-key", apiKey.Winner.Value)
	require.NotContains(t, fmt.Sprint(apiEvents), "secret-api-key")

	repository, repositoryEvents := ResolveWithBinding(
		newTestProvider(&resolveTestSource{
			raw: "https://user:password@example.com/repo.git", present: true, originValue: telemetry.OriginEnvVar,
		}),
		schema.RawDefinition{
			Key: "DD_GIT_REPOSITORY_URL", Sources: schema.SourceStable, Telemetry: schema.TelemetrySanitizeURL,
		},
		binding,
		"",
		parseTestString,
	)
	require.Equal(t, "https://user:password@example.com/repo.git", repository.Winner.Value)
	require.NotContains(t, fmt.Sprint(repositoryEvents), "password")
	require.Contains(t, fmt.Sprint(repositoryEvents), "https://example.com/repo.git")
}

func TestResolveWithBindingScrubsSensitiveParserErrors(t *testing.T) {
	const secret = "sensitive-parser-sentinel"
	binding := schema.ConsumerBinding{
		ID: "sensitive-errors", Consumer: "test",
		Keys: []string{"DD_API_KEY", "DD_GIT_REPOSITORY_URL"}, Sampling: schema.SampleConstructor,
	}

	for name, test := range map[string]struct {
		def schema.RawDefinition
		raw string
	}{
		"omit": {
			def: schema.RawDefinition{Key: "DD_API_KEY", Sources: schema.SourceStable, Telemetry: schema.TelemetryOmit},
			raw: secret,
		},
		"sanitize URL": {
			def: schema.RawDefinition{Key: "DD_GIT_REPOSITORY_URL", Sources: schema.SourceStable, Telemetry: schema.TelemetrySanitizeURL},
			raw: "https://user:" + secret + "@example.com/repo.git",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, events := ResolveWithBinding(
				newTestProvider(&resolveTestSource{
					raw: test.raw, present: true, originValue: telemetry.OriginEnvVar,
				}),
				test.def,
				binding,
				"",
				func(raw string) (string, error) {
					return "", fmt.Errorf("rejected %s", raw)
				},
			)

			require.NotContains(t, fmt.Sprint(events), secret)
			require.Error(t, events[0].Err)
		})
	}
}

func TestResolveWithBindingScrubsSensitiveSourceDiagnostics(t *testing.T) {
	const secret = "sensitive-diagnostic-sentinel"
	def := schema.RawDefinition{
		Key: "DD_GIT_REPOSITORY_URL", Sources: schema.SourceStable, Telemetry: schema.TelemetrySanitizeURL,
	}
	binding := schema.ConsumerBinding{
		ID: "sensitive-diagnostic", Consumer: "test",
		Keys: []string{def.Key}, Sampling: schema.SampleConstructor,
	}
	source := &diagnosticTestSource{
		resolveTestSource: &resolveTestSource{
			raw:     "https://user:" + secret + "@example.com/repo.git",
			present: true, originValue: telemetry.OriginEnvVar,
		},
		events: []ConfigEvent{{
			Kind: EventOTelEnvInvalid, Name: def.Key, OTelName: "OTEL_RESOURCE_ATTRIBUTES",
			Value:               "https://user:" + secret + "@example.com/repo.git",
			Err:                 fmt.Errorf("diagnostic rejected %s", secret),
			CompatibilityReport: true,
		}},
	}

	resolved, events := ResolveWithBinding(newTestProvider(source), def, binding, "", parseTestString)

	require.NotContains(t, fmt.Sprint(events), secret)
	require.NotContains(t, fmt.Sprint(resolved.Events), secret)
	require.Error(t, events[0].Err)
	require.Error(t, resolved.Events[0].Err)
}

func TestResolveExplicitEmpty(t *testing.T) {
	p := newTestProvider(&resolveTestSource{
		raw: "", present: true, originValue: telemetry.OriginManagedStableConfig, configID: "managed-id",
	})

	stringResult := Resolve(p, testDefinition("DD_VALUE", schema.SourceStable), "default", parseTestString)
	require.Equal(t, "", stringResult.Winner.Value)
	require.Equal(t, telemetry.OriginManagedStableConfig, stringResult.Winner.Origin)
	require.Equal(t, "managed-id", stringResult.Winner.ConfigID)
	require.Equal(t, schema.SourceAttempt{
		Raw: "", Present: true, Valid: true, Origin: telemetry.OriginManagedStableConfig, ConfigID: "managed-id",
	}, stringResult.Attempts[0])

	intResult := Resolve(p, testDefinition("DD_VALUE", schema.SourceStable), 42, parseTestInt)
	require.Equal(t, 42, intResult.Winner.Value)
	require.Equal(t, telemetry.OriginDefault, intResult.Winner.Origin)
	require.True(t, intResult.Winner.DefaultUsed)
	require.True(t, intResult.Attempts[0].Present)
	require.False(t, intResult.Attempts[0].Valid)
	require.Error(t, intResult.Attempts[0].Err)
}

func TestCompatibilityStringGettersSkipExplicitEmptySources(t *testing.T) {
	tests := []struct {
		name     string
		sources  []configSource
		want     string
		origin   telemetry.Origin
		validate func(string) bool
	}{
		{
			name: "managed empty falls through to environment",
			sources: []configSource{
				&resolveTestSource{raw: "", present: true, originValue: telemetry.OriginManagedStableConfig},
				&resolveTestSource{raw: "environment", present: true, originValue: telemetry.OriginEnvVar},
				&resolveTestSource{raw: "local", present: true, originValue: telemetry.OriginLocalStableConfig},
			},
			want: "environment", origin: telemetry.OriginEnvVar,
		},
		{
			name: "environment empty falls through to local",
			sources: []configSource{
				&resolveTestSource{raw: "", present: true, originValue: telemetry.OriginEnvVar},
				&resolveTestSource{raw: "local", present: true, originValue: telemetry.OriginLocalStableConfig},
			},
			want: "local", origin: telemetry.OriginLocalStableConfig,
		},
		{
			name: "local empty falls through to default",
			sources: []configSource{
				&resolveTestSource{raw: "", present: true, originValue: telemetry.OriginLocalStableConfig},
			},
			want: "default", origin: telemetry.OriginDefault,
		},
		{
			name: "validator still applies after empty fallthrough",
			sources: []configSource{
				&resolveTestSource{raw: "", present: true, originValue: telemetry.OriginManagedStableConfig},
				&resolveTestSource{raw: "valid", present: true, originValue: telemetry.OriginLocalStableConfig},
			},
			want: "valid", origin: telemetry.OriginLocalStableConfig,
			validate: func(value string) bool {
				return value == "valid"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestProvider(tt.sources...)
			got, origin := p.GetStringWithOrigin("DD_VALUE", "default")
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.origin, origin)
			require.Equal(t, tt.want, p.GetStringWithValidator("DD_VALUE", "default", tt.validate))
		})
	}
}

func TestResolveAllInvalidUsesDefault(t *testing.T) {
	p := newTestProvider(
		&resolveTestSource{raw: "managed-invalid", present: true, originValue: telemetry.OriginManagedStableConfig},
		&resolveTestSource{raw: "env-invalid", present: true, originValue: telemetry.OriginEnvVar, environment: true},
	)

	got := Resolve(p, testDefinition("DD_VALUE", schema.SourceStable), 42, parseTestInt)

	require.Equal(t, 42, got.Winner.Value)
	require.Equal(t, telemetry.OriginDefault, got.Winner.Origin)
	require.True(t, got.Winner.DefaultUsed)
	require.Len(t, got.Attempts, 2)
	require.Error(t, got.Attempts[0].Err)
	require.Error(t, got.Attempts[1].Err)
}

func TestResolveReturnsDefensiveAttemptCopies(t *testing.T) {
	p := newTestProvider(&resolveTestSource{raw: "value", present: true, originValue: telemetry.OriginEnvVar, environment: true})
	def := testDefinition("DD_VALUE", schema.SourceStable)

	first := Resolve(p, def, "default", parseTestString)
	first.Attempts[0].Raw = "mutated"
	second := Resolve(p, def, "default", parseTestString)

	require.Equal(t, "value", second.Attempts[0].Raw)
}

func TestResolveRetainsProviderDiagnosticsDefensively(t *testing.T) {
	t.Setenv("OTEL_LOG_LEVEL", "invalid")
	p := newTestProvider(new(otelEnvConfigSource))
	def := testDefinition("DD_TRACE_DEBUG", schema.SourceStable)
	binding := schema.ConsumerBinding{
		ID: "test.debug", Consumer: "test", Keys: []string{"DD_TRACE_DEBUG"}, Sampling: schema.SampleConstructor,
	}

	plain := Resolve(p, def, false, strconv.ParseBool)
	require.Len(t, plain.Events, 1)
	require.Equal(t, EventOTelEnvInvalid, plain.Events[0].Kind)
	require.Error(t, plain.Events[0].Err)

	resolved, events := ResolveWithBinding(p, def, binding, false, strconv.ParseBool)
	require.Len(t, resolved.Events, 1)
	require.NotEmpty(t, events)
	require.Equal(t, EventOTelEnvInvalid, resolved.Events[0].Kind)
	resolvedDiagnostic := eventIndex(t, resolved.Events, EventOTelEnvInvalid)
	returnedDiagnostic := eventIndex(t, events, EventOTelEnvInvalid)
	require.Equal(t, "test.debug", events[returnedDiagnostic].BindingID)

	resolved.Events[resolvedDiagnostic].Name = "mutated-result"
	require.Equal(t, "DD_TRACE_DEBUG", events[returnedDiagnostic].Name)
	events[returnedDiagnostic].Name = "mutated-return"
	require.Equal(t, "mutated-result", resolved.Events[resolvedDiagnostic].Name)
	next := Resolve(p, def, false, strconv.ParseBool)
	require.Equal(t, "DD_TRACE_DEBUG", next.Events[0].Name)
}

func TestResolveSnapshotsMutableDefaults(t *testing.T) {
	p := newTestProvider(&resolveTestSource{present: false, originValue: telemetry.OriginEnvVar})
	binding := schema.ConsumerBinding{
		ID: "test.mutable", Consumer: "test", Keys: []string{"DD_VALUE"}, Sampling: schema.SampleConstructor,
	}

	t.Run("map", func(t *testing.T) {
		def := map[string]string{"key": "original"}
		got, events := ResolveWithBinding(p, testDefinition("DD_VALUE", schema.SourceStable), binding, def,
			func(raw string) (map[string]string, error) {
				return parseMapString(raw, internal.DDTagsDelimiter), nil
			})
		defaultEvent := events[len(events)-1]
		eventMap := defaultEvent.Value.(map[string]string)

		def["key"] = "input-mutated"
		require.Equal(t, "original", got.Winner.Value["key"])
		require.Equal(t, "original", eventMap["key"])

		got.Winner.Value["key"] = "winner-mutated"
		require.Equal(t, "original", eventMap["key"])
		eventMap["key"] = "event-mutated"
		require.Equal(t, "winner-mutated", got.Winner.Value["key"])
	})

	t.Run("slice", func(t *testing.T) {
		def := []string{"original"}
		got, events := ResolveWithBinding(p, testDefinition("DD_VALUE", schema.SourceStable), binding, def,
			func(raw string) ([]string, error) {
				return []string{raw}, nil
			})
		defaultEvent := events[len(events)-1]
		eventSlice := defaultEvent.Value.([]string)

		def[0] = "input-mutated"
		require.Equal(t, "original", got.Winner.Value[0])
		require.Equal(t, "original", eventSlice[0])

		got.Winner.Value[0] = "winner-mutated"
		require.Equal(t, "original", eventSlice[0])
	})
}

func TestResolveSnapshotsNilAnyDefault(t *testing.T) {
	p := newTestProvider(&resolveTestSource{present: false, originValue: telemetry.OriginEnvVar})
	binding := schema.ConsumerBinding{
		ID: "test.any", Consumer: "test", Keys: []string{"DD_VALUE"}, Sampling: schema.SampleConstructor,
	}

	got, events := ResolveWithBinding[any](p, testDefinition("DD_VALUE", schema.SourceStable), binding, nil,
		func(raw string) (any, error) {
			return raw, nil
		})

	require.Nil(t, got.Winner.Value)
	require.NotEmpty(t, events)
	require.Nil(t, events[len(events)-1].Value)
}

func TestResolveSnapshotsSliceNilness(t *testing.T) {
	p := newTestProvider(&resolveTestSource{present: false, originValue: telemetry.OriginEnvVar})
	binding := schema.ConsumerBinding{
		ID: "test.slice", Consumer: "test", Keys: []string{"DD_VALUE"}, Sampling: schema.SampleConstructor,
	}

	t.Run("nil string slice", func(t *testing.T) {
		var def []string
		got, events := ResolveWithBinding(p, testDefinition("DD_VALUE", schema.SourceStable), binding, def,
			func(raw string) ([]string, error) {
				return []string{raw}, nil
			})

		require.Nil(t, got.Winner.Value)
		require.Nil(t, events[len(events)-1].Value.([]string))
	})

	t.Run("non-nil empty string slice", func(t *testing.T) {
		def := make([]string, 0)
		got, events := ResolveWithBinding(p, testDefinition("DD_VALUE", schema.SourceStable), binding, def,
			func(raw string) ([]string, error) {
				return []string{raw}, nil
			})

		require.NotNil(t, got.Winner.Value)
		require.Empty(t, got.Winner.Value)
		require.NotNil(t, events[len(events)-1].Value.([]string))
		require.Empty(t, events[len(events)-1].Value.([]string))
	})

	t.Run("nil byte slice", func(t *testing.T) {
		var def []byte
		got, events := ResolveWithBinding(p, testDefinition("DD_VALUE", schema.SourceStable), binding, def,
			func(raw string) ([]byte, error) {
				return []byte(raw), nil
			})

		require.Nil(t, got.Winner.Value)
		require.Nil(t, events[len(events)-1].Value.([]byte))
	})

	t.Run("non-nil empty byte slice", func(t *testing.T) {
		def := make([]byte, 0)
		got, events := ResolveWithBinding(p, testDefinition("DD_VALUE", schema.SourceStable), binding, def,
			func(raw string) ([]byte, error) {
				return []byte(raw), nil
			})

		require.NotNil(t, got.Winner.Value)
		require.Empty(t, got.Winner.Value)
		require.NotNil(t, events[len(events)-1].Value.([]byte))
		require.Empty(t, events[len(events)-1].Value.([]byte))
	})

	t.Run("byte slice copies are independent", func(t *testing.T) {
		def := []byte("original")
		got, events := ResolveWithBinding(p, testDefinition("DD_VALUE", schema.SourceStable), binding, def,
			func(raw string) ([]byte, error) {
				return []byte(raw), nil
			})
		eventValue := events[len(events)-1].Value.([]byte)

		def[0] = 'i'
		require.Equal(t, []byte("original"), got.Winner.Value)
		require.Equal(t, []byte("original"), eventValue)
		got.Winner.Value[0] = 'w'
		require.Equal(t, []byte("original"), eventValue)
	})
}

func TestResolveWithBindingDecoratesLocalEvents(t *testing.T) {
	telemetryClient := new(telemetrytest.MockClient)
	telemetryClient.On("RegisterAppConfigs", mock.Anything).Return().Maybe()
	defer telemetry.MockClient(telemetryClient)()

	p := newTestProvider(&resolveTestSource{
		raw: "7", present: true, originValue: telemetry.OriginManagedStableConfig, configID: "config-id",
	})
	def := testDefinition("DD_VALUE", schema.SourceStable)
	binding := schema.ConsumerBinding{
		ID: "test.value", Consumer: "test", Keys: []string{"DD_VALUE"}, Sampling: schema.SampleConstructor,
	}

	got, events := ResolveWithBinding(p, def, binding, 3, parseTestInt)

	require.Equal(t, 7, got.Winner.Value)
	require.Len(t, events, 2)
	require.Equal(t, ConfigEvent{
		Kind: EventConfiguration, BindingID: "test.value", Name: "DD_VALUE", Value: "7",
		Present: true, Valid: true, Origin: telemetry.OriginManagedStableConfig, ConfigID: "config-id",
		SourceOrdinal: 0, Policy: schema.TelemetryReport, Cadence: ReportOncePerGeneration, ReportValue: true,
	}, events[0])
	require.Equal(t, ConfigEvent{
		Kind: EventConfiguration, BindingID: "test.value", Name: "DD_VALUE", Value: 3,
		Present: true, Valid: true, Origin: telemetry.OriginDefault,
		SourceOrdinal: 1, Policy: schema.TelemetryReport, Cadence: ReportOncePerGeneration, ReportValue: true,
	}, events[1])
	telemetryClient.AssertNotCalled(t, "RegisterAppConfigs", mock.Anything)
}

func TestResolveWithBindingAssignsDistinctSourceOrdinals(t *testing.T) {
	p := &Provider{sources: []LookupSource{
		&resolveTestSource{raw: "dd", present: true, originValue: telemetry.OriginEnvVar},
		&resolveTestSource{raw: "otel", present: true, originValue: telemetry.OriginEnvVar},
	}}
	binding := schema.ConsumerBinding{
		ID: "tracer.DD_SERVICE", Consumer: "tracer",
		Keys: []string{"DD_SERVICE"}, Sampling: schema.SampleTracerConstruction,
	}

	_, events := ResolveWithBinding(p, testDefinition("DD_SERVICE", schema.SourceStable), binding, "default", parseTestString)

	require.Len(t, events, 3)
	require.NotEqual(t, events[0].SourceOrdinal, events[1].SourceOrdinal)
	require.NotEqual(t, events[1].SourceOrdinal, events[2].SourceOrdinal)
}

func findEvent(t *testing.T, events []ConfigEvent, kind EventKind) ConfigEvent {
	t.Helper()
	return events[eventIndex(t, events, kind)]
}

func eventIndex(t *testing.T, events []ConfigEvent, kind EventKind) int {
	t.Helper()
	for i, event := range events {
		if event.Kind == kind {
			return i
		}
	}
	require.FailNow(t, "event not found", "kind: %v", kind)
	return -1
}

func normalizeAttemptErrors(attempts []schema.SourceAttempt) []schema.SourceAttempt {
	got := append([]schema.SourceAttempt(nil), attempts...)
	for i := range got {
		if got[i].Err != nil && errors.Is(got[i].Err, strconv.ErrSyntax) {
			got[i].Err = strconv.ErrSyntax
		}
	}
	return got
}

func attemptRawValues(attempts []schema.SourceAttempt) []string {
	values := make([]string, len(attempts))
	for i := range attempts {
		values[i] = attempts[i].Raw
	}
	return values
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
	t.Run("GetBoolWithOrigin returns OriginDefault when unset", func(t *testing.T) {
		p := newTestProvider(newTestConfigSource(nil, telemetry.OriginEnvVar))
		v, origin := p.GetBoolWithOrigin("TEST_BOOL", false)
		assert.Equal(t, false, v)
		assert.Equal(t, telemetry.OriginDefault, origin)

		v, origin = p.GetBoolWithOrigin("TEST_BOOL", true)
		assert.Equal(t, true, v)
		assert.Equal(t, telemetry.OriginDefault, origin)
	})
	t.Run("GetBoolWithOrigin returns source origin when set", func(t *testing.T) {
		for _, origin := range []telemetry.Origin{
			telemetry.OriginEnvVar,
			telemetry.OriginLocalStableConfig,
			telemetry.OriginManagedStableConfig,
		} {
			p := newTestProvider(newTestConfigSource(map[string]string{"TEST_BOOL": "true"}, origin))
			v, gotOrigin := p.GetBoolWithOrigin("TEST_BOOL", false)
			assert.Equal(t, true, v)
			assert.Equal(t, origin, gotOrigin)
		}
	})
	t.Run("GetBoolWithOrigin returns OriginDefault for invalid value", func(t *testing.T) {
		p := newTestProvider(newTestConfigSource(map[string]string{"TEST_BOOL": "notabool"}, telemetry.OriginEnvVar))
		v, origin := p.GetBoolWithOrigin("TEST_BOOL", true)
		assert.Equal(t, true, v)
		assert.Equal(t, telemetry.OriginDefault, origin)
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

	t.Run("sensitive keys are not reported to telemetry", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		telemetryClient.On("RegisterAppConfigs", mock.Anything).Return().Maybe()
		defer telemetry.MockClient(telemetryClient)()

		source := newTestConfigSource(map[string]string{
			"DD_APP_KEY": "secret-app-key",
			"DD_API_KEY": "secret-api-key",
		}, telemetry.OriginEnvVar)
		p := newTestProvider(source)

		_ = p.GetString("DD_APP_KEY", "")
		_ = p.GetString("DD_API_KEY", "")

		telemetryClient.AssertNotCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchConfig("DD_APP_KEY", "secret-app-key", telemetry.OriginEnvVar, telemetry.EmptyID)))
		telemetryClient.AssertNotCalled(t, "RegisterAppConfigs", mock.MatchedBy(matchConfig("DD_API_KEY", "secret-api-key", telemetry.OriginEnvVar, telemetry.EmptyID)))
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
