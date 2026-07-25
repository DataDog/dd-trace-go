// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
	"github.com/DataDog/dd-trace-go/v2/internal/config/schema"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestHTTPTraceSnapshotPreservesPresenceAndParsing(t *testing.T) {
	keys := []string{
		"DD_TRACE_HTTP_URL_QUERY_STRING_DISABLED",
		"DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP",
		"DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST",
		"DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_CLIENT",
		"DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_SERVER",
		"DD_TRACE_BAGGAGE_TAG_KEYS",
		"DD_TRACE_CLIENT_IP_ENABLED",
		"DD_TRACE_HTTP_SERVER_ERROR_STATUSES",
		"DD_TRACE_INFERRED_PROXY_SERVICES_ENABLED",
		"DD_GOOGLE_CLOUD_PUBSUB_PROPAGATION_AS_SPAN_LINKS",
		"DD_TRACE_RESOURCE_RENAMING_ENABLED",
		"DD_TRACE_RESOURCE_RENAMING_ALWAYS_SIMPLIFIED_ENDPOINT",
	}
	for _, key := range keys {
		unsetForTest(t, key)
	}

	t.Run("absent", func(t *testing.T) {
		got := HTTPTraceSnapshot()
		require.False(t, got.QueryStringDisabled)
		require.False(t, got.QueryStringRegexpPresent)
		require.False(t, got.QueryStringAllowlistPresent)
		require.False(t, got.ClientQueryStringAllowlistPresent)
		require.False(t, got.ServerQueryStringAllowlistPresent)
		require.False(t, got.BaggageTagKeysPresent)
		require.False(t, got.TraceClientIPEnabled)
		require.Empty(t, got.ServerErrorStatuses)
		require.False(t, got.InferredProxyServicesEnabled)
		require.False(t, got.PubsubPropagationAsSpanLinks)
		require.False(t, got.ResourceRenamingEnabledPresent)
		require.False(t, got.ResourceRenamingAlwaysSimplifiedEndpoint)
	})

	t.Run("explicit empty", func(t *testing.T) {
		for _, key := range keys {
			t.Setenv(key, "")
		}
		got := HTTPTraceSnapshot()
		require.False(t, got.QueryStringDisabled)
		require.True(t, got.QueryStringRegexpPresent)
		require.Empty(t, got.QueryStringRegexp)
		require.True(t, got.QueryStringAllowlistPresent)
		require.Empty(t, got.QueryStringAllowlist)
		require.True(t, got.ClientQueryStringAllowlistPresent)
		require.Empty(t, got.ClientQueryStringAllowlist)
		require.True(t, got.ServerQueryStringAllowlistPresent)
		require.Empty(t, got.ServerQueryStringAllowlist)
		require.True(t, got.BaggageTagKeysPresent)
		require.Empty(t, got.BaggageTagKeys)
		require.False(t, got.TraceClientIPEnabled)
		require.Empty(t, got.ServerErrorStatuses)
		require.False(t, got.InferredProxyServicesEnabled)
		require.False(t, got.PubsubPropagationAsSpanLinks)
		require.False(t, got.ResourceRenamingEnabledPresent)
		require.False(t, got.ResourceRenamingAlwaysSimplifiedEndpoint)
	})

	t.Run("invalid boolean", func(t *testing.T) {
		t.Setenv("DD_TRACE_HTTP_URL_QUERY_STRING_DISABLED", "invalid")
		require.False(t, HTTPTraceSnapshot().QueryStringDisabled)
	})

	t.Run("valid", func(t *testing.T) {
		t.Setenv("DD_TRACE_HTTP_URL_QUERY_STRING_DISABLED", "true")
		t.Setenv("DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP", "token=[^&]+")
		t.Setenv("DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST", "global")
		t.Setenv("DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_CLIENT", "client")
		t.Setenv("DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_SERVER", "server")
		t.Setenv("DD_TRACE_BAGGAGE_TAG_KEYS", "user.id,account.id")
		t.Setenv("DD_TRACE_CLIENT_IP_ENABLED", "true")
		t.Setenv("DD_TRACE_HTTP_SERVER_ERROR_STATUSES", "400-499")
		t.Setenv("DD_TRACE_INFERRED_PROXY_SERVICES_ENABLED", "true")
		t.Setenv("DD_GOOGLE_CLOUD_PUBSUB_PROPAGATION_AS_SPAN_LINKS", "true")
		t.Setenv("DD_TRACE_RESOURCE_RENAMING_ENABLED", "true")
		t.Setenv("DD_TRACE_RESOURCE_RENAMING_ALWAYS_SIMPLIFIED_ENDPOINT", "true")
		got := HTTPTraceSnapshot()
		require.True(t, got.QueryStringDisabled)
		require.Equal(t, "token=[^&]+", got.QueryStringRegexp)
		require.Equal(t, "global", got.QueryStringAllowlist)
		require.Equal(t, "client", got.ClientQueryStringAllowlist)
		require.Equal(t, "server", got.ServerQueryStringAllowlist)
		require.Equal(t, "user.id,account.id", got.BaggageTagKeys)
		require.True(t, got.TraceClientIPEnabled)
		require.Equal(t, "400-499", got.ServerErrorStatuses)
		require.True(t, got.InferredProxyServicesEnabled)
		require.True(t, got.PubsubPropagationAsSpanLinks)
		require.True(t, got.ResourceRenamingEnabled)
		require.True(t, got.ResourceRenamingEnabledPresent)
		require.True(t, got.ResourceRenamingAlwaysSimplifiedEndpoint)
	})
}

