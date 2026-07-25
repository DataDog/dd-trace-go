// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/config/bootstrap"
	"github.com/DataDog/dd-trace-go/v2/internal/config/schema"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

const (
	openFeatureProviderEnabledKey  = "DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED"
	openFeatureSpanEnrichmentKey   = "DD_EXPERIMENTAL_FLAGGING_PROVIDER_SPAN_ENRICHMENT_ENABLED"
	openFeatureEvaluationCountsKey = "DD_FLAGGING_EVALUATION_COUNTS_ENABLED"
)

func setOpenFeatureEnvironment(t *testing.T, values map[string]string) {
	t.Helper()
	for _, key := range []string{
		"DD_SERVICE",
		"DD_ENV",
		"DD_VERSION",
		openFeatureProviderEnabledKey,
		openFeatureSpanEnrichmentKey,
		openFeatureEvaluationCountsKey,
	} {
		t.Setenv(key, values[key])
	}
}

func environmentOpenFeatureEvents(events []ConfigEvent) map[string]ConfigEvent {
	byName := make(map[string]ConfigEvent)
	for _, event := range events {
		if event.Origin == telemetry.OriginEnvVar &&
			event.SourceOrdinal == schema.SourceOrdinalEnvironment {
			byName[event.Name] = event
		}
	}
	return byName
}

