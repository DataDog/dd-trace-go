// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

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
			if tc.set {
				t.Setenv(EnvAgenticOnboarding, tc.envVarVal)
			}

			expected := []telemetry.Configuration{{Name: name, Value: tc.expectedValue, Origin: tc.expectedOrigin}}
			telemetryClient := new(telemetrytest.MockClient)
			telemetryClient.On("RegisterAppConfigs", expected).Return()
			defer telemetry.MockClient(telemetryClient)()

			registerAgenticOnboardingTelemetry()

			// Always emitted, even when unset (RFC-1113).
			telemetryClient.AssertCalled(t, "RegisterAppConfigs", expected)
			telemetryClient.AssertNumberOfCalls(t, "RegisterAppConfigs", 1)
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
			name:              "undefined",
			envVarVal:         "", // special case for undefined
			telemetryExpected: false,
			expectedValue:     false,
		},
		{
			name:              "parsing error",
			envVarVal:         "not a boolean string representation [at {all!}]",
			telemetryExpected: false,
			telemetryLog:      "appsec: failed to get SCA config",
			expectedValue:     false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envVarVal != "" {
				t.Setenv(EnvSCAEnabled, tc.envVarVal)
			}

			telemetryClient := new(telemetrytest.MockClient)
			telemetryClient.On("RegisterAppConfigs", []telemetry.Configuration{{Name: EnvSCAEnabled, Value: tc.expectedValue, Origin: telemetry.OriginEnvVar}}).Return()
			telemetryClient.On("RegisterAppConfig", EnvSCAEnabled, tc.expectedValue, telemetry.OriginEnvVar).Return()

			var logMatcher any
			if tc.telemetryLog != "" {
				logMatcher = mock.MatchedBy(func(record telemetry.Record) bool {
					return strings.HasPrefix(record.Message, tc.telemetryLog)
				})
				telemetryClient.On("Log", logMatcher, []telemetry.LogOption(nil)).Return()
			}
			defer telemetry.MockClient(telemetryClient)()

			registerSCAAppConfigTelemetry()

			if tc.telemetryExpected {
				telemetryClient.AssertCalled(t, "RegisterAppConfigs", []telemetry.Configuration{{Name: EnvSCAEnabled, Value: tc.expectedValue, Origin: telemetry.OriginEnvVar}})
				telemetryClient.AssertNumberOfCalls(t, "RegisterAppConfigs", 1)
			} else {
				telemetryClient.AssertNumberOfCalls(t, "RegisterAppConfigs", 0)
			}
			if tc.telemetryLog != "" {
				telemetryClient.AssertCalled(t, "Log", logMatcher, []telemetry.LogOption(nil))
			}
		})
	}
}