func TestHTTPTraceSnapshotConstructsOneEnvironmentProvider(t *testing.T) {
	originalEnvironment := newEnvironmentProvider
	originalStable := newStableProvider
	t.Cleanup(func() {
		newEnvironmentProvider = originalEnvironment
		newStableProvider = originalStable
	})
	environmentConstructions := 0
	stableConstructions := 0
	newEnvironmentProvider = func() *provider.Provider {
		environmentConstructions++
		return provider.NewEnvironment()
	}
	newStableProvider = func() *provider.Provider {
		stableConstructions++
		return provider.New()
	}

	_ = HTTPTraceSnapshot()
	require.Equal(t, 1, environmentConstructions)
	require.Zero(t, stableConstructions,
		"an environment-only snapshot must not stat or parse stable configuration files")
}

func TestInstrumentationRawDefinitionPolicies(t *testing.T) {
	raw, _ := RegisteredDefinitions()
	policies := make(map[string]TelemetryPolicy, len(raw))
	for _, def := range raw {
		policies[def.Key] = def.Telemetry
	}
	for _, key := range []string{
		"DD_LLMOBS_ML_APP",
		"DD_LLMOBS_PROJECT_NAME",
		"DD_TRACE_BAGGAGE_TAG_KEYS",
		"DD_TRACE_GRAPHQL_ERROR_EXTENSIONS",
		"DD_TRACE_HTTP_SERVER_ERROR_STATUSES",
		"DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST",
		"DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_CLIENT",
		"DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_SERVER",
		"DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP",
		"OTEL_RESOURCE_ATTRIBUTES",
	} {
		require.Equal(t, TelemetryOmit, policies[key], key)
	}

	t.Setenv("DD_TRACE_BAGGAGE_TAG_KEYS", "private-user-key")
	_, events := resolveString("DD_TRACE_BAGGAGE_TAG_KEYS", httpTraceBinding)
	require.NotEmpty(t, events)
	for _, event := range events {
		require.Equal(t, TelemetryOmit, event.Policy)
	}
}

func TestRuntimeBindingsMatchRegisteredMetadata(t *testing.T) {
	raw, bindings := RegisteredDefinitions()
	rawByKey := make(map[string]RawDefinition, len(raw))
	for _, def := range raw {
		rawByKey[def.Key] = def
	}
	registered := make(map[string]ConsumerBinding, len(bindings))
	for _, binding := range bindings {
		registered[binding.ID] = binding
	}

	require.Equal(t, httpTraceBinding, registered[httpTraceBinding.ID])
	require.Equal(t, propagationBinding, registered[propagationBinding.ID])
	require.Equal(t, tracerSourceHostnameBinding, registered[tracerSourceHostnameBinding.ID])
	require.Equal(t, SourceEnvironment, rawByKey["DD_TRACE_SOURCE_HOSTNAME"].Sources)
	require.Equal(t, TelemetryReport, rawByKey["DD_TRACE_SOURCE_HOSTNAME"].Telemetry)
	for name, binding := range tracerOTelBindings {
		if name == "propagationStyle" {
			continue
		}
		require.Equal(t, binding, registered[binding.ID], name)
	}
}

