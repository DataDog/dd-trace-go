// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package envconfig contains CI Visibility-specific environment parsing helpers.
package envconfig

import internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"

// EnabledMode is the parsed mode for DD_CIVISIBILITY_ENABLED.
type EnabledMode = internalconfig.CIVisibilityEnabledMode

const (
	// EnabledModeDisabled means CI Visibility is disabled for this process.
	EnabledModeDisabled = internalconfig.CIVisibilityEnabledModeDisabled

	// EnabledModeEnabled means CI Visibility is enabled and may propagate to children.
	EnabledModeEnabled = internalconfig.CIVisibilityEnabledModeEnabled

	// EnabledModeParent means CI Visibility is enabled for this process only.
	EnabledModeParent = internalconfig.CIVisibilityEnabledModeParent
)

const (
	// EnabledModeParentValue is the DD_CIVISIBILITY_ENABLED value that enables CI Visibility only for the current process.
	EnabledModeParentValue = "parent"
)

// ParseEnabledMode parses DD_CIVISIBILITY_ENABLED. It accepts normal Go boolean values plus "parent".
func ParseEnabledMode(value string) (EnabledMode, bool) {
	parsed, err := internalconfig.ParseCIVisibilityEnabledMode(value)
	if err != nil {
		return EnabledModeDisabled, false
	}
	return parsed, true
}

// Enabled reports whether the parsed mode enables CI Visibility in this process.
func Enabled(mode EnabledMode) bool {
	return mode == EnabledModeEnabled || mode == EnabledModeParent
}

// FromEnv reads and parses DD_CIVISIBILITY_ENABLED from the process environment.
func FromEnv() (EnabledMode, bool) {
	return internalconfig.ResolveCIVisibilityEnabledMode()
}
