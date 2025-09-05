// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package config

import (
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
	"github.com/stretchr/testify/mock"
)

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
			if tc.telemetryLog != "" {
				// Use pattern matching for secure telemetry format
				telemetryClient.On("Log", telemetry.LogError,
					mock.MatchedBy(func(msg string) bool {
						return strings.HasPrefix(msg, tc.telemetryLog)
					}), []telemetry.LogOption(nil)).Return()
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
				// Assert that telemetry log was called with expected prefix
				telemetryClient.AssertCalled(t, "Log", telemetry.LogError,
					mock.MatchedBy(func(msg string) bool {
						return strings.HasPrefix(msg, tc.telemetryLog)
					}), []telemetry.LogOption(nil))
			}
		})
	}
}