func TestTracerSourceHostnameUsesEnvironmentProvider(t *testing.T) {
	originalEnvironment := newEnvironmentProvider
	originalStable := newStableProvider
	t.Cleanup(func() {
		newEnvironmentProvider = originalEnvironment
		newStableProvider = originalStable
	})
	environmentConstructions := 0
	stableConstructions := 0
	newEnvironmentProvider = func() *provider.Provider {
		environmentConstructions++
		return provider.NewEnvironment()
	}
	newStableProvider = func() *provider.Provider {
		stableConstructions++
		return provider.New()
	}
	t.Setenv("DD_TRACE_SOURCE_HOSTNAME", "environment-hostname")

	resolved, _ := resolveString("DD_TRACE_SOURCE_HOSTNAME", tracerSourceHostnameBinding)
	require.Equal(t, "environment-hostname", resolved.Winner.Value)
	require.Equal(t, 1, environmentConstructions)
	require.Zero(t, stableConstructions,
		"the environment-only hostname must not stat or parse stable configuration files")
}

func TestMigratedBooleanPreservesInvalidValueWarning(t *testing.T) {
	logger := new(log.RecordLogger)
	defer log.UseLogger(logger)()
	t.Setenv("DD_API_SECURITY_ENDPOINT_COLLECTION_ENABLED", "invalid")

	require.True(t, APISecurityEndpointCollectionEnabled())
	require.Contains(t, strings.Join(logger.Logs(), "\n"),
		"Non-boolean value for env var DD_API_SECURITY_ENDPOINT_COLLECTION_ENABLED")
}

func TestTracerOTelCompatibilityUsesProviderMappings(t *testing.T) {
	pairs := [][2]string{
		{"DD_SERVICE", "OTEL_SERVICE_NAME"},
		{"DD_TRACE_DEBUG", "OTEL_LOG_LEVEL"},
		{"DD_TRACE_ENABLED", "OTEL_TRACES_EXPORTER"},
		{"DD_TRACE_SAMPLE_RATE", "OTEL_TRACES_SAMPLER"},
		{"DD_TRACE_PROPAGATION_STYLE", "OTEL_PROPAGATORS"},
		{"DD_TAGS", "OTEL_RESOURCE_ATTRIBUTES"},
	}
	for _, pair := range pairs {
		unsetForTest(t, pair[0])
		unsetForTest(t, pair[1])
	}
	unsetForTest(t, "OTEL_TRACES_SAMPLER_ARG")

	tests := []struct {
		name, otelKey, otelValue, arg, want string
	}{
		{name: "service", otelKey: "OTEL_SERVICE_NAME", otelValue: "checkout", want: "checkout"},
		{name: "debugMode", otelKey: "OTEL_LOG_LEVEL", otelValue: "debug", want: "true"},
		{name: "enabled", otelKey: "OTEL_TRACES_EXPORTER", otelValue: "none", want: "false"},
		{name: "sampleRate", otelKey: "OTEL_TRACES_SAMPLER", otelValue: "traceidratio", arg: "0.25", want: "0.25"},
		{name: "propagationStyle", otelKey: "OTEL_PROPAGATORS", otelValue: "tracecontext,b3", want: "tracecontext,b3 single header"},
		{name: "resourceAttributes", otelKey: "OTEL_RESOURCE_ATTRIBUTES", otelValue: "custom=one,service.name=checkout,deployment.environment=prod,service.version=1.2.3", want: "version:1.2.3,env:prod,service:checkout,custom:one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.otelKey, tt.otelValue)
			if tt.arg != "" {
				t.Setenv("OTEL_TRACES_SAMPLER_ARG", tt.arg)
			}
			require.Equal(t, tt.want, TracerOTelDDValue(tt.name))
		})
	}

	t.Run("Datadog wins over OTel", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "datadog")
		t.Setenv("OTEL_SERVICE_NAME", "otel")
		require.Equal(t, "datadog", TracerOTelDDValue("service"))
	})
	t.Run("explicit empty Datadog falls through to OTel", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "")
		t.Setenv("OTEL_SERVICE_NAME", "otel")
		require.Equal(t, "otel", TracerOTelDDValue("service"))
	})
	t.Run("reserved tags are ordered before truncation", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "custom0=0,custom1=1,custom2=2,custom3=3,custom4=4,custom5=5,custom6=6,custom7=7,custom8=8,service.name=checkout,deployment.environment=prod,service.version=1.2.3")
		require.Equal(t,
			"version:1.2.3,env:prod,service:checkout,custom0:0,custom1:1,custom2:2,custom3:3,custom4:4,custom5:5,custom6:6",
			TracerOTelDDValue("resourceAttributes"),
		)
	})
}

