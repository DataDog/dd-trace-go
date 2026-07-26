// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package config

import (
	"testing"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/stretchr/testify/require"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func configurationsNamed(configs []telemetry.Configuration, name string) []telemetry.Configuration {
	var matches []telemetry.Configuration
	for _, cfg := range configs {
		if cfg.Name == name {
			matches = append(matches, cfg)
		}
	}
	return matches
}

func withConfigEnv(t *testing.T, set func()) {
	t.Helper()
	t.Cleanup(func() {
		internalconfig.CreateNew()
	})
	set()
	internalconfig.CreateNew()
}

func TestAgenticOnboarding(t *testing.T) {
	name := telemetry.EnvToTelemetryName(EnvAgenticOnboarding)
	for _, tc := range []struct {
		name           string
		envVarVal      string
		set            bool
		expectedValue  string
		expectedOrigin telemetry.Origin
	}{
		{
			name:           "set-true",
			envVarVal:      "true",
			set:            true,
			expectedValue:  "true",
			expectedOrigin: telemetry.OriginEnvVar,
		},
		{
			name:           "set-arbitrary",
			envVarVal:      "false",
			set:            true,
			expectedValue:  "false",
			expectedOrigin: telemetry.OriginEnvVar,
		},
		{
			name:           "unset",
			expectedValue:  "",
			expectedOrigin: telemetry.OriginDefault,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := new(telemetrytest.RecordClient)
			defer telemetry.MockClient(client)()

			withConfigEnv(t, func() {
				if tc.set {
					t.Setenv(EnvAgenticOnboarding, tc.envVarVal)
				}
			})

			require.Equal(t, []telemetry.Configuration{{
				Name:   name,
				Value:  tc.expectedValue,
				Origin: tc.expectedOrigin,
			}}, configurationsNamed(client.Configuration, name))
		})
	}
}

func TestSCAEnabled(t *testing.T) {
	for _, tc := range []struct {
		name              string
		envVarVal         string
		telemetryExpected bool
		telemetryLog      string
		expectedValue     bool
	}{
		{
			name:              "true",
			envVarVal:         "true",
			telemetryExpected: true,
			expectedValue:     true,
		},
		{
			name:              "false",
			envVarVal:         "false",
			telemetryExpected: true,
			expectedValue:     false,
		},
		{
			name:          "undefined",
			envVarVal:     "", // special case for undefined
			expectedValue: false,
		},
		{
			name:          "parsing error",
			envVarVal:     "not a boolean string representation [at {all!}]",
			telemetryLog:  "appsec: failed to get SCA config",
			expectedValue: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := new(telemetrytest.RecordClient)
			defer telemetry.MockClient(client)()

			withConfigEnv(t, func() {
				if tc.envVarVal != "" {
					t.Setenv(EnvSCAEnabled, tc.envVarVal)
				}
			})
			registerSCAAppConfigTelemetry()

			configs := configurationsNamed(client.Configuration, EnvSCAEnabled)
			if tc.telemetryExpected {
				require.Equal(t, []telemetry.Configuration{{
					Name:   EnvSCAEnabled,
					Value:  tc.expectedValue,
					Origin: telemetry.OriginEnvVar,
				}}, configs)
			} else {
				require.Empty(t, configs)
			}
			if tc.telemetryLog != "" {
				require.Contains(t, client.Logs, telemetrytest.LogLine{
					Level: telemetry.LogError,
					Text:  tc.telemetryLog,
				})
			}
		})
	}
}

func TestIsEnabledByEnvironmentInvalid(t *testing.T) {
	withConfigEnv(t, func() {
		t.Setenv(EnvEnabled, "not-a-bool")
	})

	enabled, set, err := IsEnabledByEnvironment()

	require.False(t, enabled)
	require.False(t, set)
	require.EqualError(t, err, "non-boolean value for DD_APPSEC_ENABLED: 'not-a-bool' in env_var configuration, dropping")
}

func TestNewConfigTracingAsTransportFromEnvironment(t *testing.T) {
	if supported, _ := libddwaf.Usable(); !supported {
		t.Skip("WAF cannot be used")
	}

	for _, tc := range []struct {
		name               string
		tracingEnabled     string
		tracingAsTransport bool
	}{
		{
			name:               "tracing enabled",
			tracingEnabled:     "true",
			tracingAsTransport: false,
		},
		{
			name:               "tracing disabled",
			tracingEnabled:     "false",
			tracingAsTransport: true,
		},
		{
			name:               "invalid uses enabled default",
			tracingEnabled:     "not-a-bool",
			tracingAsTransport: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withConfigEnv(t, func() {
				t.Setenv("DD_APM_TRACING_ENABLED", tc.tracingEnabled)
			})

			cfg, err := NewStartConfig().NewConfig()
			require.NoError(t, err)
			defer cfg.WAFManager.Close()
			require.Equal(t, tc.tracingAsTransport, cfg.TracingAsTransport)
		})
	}
}
