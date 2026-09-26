// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
)

func TestSessionSkipIfNoModules(t *testing.T) {
	for _, tc := range []struct {
		name     string
		optIn    bool
		module   bool
		exitCode int
		status   string
	}{
		{"manual-default", false, false, 0, "pass"},
		{"empty-success", true, false, 0, "skip"},
		{"empty-failure", true, false, 1, "fail"},
		{"module-success", true, true, 0, "pass"},
		{"module-failure", true, true, 1, "fail"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockTracer.Reset()
			defer mockTracer.Reset()
			var options []TestSessionStartOption
			if tc.optIn {
				options = append(options, WithTestSessionSkipIfNoModules())
			}
			session := CreateTestSession(options...)
			if tc.module {
				session.GetOrCreateModule("synthetic-module").Close()
			}
			session.Close(tc.exitCode)
			session.Close(1) // Closing twice must not overwrite the result.
			status, ok := session.GetTag(constants.TestStatus)
			require.True(t, ok)
			require.Equal(t, tc.status, status)
			reason, hasReason := session.GetTag(constants.TestSessionEmptyReason)
			skipReason, hasSkipReason := session.GetTag(constants.TestSkipReason)
			if tc.status == "skip" {
				require.Equal(t, "zero_tests", reason)
				require.Equal(t, "No tests or benchmarks ran", skipReason)
			} else {
				require.False(t, hasReason)
				require.False(t, hasSkipReason)
			}
		})
	}
}
