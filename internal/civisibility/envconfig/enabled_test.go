// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package envconfig

import (
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	internalenv "github.com/DataDog/dd-trace-go/v2/internal/env"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEnabledMode(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		wantMode EnabledMode
		wantOK   bool
	}{
		{name: "true", value: "true", wantMode: EnabledModeEnabled, wantOK: true},
		{name: "one", value: "1", wantMode: EnabledModeEnabled, wantOK: true},
		{name: "uppercase true", value: "TRUE", wantMode: EnabledModeEnabled, wantOK: true},
		{name: "single letter true", value: "t", wantMode: EnabledModeEnabled, wantOK: true},
		{name: "false", value: "false", wantMode: EnabledModeDisabled, wantOK: true},
		{name: "zero", value: "0", wantMode: EnabledModeDisabled, wantOK: true},
		{name: "uppercase false", value: "FALSE", wantMode: EnabledModeDisabled, wantOK: true},
		{name: "single letter false", value: "f", wantMode: EnabledModeDisabled, wantOK: true},
		{name: "parent", value: "parent", wantMode: EnabledModeParent, wantOK: true},
		{name: "mixed case parent", value: "PaReNt", wantMode: EnabledModeParent, wantOK: true},
		{name: "trimmed parent", value: " parent ", wantMode: EnabledModeParent, wantOK: true},
		{name: "invalid", value: "enabled", wantMode: EnabledModeDisabled, wantOK: false},
		{name: "empty", value: "", wantMode: EnabledModeDisabled, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMode, gotOK := ParseEnabledMode(tt.value)
			assert.Equal(t, tt.wantMode, gotMode)
			assert.Equal(t, tt.wantOK, gotOK)
		})
	}
}

func TestEnabled(t *testing.T) {
	assert.False(t, Enabled(EnabledModeDisabled))
	assert.True(t, Enabled(EnabledModeEnabled))
	assert.True(t, Enabled(EnabledModeParent))
}

func TestFromEnv(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		unsetCIVisibilityEnabledForTest(t)

		gotMode, gotOK := FromEnv()
		assert.Equal(t, EnabledModeDisabled, gotMode)
		assert.False(t, gotOK)
	})

	t.Run("parent", func(t *testing.T) {
		t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "parent")

		gotMode, gotOK := FromEnv()
		assert.Equal(t, EnabledModeParent, gotMode)
		assert.True(t, gotOK)
	})

	t.Run("invalid", func(t *testing.T) {
		t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "enabled")

		gotMode, gotOK := FromEnv()
		assert.Equal(t, EnabledModeDisabled, gotMode)
		assert.False(t, gotOK)
	})
}

func unsetCIVisibilityEnabledForTest(t *testing.T) {
	t.Helper()

	value, ok := internalenv.Lookup(constants.CIVisibilityEnabledEnvironmentVariable)
	require.NoError(t, os.Unsetenv(constants.CIVisibilityEnabledEnvironmentVariable))
	t.Cleanup(func() {
		if ok {
			require.NoError(t, os.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, value))
			return
		}
		require.NoError(t, os.Unsetenv(constants.CIVisibilityEnabledEnvironmentVariable))
	})
}