func TestTracerOTelCompatibilityPreservesLegacyMappingEdges(t *testing.T) {
	type mapping struct {
		name, ddKey, ddValue, otelKey, invalidOTel string
	}
	mappings := []mapping{
		{name: "service", ddKey: "DD_SERVICE", ddValue: "checkout", otelKey: "OTEL_SERVICE_NAME", invalidOTel: "other"},
		{name: "debugMode", ddKey: "DD_TRACE_DEBUG", ddValue: "true", otelKey: "OTEL_LOG_LEVEL", invalidOTel: "info"},
		{name: "enabled", ddKey: "DD_TRACE_ENABLED", ddValue: "true", otelKey: "OTEL_TRACES_EXPORTER", invalidOTel: "jaeger"},
		{name: "sampleRate", ddKey: "DD_TRACE_SAMPLE_RATE", ddValue: "0.25", otelKey: "OTEL_TRACES_SAMPLER", invalidOTel: "unknown"},
		{name: "propagationStyle", ddKey: "DD_TRACE_PROPAGATION_STYLE", ddValue: "datadog", otelKey: "OTEL_PROPAGATORS", invalidOTel: "unknown"},
		{name: "resourceAttributes", ddKey: "DD_TAGS", ddValue: "team:checkout", otelKey: "OTEL_RESOURCE_ATTRIBUTES", invalidOTel: "broken"},
	}
	for _, item := range mappings {
		unsetForTest(t, item.ddKey)
		unsetForTest(t, item.otelKey)
	}
	unsetForTest(t, "OTEL_TRACES_SAMPLER_ARG")

	t.Run("OTEL_TRACES_EXPORTER otlp remains unsupported", func(t *testing.T) {
		logger := new(log.RecordLogger)
		defer log.UseLogger(logger)()
		rec := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(rec)()
		t.Setenv("OTEL_TRACES_EXPORTER", "otlp")

		require.Empty(t, TracerOTelDDValue("enabled"))
		require.Contains(t, strings.Join(logger.Logs(), "\n"), "OTEL_METRICS_EXPORTER=otlp")
		require.NotZero(t, rec.Count(
			telemetry.NamespaceTracers,
			"otel.env.invalid",
			[]string{"config_datadog:dd_trace_enabled", "config_opentelemetry:otel_traces_exporter"},
		).Get())
	})

	for _, item := range mappings {
		t.Run(item.name+" explicit empty OTel", func(t *testing.T) {
			logger := new(log.RecordLogger)
			defer log.UseLogger(logger)()
			rec := new(telemetrytest.RecordClient)
			defer telemetry.MockClient(rec)()
			t.Setenv(item.otelKey, "")

			require.Empty(t, TracerOTelDDValue(item.name))
			require.Empty(t, logger.Logs())
			require.Zero(t, rec.Count(
				telemetry.NamespaceTracers,
				"otel.env.invalid",
				[]string{"config_datadog:" + strings.ToLower(item.ddKey), "config_opentelemetry:" + strings.ToLower(item.otelKey)},
			).Get())
		})

		t.Run(item.name+" DD wins without validating OTel", func(t *testing.T) {
			logger := new(log.RecordLogger)
			defer log.UseLogger(logger)()
			rec := new(telemetrytest.RecordClient)
			defer telemetry.MockClient(rec)()
			t.Setenv(item.ddKey, item.ddValue)
			t.Setenv(item.otelKey, item.invalidOTel)

			require.Equal(t, item.ddValue, TracerOTelDDValue(item.name))
			require.Len(t, logger.Logs(), 1)
			require.Contains(t, logger.Logs()[0], "using "+item.ddKey+"="+item.ddValue)
			tags := []string{"config_datadog:" + strings.ToLower(item.ddKey), "config_opentelemetry:" + strings.ToLower(item.otelKey)}
			require.NotZero(t, rec.Count(telemetry.NamespaceTracers, "otel.env.hiding", tags).Get())
			require.Zero(t, rec.Count(telemetry.NamespaceTracers, "otel.env.invalid", tags).Get())
		})
	}
}

