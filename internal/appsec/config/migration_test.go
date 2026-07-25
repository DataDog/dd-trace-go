// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/appsec/apisec"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

type fixedAPISecuritySampler struct{}

func (fixedAPISecuritySampler) DecisionFor(apisec.SamplingKey) bool { return false }

func TestLegacyAppSecHelpersResolveOnlyTheirOwnKeys(t *testing.T) {
	allKeys := []string{
		EnvAPISecEnabled,
		EnvAPISecSampleRate,
		EnvAPISecDownstreamRequestBodyAnalysisSampleRate,
		EnvAPISecMaxDownstreamRequestBodyAnalysis,
		EnvObfuscatorKey,
		EnvObfuscatorValue,
		EnvWAFTimeout,
		EnvTraceRateLimit,
		EnvRules,
		EnvRASPEnabled,
		"DD_APM_TRACING_ENABLED",
		envAPISecSampleDelay,
		EnvAPISecProxySampleRate,
	}
	tests := []struct {
		name    string
		allowed []string
		call    func()
	}{
		{
			name: "NewAPISecConfig",
			allowed: []string{
				EnvAPISecEnabled,
				EnvAPISecSampleRate,
				EnvAPISecDownstreamRequestBodyAnalysisSampleRate,
				EnvAPISecMaxDownstreamRequestBodyAnalysis,
				EnvRASPEnabled,
			},
			call: func() { NewAPISecConfig(WithAPISecSampler(fixedAPISecuritySampler{})) },
		},
		{
			name:    "readAPISecuritySampleRate",
			allowed: []string{EnvAPISecSampleRate},
			call:    func() { readAPISecuritySampleRate() },
		},
		{
			name:    "RASPEnabled",
			allowed: []string{EnvRASPEnabled},
			call:    func() { RASPEnabled() },
		},
		{
			name:    "NewObfuscatorConfig",
			allowed: []string{EnvObfuscatorKey, EnvObfuscatorValue},
			call:    func() { NewObfuscatorConfig() },
		},
		{
			name:    "WAFTimeoutFromEnv",
			allowed: []string{EnvWAFTimeout},
			call:    func() { WAFTimeoutFromEnv() },
		},
		{
			name:    "RateLimitFromEnv",
			allowed: []string{EnvTraceRateLimit},
			call:    func() { RateLimitFromEnv() },
		},
		{
			name:    "RulesFromEnv",
			allowed: []string{EnvRules},
			call:    func() { _, _ = RulesFromEnv() },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setInvalidAppSecEnvironment(t)
			names, logs := captureAppSecHelperEffects(t, tc.call)
			for _, name := range names {
				require.Contains(t, tc.allowed, name, "reported unrelated configuration %s", name)
			}
			for _, key := range allKeys {
				if slices.Contains(tc.allowed, key) {
					continue
				}
				require.NotContains(t, logs, key, "logged unrelated configuration %s", key)
			}
		})
	}
}

func TestNewConfigStopsAfterRulesFailure(t *testing.T) {
	setInvalidAppSecEnvironment(t)

	var cfg *Config
	var err error
	names, logs := captureAppSecHelperEffects(t, func() {
		cfg, err = (&StartConfig{}).NewConfig()
	})

	require.Nil(t, cfg)
	require.ErrorContains(t, err, "reading WAF rules from environment")
	require.Empty(t, names)
	for _, key := range []string{
		EnvObfuscatorKey,
		EnvObfuscatorValue,
		EnvWAFTimeout,
		EnvTraceRateLimit,
		EnvAPISecEnabled,
		EnvAPISecSampleRate,
		EnvAPISecDownstreamRequestBodyAnalysisSampleRate,
		EnvAPISecMaxDownstreamRequestBodyAnalysis,
		EnvRASPEnabled,
		"DD_APM_TRACING_ENABLED",
	} {
		require.NotContains(t, logs, key)
	}
}

func TestNewConfigStopsAfterWAFConstructionFailure(t *testing.T) {
	setInvalidAppSecEnvironment(t)
	rulesPath := filepath.Join(t.TempDir(), "invalid-rules.json")
	require.NoError(t, os.WriteFile(rulesPath, []byte("{"), 0o600))
	t.Setenv(EnvRules, rulesPath)
	t.Setenv(EnvObfuscatorKey, DefaultObfuscatorKeyRegex)
	t.Setenv(EnvObfuscatorValue, DefaultObfuscatorValueRegex)

	var cfg *Config
	var err error
	names, logs := captureAppSecHelperEffects(t, func() {
		cfg, err = (&StartConfig{}).NewConfig()
	})

	require.Nil(t, cfg)
	require.Error(t, err)
	require.Empty(t, names)
	for _, key := range []string{
		EnvWAFTimeout,
		EnvTraceRateLimit,
		EnvAPISecEnabled,
		EnvAPISecSampleRate,
		EnvAPISecDownstreamRequestBodyAnalysisSampleRate,
		EnvAPISecMaxDownstreamRequestBodyAnalysis,
		EnvRASPEnabled,
		"DD_APM_TRACING_ENABLED",
	} {
		require.NotContains(t, logs, key)
	}
}

func TestZeroByteRulesRemainConfiguredThroughLegacyBoundaries(t *testing.T) {
	rulesPath := filepath.Join(t.TempDir(), "empty-rules.json")
	require.NoError(t, os.WriteFile(rulesPath, []byte{}, 0o600))
	t.Setenv(EnvRules, rulesPath)

	rules, err := RulesFromEnv()
	require.NoError(t, err)
	require.NotNil(t, rules)
	require.Empty(t, rules)

	cfg, err := (&StartConfig{}).NewConfig()
	require.Nil(t, cfg)
	require.Error(t, err, "a configured empty ruleset must reach the WAF JSON decoder")
}

func setInvalidAppSecEnvironment(t *testing.T) {
	t.Helper()
	values := map[string]string{
		EnvAPISecEnabled:    "invalid-bool",
		EnvAPISecSampleRate: "invalid-float",
		EnvAPISecDownstreamRequestBodyAnalysisSampleRate: "invalid-float",
		EnvAPISecMaxDownstreamRequestBodyAnalysis:        "invalid-int",
		EnvObfuscatorKey:         "+",
		EnvObfuscatorValue:       "+",
		EnvWAFTimeout:            "invalid-duration",
		EnvTraceRateLimit:        "invalid-rate",
		EnvRules:                 filepath.Join(t.TempDir(), "missing-rules.json"),
		EnvRASPEnabled:           "invalid-bool",
		"DD_APM_TRACING_ENABLED": "invalid-bool",
		envAPISecSampleDelay:     "invalid-duration",
		EnvAPISecProxySampleRate: "invalid-int",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
}

func captureAppSecHelperEffects(t *testing.T, call func()) ([]string, string) {
	t.Helper()
	client := new(telemetrytest.MockClient)
	client.On("RegisterAppConfigs", mock.Anything).Return().Maybe()
	restoreClient := telemetry.MockClient(client)
	t.Cleanup(restoreClient)

	logger := new(log.RecordLogger)
	restoreLogger := log.UseLogger(logger)
	t.Cleanup(restoreLogger)

	call()

	var names []string
	for _, recorded := range client.Calls {
		if recorded.Method != "RegisterAppConfigs" {
			continue
		}
		configs, ok := recorded.Arguments.Get(0).([]telemetry.Configuration)
		require.True(t, ok)
		for _, config := range configs {
			names = append(names, config.Name)
		}
	}
	return names, strings.Join(logger.Logs(), "\n")
}