func unsetOpenFeatureEnvironment(t *testing.T, key string) {
	t.Helper()
	old, present := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	t.Cleanup(func() {
		if present {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestOpenFeatureSnapshotResolvesAllFieldsAndRawEvents(t *testing.T) {
	setOpenFeatureEnvironment(t, map[string]string{
		"DD_SERVICE":                   "checkout",
		"DD_ENV":                       "staging",
		"DD_VERSION":                   "1.2.3",
		openFeatureProviderEnabledKey:  "true",
		openFeatureSpanEnrichmentKey:   "1",
		openFeatureEvaluationCountsKey: "FALSE",
	})

	snapshot, events := resolveOpenFeatureSnapshot(openFeaturePublicResolution)

	require.Equal(t, OpenFeatureSnapshot{
		Service:                     "checkout",
		Environment:                 "staging",
		Version:                     "1.2.3",
		ProviderEnabled:             true,
		SpanEnrichmentEnabled:       true,
		FlagEvaluationCountsEnabled: false,
	}, snapshot)
	environment := environmentOpenFeatureEvents(events)
	require.Len(t, environment, 6)
	require.Equal(t, "checkout", environment["DD_SERVICE"].Value)
	require.Equal(t, "staging", environment["DD_ENV"].Value)
	require.Equal(t, "1.2.3", environment["DD_VERSION"].Value)
	require.Equal(t, "true", environment[openFeatureProviderEnabledKey].Value)
	require.Equal(t, "1", environment[openFeatureSpanEnrichmentKey].Value)
	require.Equal(t, "FALSE", environment[openFeatureEvaluationCountsKey].Value)
	for _, event := range environment {
		require.Equal(t, "openfeature.provider", event.BindingID)
		require.Equal(t, TelemetryReport, event.Policy)
		require.Equal(t, ReportOncePerGeneration, event.Cadence)
		require.True(t, event.Present)
		require.True(t, event.Valid)
	}
}

func TestOpenFeaturePublicDisabledShortCircuitsAfterEnablement(t *testing.T) {
	setOpenFeatureEnvironment(t, map[string]string{
		"DD_SERVICE":                   "must-not-be-read",
		openFeatureProviderEnabledKey:  "false",
		openFeatureSpanEnrichmentKey:   "not-a-bool",
		openFeatureEvaluationCountsKey: "not-a-bool",
	})

	snapshot, events := resolveOpenFeatureSnapshot(openFeaturePublicResolution)

	require.False(t, snapshot.ProviderEnabled)
	require.Equal(t, []string{
		openFeatureProviderEnabledKey,
		openFeatureProviderEnabledKey,
	}, eventNames(events))
	require.Len(t, environmentOpenFeatureEvents(events), 1)
}

func TestOpenFeatureCompatibilityResolversRespectSamplingBoundaries(t *testing.T) {
	setOpenFeatureEnvironment(t, map[string]string{
		"DD_SERVICE":                   "service",
		"DD_ENV":                       "env",
		"DD_VERSION":                   "version",
		openFeatureProviderEnabledKey:  "not-a-bool",
		openFeatureSpanEnrichmentKey:   "true",
		openFeatureEvaluationCountsKey: "false",
	})

	providerSnapshot, providerEvents := resolveOpenFeatureSnapshot(openFeatureProviderResolution)
	require.False(t, providerSnapshot.ProviderEnabled)
	require.True(t, providerSnapshot.SpanEnrichmentEnabled)
	require.False(t, providerSnapshot.FlagEvaluationCountsEnabled)
	require.ElementsMatch(t, []string{
		"DD_SERVICE", "DD_SERVICE",
		"DD_ENV", "DD_ENV",
		"DD_VERSION", "DD_VERSION",
		openFeatureSpanEnrichmentKey, openFeatureSpanEnrichmentKey,
		openFeatureEvaluationCountsKey, openFeatureEvaluationCountsKey,
	}, eventNames(providerEvents))

	contextSnapshot, contextEvents := resolveOpenFeatureSnapshot(openFeatureContextResolution)
	require.Equal(t, "service", contextSnapshot.Service)
	require.Equal(t, "env", contextSnapshot.Environment)
	require.Equal(t, "version", contextSnapshot.Version)
	require.ElementsMatch(t, []string{
		"DD_SERVICE", "DD_SERVICE",
		"DD_ENV", "DD_ENV",
		"DD_VERSION", "DD_VERSION",
	}, eventNames(contextEvents))
}

func TestOpenFeatureServiceFallbacksKeepRawTelemetry(t *testing.T) {
	oldService := globalconfig.ServiceName()
	t.Cleanup(func() { globalconfig.SetServiceName(oldService) })

	t.Run("global service", func(t *testing.T) {
		globalconfig.SetServiceName("global-service")
		setOpenFeatureEnvironment(t, map[string]string{})

		snapshot, events := resolveOpenFeatureSnapshot(openFeatureContextResolution)

		require.Equal(t, "global-service", snapshot.Service)
		event := environmentOpenFeatureEvents(events)["DD_SERVICE"]
		require.True(t, event.Present)
		require.Equal(t, "", event.Value)
		require.True(t, event.Valid)
	})

	t.Run("executable", func(t *testing.T) {
		globalconfig.SetServiceName("")
		setOpenFeatureEnvironment(t, map[string]string{})
		executable, err := os.Executable()
		require.NoError(t, err)

		snapshot, _ := resolveOpenFeatureSnapshot(openFeatureContextResolution)

		require.Equal(t, executable, snapshot.Service)
	})
}

func TestOpenFeatureBooleanParsingMatchesStrconv(t *testing.T) {
	for _, raw := range []string{"1", "t", "T", "TRUE", "true", "True"} {
		t.Run("true/"+raw, func(t *testing.T) {
			setOpenFeatureEnvironment(t, map[string]string{
				openFeatureProviderEnabledKey:  "true",
				openFeatureSpanEnrichmentKey:   raw,
				openFeatureEvaluationCountsKey: "false",
			})
			require.True(t, ResolveOpenFeatureSnapshot().SpanEnrichmentEnabled)
		})
	}
	for _, raw := range []string{"0", "f", "F", "FALSE", "false", "False"} {
		t.Run("false/"+raw, func(t *testing.T) {
			setOpenFeatureEnvironment(t, map[string]string{
				openFeatureProviderEnabledKey:  "true",
				openFeatureSpanEnrichmentKey:   raw,
				openFeatureEvaluationCountsKey: "true",
			})
			require.False(t, ResolveOpenFeatureSnapshot().SpanEnrichmentEnabled)
		})
	}
}

func TestOpenFeatureBooleanDefaultsAndWarnings(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		setOpenFeatureEnvironment(t, map[string]string{
			openFeatureProviderEnabledKey: "true",
		})
		unsetOpenFeatureEnvironment(t, openFeatureSpanEnrichmentKey)
		unsetOpenFeatureEnvironment(t, openFeatureEvaluationCountsKey)

		snapshot := ResolveOpenFeatureSnapshot()

		require.False(t, snapshot.SpanEnrichmentEnabled)
		require.True(t, snapshot.FlagEvaluationCountsEnabled)
	})

	for _, raw := range []string{"", "not-a-bool"} {
		t.Run(raw, func(t *testing.T) {
			logger := new(log.RecordLogger)
			defer log.UseLogger(logger)()
			setOpenFeatureEnvironment(t, map[string]string{
				openFeatureProviderEnabledKey:  "true",
				openFeatureSpanEnrichmentKey:   raw,
				openFeatureEvaluationCountsKey: raw,
			})

			snapshot := ResolveOpenFeatureSnapshot()

			require.False(t, snapshot.SpanEnrichmentEnabled)
			require.True(t, snapshot.FlagEvaluationCountsEnabled)
			logs := strings.Join(logger.Logs(), "\n")
			require.Contains(t, logs, "Non-boolean value for env var "+openFeatureSpanEnrichmentKey+". Parse failed with error:")
			require.Contains(t, logs, "Non-boolean value for env var "+openFeatureEvaluationCountsKey+". Parse failed with error:")
		})
	}

	t.Run("provider default", func(t *testing.T) {
		logger := new(log.RecordLogger)
		defer log.UseLogger(logger)()
		setOpenFeatureEnvironment(t, map[string]string{
			openFeatureProviderEnabledKey: "not-a-bool",
		})

		snapshot := ResolveOpenFeatureSnapshot()

		require.False(t, snapshot.ProviderEnabled)
		require.Contains(t, strings.Join(logger.Logs(), "\n"),
			"Non-boolean value for env var "+openFeatureProviderEnabledKey+". Parse failed with error:")
	})
}

func TestOpenFeatureReportsRawServiceInsteadOfFallback(t *testing.T) {
	bootstrap.ResetForTesting()
	instrumentationReporter.ResetForTesting()
	t.Cleanup(bootstrap.ResetForTesting)
	t.Cleanup(instrumentationReporter.ResetForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	oldService := globalconfig.ServiceName()
	globalconfig.SetServiceName("fallback-service")
	t.Cleanup(func() { globalconfig.SetServiceName(oldService) })
	setOpenFeatureEnvironment(t, map[string]string{})
	client := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(client))

	snapshot := ResolveOpenFeatureContextSnapshot()

	require.Equal(t, "fallback-service", snapshot.Service)
	var environmentEvents []telemetry.Configuration
	for _, configuration := range client.Configuration {
		if configuration.Name == "DD_SERVICE" && configuration.Origin == telemetry.OriginEnvVar {
			environmentEvents = append(environmentEvents, configuration)
		}
	}
	require.Len(t, environmentEvents, 1)
	require.Equal(t, "", environmentEvents[0].Value)
}

func TestOpenFeatureSnapshotResamplesEachConstructor(t *testing.T) {
	setOpenFeatureEnvironment(t, map[string]string{
		"DD_SERVICE":                   "first",
		openFeatureProviderEnabledKey:  "true",
		openFeatureSpanEnrichmentKey:   "false",
		openFeatureEvaluationCountsKey: "true",
	})
	first := ResolveOpenFeatureSnapshot()

	t.Setenv("DD_SERVICE", "second")
	t.Setenv(openFeatureSpanEnrichmentKey, "true")
	t.Setenv(openFeatureEvaluationCountsKey, "false")
	second := ResolveOpenFeatureSnapshot()

	require.Equal(t, "first", first.Service)
	require.False(t, first.SpanEnrichmentEnabled)
	require.True(t, first.FlagEvaluationCountsEnabled)
	require.Equal(t, "second", second.Service)
	require.True(t, second.SpanEnrichmentEnabled)
	require.False(t, second.FlagEvaluationCountsEnabled)
}

func TestOpenFeatureRegistryBinding(t *testing.T) {
	raw, bindings := RegisteredDefinitions()
	rawByKey := make(map[string]RawDefinition, len(raw))
	for _, definition := range raw {
		rawByKey[definition.Key] = definition
	}
	for _, key := range []string{openFeatureSpanEnrichmentKey, openFeatureEvaluationCountsKey} {
		require.Equal(t, RawDefinition{
			Key:       key,
			Sources:   SourceEnvironment,
			Telemetry: TelemetryReport,
		}, rawByKey[key])
	}

	var binding ConsumerBinding
	for _, candidate := range bindings {
		if candidate.ID == "openfeature.provider" {
			binding = candidate
			break
		}
	}
	require.Equal(t, ConsumerBinding{
		ID:       "openfeature.provider",
		Consumer: "the OpenFeature provider constructor",
		Keys: []string{
			"DD_SERVICE",
			"DD_ENV",
			"DD_VERSION",
			openFeatureProviderEnabledKey,
			openFeatureSpanEnrichmentKey,
			openFeatureEvaluationCountsKey,
		},
		Sampling:        SampleConstructor,
		EnvironmentOnly: true,
	}, binding)
}

func eventNames(events []ConfigEvent) []string {
	names := make([]string, len(events))
	for i, event := range events {
		names[i] = event.Name
	}
	return names
}