func TestTracerOTelCompatibilitySharesStableProviderAndResamplesEnvironment(t *testing.T) {
	original := newStableProvider
	t.Cleanup(func() {
		newStableProvider = original
	})

	pairs := [][2]string{
		{"DD_SERVICE", "OTEL_SERVICE_NAME"},
		{"DD_TRACE_DEBUG", "OTEL_LOG_LEVEL"},
		{"DD_TRACE_ENABLED", "OTEL_TRACES_EXPORTER"},
		{"DD_TRACE_SAMPLE_RATE", "OTEL_TRACES_SAMPLER"},
		{"DD_TRACE_PROPAGATION_STYLE", "OTEL_PROPAGATORS"},
		{"DD_TAGS", "OTEL_RESOURCE_ATTRIBUTES"},
	}
	for _, pair := range pairs {
		unsetForTest(t, pair[0])
		unsetForTest(t, pair[1])
	}
	unsetForTest(t, "OTEL_TRACES_SAMPLER_ARG")
	unsetForTest(t, "DD_DATA_STREAMS_ENABLED")

	constructions := 0
	newStableProvider = sync.OnceValue(func() *provider.Provider {
		constructions++
		return provider.New()
	})

	t.Setenv("DD_DATA_STREAMS_ENABLED", "false")
	require.False(t, InstrumentationDataStreamsEnabled())
	t.Setenv("DD_SERVICE", "first")
	require.Equal(t, "first", TracerOTelDDValue("service"))
	first := newStableProvider()
	require.Same(t, first, newStableProvider())

	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	require.True(t, InstrumentationDataStreamsEnabled(),
		"ordinary stable accessors must resample live environment values")
	t.Setenv("DD_SERVICE", "second")
	require.Equal(t, "second", TracerOTelDDValue("service"),
		"cached provider sources must continue sampling live environment values")
	require.Empty(t, TracerOTelDDValue("debugMode"))
	require.Equal(t, 1, constructions,
		"stable YAML sources must be parsed once and shared across legacy accessors")
}

func TestTracerOTelSampleRateSamplerArgumentSemantics(t *testing.T) {
	for _, key := range []string{
		"DD_TRACE_SAMPLE_RATE",
		"OTEL_TRACES_SAMPLER",
		"OTEL_TRACES_SAMPLER_ARG",
	} {
		unsetForTest(t, key)
	}
	t.Setenv("OTEL_TRACES_SAMPLER", "parentbased_traceidratio")

	require.Equal(t, "1.0", TracerOTelDDValue("sampleRate"), "absent argument")

	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "")
	require.Equal(t, "1.0", TracerOTelDDValue("sampleRate"), "explicit empty argument")

	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.25")
	require.Equal(t, "0.25", TracerOTelDDValue("sampleRate"))
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.75")
	require.Equal(t, "0.75", TracerOTelDDValue("sampleRate"),
		"each resolution must resample the current argument")

	t.Setenv("DD_TRACE_SAMPLE_RATE", "0.5")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.1")
	require.Equal(t, "0.5", TracerOTelDDValue("sampleRate"),
		"an applicable Datadog value must win")
}

func TestTracerOTelCompatibilityPreservesStableSourceBoundary(t *testing.T) {
	for name, binding := range tracerOTelBindings {
		require.Equal(t, name == "debugMode", !binding.EnvironmentOnly, name)
		require.Equal(t, SourceStable, registeredDefinition(tracerOTelDDKeys[name]).Sources, name)
	}
	require.Contains(t, tracerOTelBindings["sampleRate"].Keys, "OTEL_TRACES_SAMPLER_ARG")
}

func TestTracerSourceHostnameConstructionResamplesAndStagesExactEvent(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	unsetForTest(t, "DD_TRACE_SOURCE_HOSTNAME")

	for _, value := range []string{"first-hostname", "second-hostname", ""} {
		t.Setenv("DD_TRACE_SOURCE_HOSTNAME", value)
		candidate := NewTracerGeneration()
		require.Equal(t, value, candidate.Hostname())
		require.True(t, candidate.ReportHostname())

		event := tracerSourceHostnameEnvironmentEvent(t, candidate)
		require.Equal(t, ConfigEvent{
			Kind:          EventConfiguration,
			BindingID:     tracerSourceHostnameBinding.ID,
			Name:          "DD_TRACE_SOURCE_HOSTNAME",
			Value:         value,
			Present:       true,
			Valid:         true,
			Origin:        telemetry.OriginEnvVar,
			SourceOrdinal: schema.SourceOrdinalEnvironment,
			Policy:        TelemetryReport,
			Cadence:       ReportOncePerGeneration,
			ReportValue:   true,
		}, event)
	}
}

