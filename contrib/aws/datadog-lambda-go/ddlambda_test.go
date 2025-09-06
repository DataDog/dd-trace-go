/*
 * Unless explicitly stated otherwise all files in this repository are licensed
 * under the Apache License Version 2.0.
 *
 * This product includes software developed at Datadog (https://www.datadoghq.com/).
 * Copyright 2021 Datadog, Inc.
 */
package ddlambda

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInvokeDryRun(t *testing.T) {
	t.Setenv(UniversalInstrumentation, "false")
	t.Setenv(DatadogTraceEnabledEnvVar, "false")

	called := false
	_, err := InvokeDryRun(func(ctx context.Context) {
		called = true
		globalCtx := GetContext()
		assert.Equal(t, globalCtx, ctx)
	}, nil)
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestMetricsSilentFailWithoutWrapper(t *testing.T) {
	Metric("my-metric", 100, "my:tag")
}

func TestMetricsSubmitWithWrapper(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	_, err := InvokeDryRun(func(ctx context.Context) {
		Metric("my-metric", 100, "my:tag")
	}, &Config{
		APIKey: "abc-123",
		Site:   server.URL,
	})
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestToMetricConfigLocalTest(t *testing.T) {
	testcases := []struct {
		envs map[string]string
		cval bool
	}{
		{
			envs: map[string]string{"DD_LOCAL_TEST": "True"},
			cval: true,
		},
		{
			envs: map[string]string{"DD_LOCAL_TEST": "true"},
			cval: true,
		},
		{
			envs: map[string]string{"DD_LOCAL_TEST": "1"},
			cval: true,
		},
		{
			envs: map[string]string{"DD_LOCAL_TEST": "False"},
			cval: false,
		},
		{
			envs: map[string]string{"DD_LOCAL_TEST": "false"},
			cval: false,
		},
		{
			envs: map[string]string{"DD_LOCAL_TEST": "0"},
			cval: false,
		},
		{
			envs: map[string]string{"DD_LOCAL_TEST": ""},
			cval: false,
		},
		{
			envs: map[string]string{},
			cval: false,
		},
	}

	cfg := Config{}
	for _, tc := range testcases {
		t.Run(fmt.Sprintf("%#v", tc.envs), func(t *testing.T) {
			for k, v := range tc.envs {
				os.Setenv(k, v)
			}
			mc := cfg.toMetricsConfig(true)
			assert.Equal(t, tc.cval, mc.LocalTest)
		})
	}
}

func TestCalculateFipsMode(t *testing.T) {
	// Save original environment to restore later
	originalRegion := os.Getenv("AWS_REGION")
	originalFipsMode := os.Getenv(FIPSModeEnvVar)
	defer func() {
		os.Setenv("AWS_REGION", originalRegion)
		os.Setenv(FIPSModeEnvVar, originalFipsMode)
	}()

	testCases := []struct {
		name           string
		configFIPSMode *bool
		region         string
		fipsModeEnv    string
		expected       bool
	}{
		{
			name:           "Config explicit true",
			configFIPSMode: boolPtr(true),
			region:         "us-east-1",
			fipsModeEnv:    "",
			expected:       true,
		},
		{
			name:           "Config explicit false",
			configFIPSMode: boolPtr(false),
			region:         "us-gov-west-1",
			fipsModeEnv:    "",
			expected:       false,
		},
		{
			name:           "GovCloud default true",
			configFIPSMode: nil,
			region:         "us-gov-east-1",
			fipsModeEnv:    "",
			expected:       true,
		},
		{
			name:           "Non-GovCloud default false",
			configFIPSMode: nil,
			region:         "us-east-1",
			fipsModeEnv:    "",
			expected:       false,
		},
		{
			name:           "Env var override to true",
			configFIPSMode: nil,
			region:         "us-east-1",
			fipsModeEnv:    "true",
			expected:       true,
		},
		{
			name:           "Env var override to false",
			configFIPSMode: nil,
			region:         "us-gov-west-1",
			fipsModeEnv:    "false",
			expected:       false,
		},
		{
			name:           "Invalid env var in GovCloud",
			configFIPSMode: nil,
			region:         "us-gov-west-1",
			fipsModeEnv:    "invalid",
			expected:       true,
		},
		{
			name:           "Invalid env var in non-GovCloud",
			configFIPSMode: nil,
			region:         "us-east-1",
			fipsModeEnv:    "invalid",
			expected:       false,
		},
		{
			name:           "Config takes precedence over env and region",
			configFIPSMode: boolPtr(true),
			region:         "us-east-1",
			fipsModeEnv:    "false",
			expected:       true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			os.Setenv("AWS_REGION", tc.region)
			os.Setenv(FIPSModeEnvVar, tc.fipsModeEnv)

			cfg := &Config{FIPSMode: tc.configFIPSMode}
			result := cfg.calculateFipsMode()

			assert.Equal(t, tc.expected, result, "calculateFipsMode returned incorrect value")
		})
	}
}

// Helper function to create bool pointers
func boolPtr(b bool) *bool {
	return &b
}
