// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package subtests

import (
	"os"
	"testing"

	gotesting "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
)

// TestSubtestManagement exercises multiple subtests so the Datadog Go testing
// instrumentation can attach management directives at different hierarchy levels.
func TestSubtestManagement(t *testing.T) {
	gt := gotesting.GetTest(t)

	gt.Run("SubDisabled", func(t *testing.T) {
		t.Log("subtest intentionally disabled by management directives")
	})

	gt.Run("SubQuarantined", func(t *testing.T) {
		t.Log("subtest intentionally quarantined by management directives")
	})

	gt.Run("SubAttemptFix", func(t *testing.T) {
	})

	gt.Run("SubAttemptFixParallel", func(t *testing.T) {
		// Run this attempt-to-fix wrapper in parallel when the scenario requests it.
		if os.Getenv(parallelToggleEnv) == "1" {
			t.Parallel()
		}
	})
}

// TestParentDisabled validates fallback behaviour when only the parent test is
// configured by management data. The child subtest should inherit the disabled
// directive even without an explicit entry.
func TestParentDisabled(t *testing.T) {
	gotesting.GetTest(t).Run("Child", func(t *testing.T) {
		t.Log("child inherits disabled directive from parent")
	})
}
