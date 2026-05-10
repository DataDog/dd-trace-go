// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package envconfig contains CI Visibility-specific environment parsing helpers.
package envconfig

import (
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	internalenv "github.com/DataDog/dd-trace-go/v2/internal/env"
)

// EnabledMode is the parsed mode for DD_CIVISIBILITY_ENABLED.
type EnabledMode int

const (
	// EnabledModeDisabled means CI Visibility is disabled for this process.
	EnabledModeDisabled EnabledMode = iota

	// EnabledModeEnabled means CI Visibility is enabled and may propagate to children.
	EnabledModeEnabled

	// EnabledModeParent means CI Visibility is enabled for this process only.
	EnabledModeParent
)

const (
	// EnabledModeParentValue is the DD_CIVISIBILITY_ENABLED value that enables CI Visibility only for the current process.
	EnabledModeParentValue = "parent"
)

// ParseEnabledMode parses DD_CIVISIBILITY_ENABLED. It accepts normal Go boolean values plus "parent".
func ParseEnabledMode(value string) (EnabledMode, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == EnabledModeParentValue {
		return EnabledModeParent, true
	}

	parsed, err := strconv.ParseBool(normalized)
	if err != nil {
		return EnabledModeDisabled, false
	}
	if parsed {
		return EnabledModeEnabled, true
	}
	return EnabledModeDisabled, true
}

// Enabled reports whether the parsed mode enables CI Visibility in this process.
func Enabled(mode EnabledMode) bool {
	return mode == EnabledModeEnabled || mode == EnabledModeParent
}

// FromEnv reads and parses DD_CIVISIBILITY_ENABLED from the process environment.
func FromEnv() (EnabledMode, bool) {
	value, ok := internalenv.Lookup(constants.CIVisibilityEnabledEnvironmentVariable)
	if !ok {
		return EnabledModeDisabled, false
	}
	return ParseEnabledMode(value)
}