func TestTracerConstructionEventsWaitForWinningPublication(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	instrumentationReporter.ResetForTesting()
	t.Cleanup(instrumentationReporter.ResetForTesting)
	rec := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(rec))
	t.Setenv("DD_LLMOBS_ENABLED", "true")
	t.Setenv("DD_APM_TRACING_ENABLED", "false")
	t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
	t.Setenv("DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", "true")

	stage := func(candidate *Config) {
		require.True(t, TracerLLMObsSnapshot(candidate).Enabled)
		require.False(t, APMTracingEnabled(candidate))
		require.Equal(t, "v1", TracerNamingSchemaSnapshot(candidate).Schema)
	}

	failed := NewTracerGeneration()
	stage(failed)
	failedPrepared := failed.PrepareClaims()
	failed.SetEnv("invalidate-failed-candidate", OriginCode, ProductTracer)
	require.Error(t, PublishTracerGeneration(failed, failedPrepared, nil))
	require.Zero(t, countConfiguration(rec.Configuration, "DD_LLMOBS_ENABLED", telemetry.OriginEnvVar, "true"))
	require.Zero(t, countConfiguration(rec.Configuration, "DD_APM_TRACING_ENABLED", telemetry.OriginEnvVar, "false"))
	require.Zero(t, countConfiguration(rec.Configuration, "DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", telemetry.OriginEnvVar, "v1"))
	require.Zero(t, countConfiguration(rec.Configuration, "DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", telemetry.OriginEnvVar, "true"))

	loser := NewTracerGeneration()
	stage(loser)
	require.Equal(t, "v1", ProcessNamingSchemaSnapshot().Schema)
	require.Equal(t, 1, countConfiguration(rec.Configuration, "DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", telemetry.OriginEnvVar, "true"),
		"the distinct process-init binding should report immediately")

	winner := NewTracerGeneration()
	stage(winner)
	require.Zero(t, countConfiguration(rec.Configuration, "DD_LLMOBS_ENABLED", telemetry.OriginEnvVar, "true"))
	require.Zero(t, countConfiguration(rec.Configuration, "DD_APM_TRACING_ENABLED", telemetry.OriginEnvVar, "false"))
	require.Equal(t, 1, countConfiguration(rec.Configuration, "DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", telemetry.OriginEnvVar, "true"))

	require.NoError(t, PublishTracerGeneration(winner, winner.PrepareClaims(), func(Publication) {
		winner.DrainPublicationTelemetry()
	}))
	require.Equal(t, 1, countConfiguration(rec.Configuration, "DD_LLMOBS_ENABLED", telemetry.OriginEnvVar, "true"))
	require.Equal(t, 1, countConfiguration(rec.Configuration, "DD_APM_TRACING_ENABLED", telemetry.OriginEnvVar, "false"))
	require.Equal(t, 2, countConfiguration(rec.Configuration, "DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", telemetry.OriginEnvVar, "true"),
		"process-init reporting must not suppress generation 1")

	winner.DrainPublicationTelemetry()
	require.Equal(t, 1, countConfiguration(rec.Configuration, "DD_LLMOBS_ENABLED", telemetry.OriginEnvVar, "true"),
		"publication events must drain once")
}

func TestTracerSourceHostnameEventsWaitForWinningPublication(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	instrumentationReporter.ResetForTesting()
	t.Cleanup(instrumentationReporter.ResetForTesting)
	rec := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(rec))
	unsetForTest(t, "DD_TRACE_SOURCE_HOSTNAME")

	t.Setenv("DD_TRACE_SOURCE_HOSTNAME", "failed-hostname")
	failed := NewTracerGeneration()
	failedPrepared := failed.PrepareClaims()
	failed.SetEnv("invalidate-failed-hostname", OriginCode, ProductTracer)
	require.Error(t, PublishTracerGeneration(failed, failedPrepared, nil))
	require.Zero(t, countConfiguration(rec.Configuration, "DD_TRACE_SOURCE_HOSTNAME", telemetry.OriginEnvVar, "failed-hostname"))

	t.Setenv("DD_TRACE_SOURCE_HOSTNAME", "unpublished-hostname")
	_ = NewTracerGeneration()
	require.Zero(t, countConfiguration(rec.Configuration, "DD_TRACE_SOURCE_HOSTNAME", telemetry.OriginEnvVar, "unpublished-hostname"))

	t.Setenv("DD_TRACE_SOURCE_HOSTNAME", "winning-hostname")
	winner := NewTracerGeneration()
	require.Zero(t, countConfiguration(rec.Configuration, "DD_TRACE_SOURCE_HOSTNAME", telemetry.OriginEnvVar, "winning-hostname"))
	require.NoError(t, PublishTracerGeneration(winner, winner.PrepareClaims(), func(Publication) {
		winner.DrainPublicationTelemetry()
	}))
	require.Equal(t, 1, countConfiguration(rec.Configuration, "DD_TRACE_SOURCE_HOSTNAME", telemetry.OriginEnvVar, "winning-hostname"))
	winner.DrainPublicationTelemetry()
	require.Equal(t, 1, countConfiguration(rec.Configuration, "DD_TRACE_SOURCE_HOSTNAME", telemetry.OriginEnvVar, "winning-hostname"),
		"publication events must drain once")

	t.Setenv("DD_TRACE_SOURCE_HOSTNAME", "")
	explicitEmpty := NewTracerGeneration()
	require.NoError(t, PublishTracerGeneration(explicitEmpty, explicitEmpty.PrepareClaims(), func(Publication) {
		explicitEmpty.DrainPublicationTelemetry()
	}))
	require.Equal(t, 1, countConfiguration(rec.Configuration, "DD_TRACE_SOURCE_HOSTNAME", telemetry.OriginEnvVar, ""))
	explicitEmpty.DrainPublicationTelemetry()
	require.Equal(t, 1, countConfiguration(rec.Configuration, "DD_TRACE_SOURCE_HOSTNAME", telemetry.OriginEnvVar, ""),
		"the next published generation must report explicit empty exactly once")
}

