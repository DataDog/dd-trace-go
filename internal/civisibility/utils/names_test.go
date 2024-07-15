// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetModuleAndSuiteName(t *testing.T) {
	pc, _, _, _ := runtime.Caller(0) // Get the program counter of this function
	module, suite := GetModuleAndSuiteName(pc)
	expectedModule := "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
	expectedSuite := "names_test.go"

	assert.True(t, strings.HasPrefix(module, expectedModule))
	assert.Equal(t, expectedSuite, suite)
}

func TestGetStacktrace(t *testing.T) {
	stacktrace := GetStacktrace(0)
	assert.Contains(t, stacktrace, "names_test.go")     // Ensure that the current test file is part of the stack trace
	assert.Contains(t, stacktrace, "TestGetStacktrace") // Ensure that the current test function is part of the stack trace
}

func TestGetStacktraceWithSkip(t *testing.T) {
	stacktrace := getStacktraceHelper()
	assert.Contains(t, stacktrace, "names_test.go")
	assert.NotContains(t, stacktrace, "getStacktraceHelper") // Ensure the helper function is skipped
}

func getStacktraceHelper() string {
	return GetStacktrace(1) // Skip this helper function frame
}
