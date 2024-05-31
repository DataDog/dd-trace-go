// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package config

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry/telemetrytest"
)

func TestSCAEnabled(t *testing.T) {
	for _, tc := range []struct {
		name              string
		envVarVal         string
		telemetryExpected bool
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
			expectedValue:     false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envVarVal != "" {
				t.Setenv(EnvSCAEnabled, tc.envVarVal)
			}

			telemetryClient := new(telemetrytest.MockClient)
			telemetryClient.On("RegisterAppConfig", EnvSCAEnabled, tc.expectedValue, telemetry.OriginEnvVar).Return()

			registerSCAAppConfigTelemetry(telemetryClient)

			if tc.telemetryExpected {
				telemetryClient.AssertCalled(t, "RegisterAppConfig", EnvSCAEnabled, tc.expectedValue, telemetry.OriginEnvVar)
				telemetryClient.AssertNumberOfCalls(t, "RegisterAppConfig", 1)
			} else {
				telemetryClient.AssertNumberOfCalls(t, "RegisterAppConfig", 0)
			}
		})
	}
}