func TestTracerSourceHostnameProgrammaticOverrideTelemetryWins(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	instrumentationReporter.ResetForTesting()
	t.Cleanup(instrumentationReporter.ResetForTesting)
	rec := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(rec))
	t.Setenv("DD_TRACE_SOURCE_HOSTNAME", "environment-hostname")

	candidate := NewTracerGeneration()
	candidate.SetHostname("option-hostname", OriginCode)
	require.Equal(t, "option-hostname", candidate.Hostname())
	require.NoError(t, PublishTracerGeneration(candidate, candidate.PrepareClaims(), func(Publication) {
		candidate.DrainPublicationTelemetry()
	}))

	var environment, code *telemetry.Configuration
	for i := range rec.Configuration {
		entry := &rec.Configuration[i]
		if entry.Name != "DD_TRACE_SOURCE_HOSTNAME" {
			continue
		}
		switch entry.Origin {
		case telemetry.OriginEnvVar:
			environment = entry
		case telemetry.OriginCode:
			code = entry
		}
	}
	require.NotNil(t, environment, "the environment attempt must be reported")
	require.NotNil(t, code, "the WithHostname override must be reported")
	require.Equal(t, "environment-hostname", environment.Value)
	require.Equal(t, "option-hostname", code.Value)
	require.Greater(t, code.SeqID, environment.SeqID,
		"the programmatic winner must have a higher sequence ID than the environment attempt")
}

func tracerSourceHostnameEnvironmentEvent(t *testing.T, candidate *Config) ConfigEvent {
	t.Helper()
	candidate.mu.Lock()
	events := cloneConfigEvents(candidate.pendingConfigEvents)
	candidate.mu.Unlock()
	for _, event := range events {
		if event.BindingID == tracerSourceHostnameBinding.ID &&
			event.Name == "DD_TRACE_SOURCE_HOSTNAME" &&
			event.SourceOrdinal == schema.SourceOrdinalEnvironment {
			return event
		}
	}
	t.Fatalf("DD_TRACE_SOURCE_HOSTNAME environment event not staged: %#v", events)
	return ConfigEvent{}
}

