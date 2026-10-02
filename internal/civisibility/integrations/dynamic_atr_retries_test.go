// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
)

func TestParseDynamicATREnabled(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		set      bool
		expected bool
	}{
		{name: "unset", set: false, expected: false},
		{name: "false", envValue: "false", set: true, expected: false},
		{name: "true", envValue: "true", set: true, expected: true},
		{name: "1", envValue: "1", set: true, expected: true},
		{name: "0", envValue: "0", set: true, expected: false},
		{name: "invalid", envValue: "not-a-bool", set: true, expected: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(constants.CIVisibilityDynamicATREnabledEnvironmentVariable, tc.envValue)
			} else {
				t.Setenv(constants.CIVisibilityDynamicATREnabledEnvironmentVariable, "")
			}
			// parseDynamicATREnabled uses os.Getenv directly (not t.Setenv-safe),
			// but t.Setenv does set the env for the test goroutine on Unix.
			got := parseDynamicATREnabled()
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestParseDynamicATRBuckets(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		set      bool
		expected *[5]int
	}{
		{name: "unset", set: false, expected: nil},
		{name: "empty", envValue: "", set: true, expected: nil},
		{name: "valid", envValue: "10,4,1,1,1", set: true, expected: &[5]int{10, 4, 1, 1, 1}},
		{name: "wrong_count", envValue: "10,4,1", set: true, expected: nil},
		{name: "non_integer", envValue: "a,b,c,d,e", set: true, expected: nil},
		{name: "value_below_1", envValue: "10,4,0,1,1", set: true, expected: nil},
		{name: "value_above_20", envValue: "21,4,1,1,1", set: true, expected: nil},
		{name: "valid_with_spaces", envValue: " 3, 1, 1, 1, 1 ", set: true, expected: &[5]int{3, 1, 1, 1, 1}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(constants.CIVisibilityDynamicATRBucketsEnvironmentVariable, tc.envValue)
			} else {
				t.Setenv(constants.CIVisibilityDynamicATRBucketsEnvironmentVariable, "")
			}
			got := parseDynamicATRBuckets()
			if tc.expected == nil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Equal(t, *tc.expected, *got)
			}
		})
	}
}

func TestInitDynamicATRSettings(t *testing.T) {
	t.Cleanup(func() {
		t.Setenv(constants.CIVisibilityDynamicATREnabledEnvironmentVariable, "")
		t.Setenv(constants.CIVisibilityDynamicATRBucketsEnvironmentVariable, "")
		resetDynamicATRSettingsForTesting()
	})
	t.Run("disabled", func(t *testing.T) {
		t.Setenv(constants.CIVisibilityDynamicATREnabledEnvironmentVariable, "")
		t.Setenv(constants.CIVisibilityDynamicATRBucketsEnvironmentVariable, "")
		resetDynamicATRSettingsForTesting()
		initDynamicATRSettings()
		assert.False(t, IsDynamicATREnabled())
		assert.Nil(t, GetDynamicATRCustomBuckets())
	})

	t.Run("enabled_no_custom_buckets", func(t *testing.T) {
		t.Setenv(constants.CIVisibilityDynamicATREnabledEnvironmentVariable, "true")
		t.Setenv(constants.CIVisibilityDynamicATRBucketsEnvironmentVariable, "")
		resetDynamicATRSettingsForTesting()
		initDynamicATRSettings()
		assert.True(t, IsDynamicATREnabled())
		assert.Nil(t, GetDynamicATRCustomBuckets())
	})

	t.Run("enabled_with_custom_buckets", func(t *testing.T) {
		t.Setenv(constants.CIVisibilityDynamicATREnabledEnvironmentVariable, "1")
		t.Setenv(constants.CIVisibilityDynamicATRBucketsEnvironmentVariable, "6,4,3,1,1")
		resetDynamicATRSettingsForTesting()
		initDynamicATRSettings()
		assert.True(t, IsDynamicATREnabled())
		buckets := GetDynamicATRCustomBuckets()
		require.NotNil(t, buckets)
		assert.Equal(t, [5]int{6, 4, 3, 1, 1}, *buckets)
	})

	t.Run("enabled_with_invalid_buckets_falls_back_to_nil", func(t *testing.T) {
		t.Setenv(constants.CIVisibilityDynamicATREnabledEnvironmentVariable, "true")
		t.Setenv(constants.CIVisibilityDynamicATRBucketsEnvironmentVariable, "not,enough,values")
		resetDynamicATRSettingsForTesting()
		initDynamicATRSettings()
		assert.True(t, IsDynamicATREnabled())
		assert.Nil(t, GetDynamicATRCustomBuckets())
	})
}

// TestDynamicATREnvVarConstants ensures the env var names match the Python reference.
func TestDynamicATREnvVarConstants(t *testing.T) {
	assert.Equal(t, "DD_CIVISIBILITY_DYNAMIC_ATR_ENABLED", constants.CIVisibilityDynamicATREnabledEnvironmentVariable)
	assert.Equal(t, "DD_CIVISIBILITY_DYNAMIC_ATR_BUCKETS", constants.CIVisibilityDynamicATRBucketsEnvironmentVariable)
}
