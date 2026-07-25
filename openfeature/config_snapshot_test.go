// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"strings"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/require"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
)

func TestOpenFeaturePublicDisabledReturnsNoopWithExactLog(t *testing.T) {
	internalffe.ResetForTest()
	defer internalffe.ResetForTest()
	t.Setenv(ffeProductEnvVar, "false")
	t.Setenv("DD_SERVICE", "must-not-be-read")
	logger := new(log.RecordLogger)
	defer log.UseLogger(logger)()

	provider, err := NewDatadogProvider(ProviderConfig{})
	log.Flush()

	require.NoError(t, err)
	require.IsType(t, &of.NoopProvider{}, provider)
	require.Contains(t, strings.Join(logger.Logs(), "\n"),
		"openfeature: experimental flagging provider is not enabled, please set DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED=true to enable it")
}

func TestOpenFeatureSnapshotAwareProviderPreservesHooksAndSharedEVP(t *testing.T) {
	snapshot := internalconfig.OpenFeatureSnapshot{
		Service:                     "checkout",
		Environment:                 "staging",
		Version:                     "1.2.3",
		ProviderEnabled:             true,
		SpanEnrichmentEnabled:       true,
		FlagEvaluationCountsEnabled: true,
	}

	provider := newDatadogProviderWithSnapshot(ProviderConfig{}, snapshot)

	require.Len(t, provider.hooks, 4)
	require.IsType(t, &exposureHook{}, provider.hooks[0])
	require.IsType(t, &flagEvalMetricsHook{}, provider.hooks[1])
	require.IsType(t, &flagEvalLoggingHook{}, provider.hooks[2])
	require.IsType(t, &spanEnrichmentHook{}, provider.hooks[3])
	require.Same(t, provider.exposureWriter.evp, provider.flagEvalLoggingWriter.evp)
	require.Equal(t, exposureContext{
		Service: "checkout",
		Version: "1.2.3",
		Env:     "staging",
	}, provider.exposureWriter.context)
	require.Equal(t, flagEvalDDContext{
		Service: "checkout",
		Version: "1.2.3",
		Env:     "staging",
	}, provider.flagEvalLoggingWriter.ddContext)
}

func TestOpenFeatureSnapshotAwareProviderOptionalHooks(t *testing.T) {
	provider := newDatadogProviderWithSnapshot(ProviderConfig{}, internalconfig.OpenFeatureSnapshot{
		Service:                     "service",
		FlagEvaluationCountsEnabled: false,
		SpanEnrichmentEnabled:       false,
	})

	require.Len(t, provider.hooks, 2)
	require.IsType(t, &exposureHook{}, provider.hooks[0])
	require.IsType(t, &flagEvalMetricsHook{}, provider.hooks[1])
	require.Nil(t, provider.flagEvalLoggingWriter)
	require.Nil(t, provider.flagEvalLoggingHook)
}

func TestOpenFeatureStandaloneWritersSampleOnlyContext(t *testing.T) {
	t.Setenv("DD_SERVICE", "standalone-service")
	t.Setenv("DD_ENV", "standalone-env")
	t.Setenv("DD_VERSION", "standalone-version")
	t.Setenv(ffeProductEnvVar, "invalid")
	t.Setenv(spanEnrichmentEnvVar, "invalid")
	t.Setenv(flagEvalCountsEnabledEnvVar, "invalid")
	logger := new(log.RecordLogger)
	defer log.UseLogger(logger)()

	exposure := newExposureWriter(ProviderConfig{})
	evaluations := newFlagEvalLoggingWriter(ProviderConfig{})

	require.Equal(t, exposureContext{
		Service: "standalone-service",
		Version: "standalone-version",
		Env:     "standalone-env",
	}, exposure.context)
	require.Equal(t, flagEvalDDContext{
		Service: "standalone-service",
		Version: "standalone-version",
		Env:     "standalone-env",
	}, evaluations.ddContext)
	require.NotContains(t, strings.Join(logger.Logs(), "\n"), "Non-boolean value for env var")
}

func TestOpenFeatureRemoteConfigPathsKeepConstructionSnapshot(t *testing.T) {
	for _, fast := range []bool{false, true} {
		name := "slow"
		if fast {
			name = "fast"
		}
		t.Run(name, func(t *testing.T) {
			internalffe.ResetForTest()
			remoteconfig.Reset()
			t.Cleanup(internalffe.ResetForTest)
			t.Cleanup(remoteconfig.Reset)
			if fast {
				internalffe.SetSubscribedForTest(true)
			}
			snapshot := internalconfig.OpenFeatureSnapshot{
				Service:                     name + "-service",
				Environment:                 name + "-env",
				Version:                     name + "-version",
				ProviderEnabled:             true,
				FlagEvaluationCountsEnabled: true,
			}

			provider, err := startWithRemoteConfig(ProviderConfig{}, snapshot)
			require.NoError(t, err)
			t.Setenv("DD_SERVICE", "changed")
			t.Setenv("DD_ENV", "changed")
			t.Setenv("DD_VERSION", "changed")
			t.Setenv(flagEvalCountsEnabledEnvVar, "false")
			t.Setenv(spanEnrichmentEnvVar, "true")

			require.Equal(t, name+"-service", provider.exposureWriter.context.Service)
			require.Equal(t, name+"-env", provider.exposureWriter.context.Env)
			require.Equal(t, name+"-version", provider.exposureWriter.context.Version)
			require.Equal(t, name+"-service", provider.flagEvalLoggingWriter.ddContext.Service)
			require.NotNil(t, provider.flagEvalLoggingWriter)
			require.Len(t, provider.hooks, 3)
		})
	}
}