func TestStagePublicationConfigEventsDefensivelyClonesAndScrubsOmittedValues(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	instrumentationReporter.ResetForTesting()
	t.Cleanup(instrumentationReporter.ResetForTesting)
	rec := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(rec))

	candidate := NewTracerGeneration()
	initialEvents := len(candidate.pendingConfigEvents)
	value := []string{"before"}
	candidate.StagePublicationConfigEvents([]ConfigEvent{{
		Kind: EventConfiguration, BindingID: "tracer.llmobs", Name: "DD_LLMOBS_ENABLED",
		Value: value, Present: true, Valid: true, Origin: telemetry.OriginEnvVar,
		SourceOrdinal: schema.SourceOrdinalEnvironment, Policy: TelemetryReport,
		Cadence: ReportOncePerGeneration, ReportValue: true,
	}})
	candidate.StagePublicationConfigEvents([]ConfigEvent{{
		Kind: EventConfiguration, BindingID: "tracer.llmobs", Name: "DD_LLMOBS_AGENTLESS_ENABLED",
		Value: "true", Present: true, Valid: true, Origin: telemetry.OriginEnvVar,
		SourceOrdinal: schema.SourceOrdinalEnvironment, Policy: TelemetryReport,
		Cadence: ReportOncePerGeneration, ReportValue: true,
	}})
	candidate.StagePublicationConfigEvents([]ConfigEvent{{
		Kind: EventConfiguration, BindingID: "tracer.llmobs", Name: "DD_LLMOBS_ML_APP",
		Value: "private-app", Err: errors.New("secret parse detail"), Present: true, Origin: telemetry.OriginEnvVar,
		SourceOrdinal: schema.SourceOrdinalEnvironment, Policy: TelemetryOmit,
		Cadence: ReportOncePerGeneration, ReportValue: true,
	}})
	value[0] = "after"
	require.Equal(t, []string{"before"}, candidate.pendingConfigEvents[initialEvents].Value)
	require.Nil(t, candidate.pendingConfigEvents[initialEvents+2].Value)
	require.NoError(t, candidate.pendingConfigEvents[initialEvents+2].Err)

	require.NoError(t, PublishTracerGeneration(candidate, candidate.PrepareClaims(), func(Publication) {
		candidate.DrainPublicationTelemetry()
	}))
	require.Equal(t, 1, countConfiguration(rec.Configuration, "DD_LLMOBS_AGENTLESS_ENABLED", telemetry.OriginEnvVar, "true"))
	require.Zero(t, countConfiguration(rec.Configuration, "DD_LLMOBS_ML_APP", telemetry.OriginEnvVar, "private-app"))
}

func TestNewPropagationSnapshotResamplesEnvironment(t *testing.T) {
	t.Setenv("DD_TRACE_PROPAGATION_EXTRACT_FIRST", "false")
	t.Setenv("DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT", "continue")
	t.Setenv("DD_TRACE_PROPAGATION_STYLE_INJECT", "datadog")
	t.Setenv("DD_TRACE_PROPAGATION_STYLE_EXTRACT", "datadog")
	first := NewPropagationSnapshot()
	require.False(t, first.ExtractFirst)
	require.Equal(t, "continue", first.BehaviorExtract)
	require.Equal(t, "datadog", first.InjectStyle)
	require.Equal(t, "datadog", first.ExtractStyle)

	t.Setenv("DD_TRACE_PROPAGATION_EXTRACT_FIRST", "true")
	t.Setenv("DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT", "ignore")
	t.Setenv("DD_TRACE_PROPAGATION_STYLE_INJECT", "tracecontext")
	t.Setenv("DD_TRACE_PROPAGATION_STYLE_EXTRACT", "b3multi")
	second := NewPropagationSnapshot()
	require.True(t, second.ExtractFirst)
	require.Equal(t, "ignore", second.BehaviorExtract)
	require.Equal(t, "tracecontext", second.InjectStyle)
	require.Equal(t, "b3multi", second.ExtractStyle)
}

func TestStoppedTracerTagValueResolvesOnlyRequestedTag(t *testing.T) {
	instrumentationReporter.ResetForTesting()
	t.Cleanup(instrumentationReporter.ResetForTesting)
	rec := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(rec))
	t.Setenv("DD_ENV", "production")
	t.Setenv("DD_VERSION", "1.2.3")

	require.Equal(t, "production", StoppedTracerTagValue(StoppedTracerEnvironment))
	require.NotEmpty(t, rec.Configuration)
	for _, configuration := range rec.Configuration {
		require.NotEqual(t, telemetry.EnvToTelemetryName("DD_VERSION"), configuration.Name)
	}
}

func TestTraceIDLoggingEnabledResamplesPerCall(t *testing.T) {
	t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "false")
	require.False(t, TraceIDLoggingEnabled())
	t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "true")
	require.True(t, TraceIDLoggingEnabled())
	t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "invalid")
	require.True(t, TraceIDLoggingEnabled())
}

func TestNamingSchemaSnapshotsAreEnvironmentOnly(t *testing.T) {
	t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
	t.Setenv("DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", "true")

	initSnapshot := InstrumentationNamingSchemaSnapshot()
	require.Equal(t, "v1", initSnapshot.Schema)
	require.True(t, initSnapshot.RemoveIntegrationServiceNames)

	tracerSnapshot := TracerNamingSchemaSnapshot(NewTracerGeneration())
	require.Equal(t, "v1", tracerSnapshot.Schema)
	require.True(t, tracerSnapshot.RemoveIntegrationServiceNames)
}

func unsetForTest(t *testing.T, key string) {
	t.Helper()
	value, present := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	t.Cleanup(func() {
		if present {
			require.NoError(t, os.Setenv(key, value))
		} else {
			require.NoError(t, os.Unsetenv(key))
		}
	})
}
