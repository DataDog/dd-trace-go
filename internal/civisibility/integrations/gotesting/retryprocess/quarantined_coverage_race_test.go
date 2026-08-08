// Unless explicitly stated otherwise all files in this repository are licensed under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build race

package retryprocess

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
)

const processRetryQuarantinedCoverageOutputSentinel = "quarantined coverage child output sentinel"

func TestProcessRetryQuarantinedCoverageIncludesIsolatedFirstAttempt(t *testing.T) {
	if !processRetryFixtureScenarioEnabled() && !processRetryFixtureChild() {
		skipProcessRetryFixtureChildLaunchIneligible(t, "quarantined coverage")
		if testing.CoverMode() == "" {
			t.Skip("quarantined coverage fixture runs only with Go coverage enabled")
		}
		coveragePath := filepath.Join(t.TempDir(), "quarantined.out")
		resultPath := filepath.Join(t.TempDir(), "result.json")
		cmd := exec.Command(os.Args[0],
			"-test.run=^TestProcessRetryQuarantinedCoverageIncludesIsolatedFirstAttempt$",
			"-test.coverprofile="+coveragePath,
			"-test.v",
		)
		cmd.Env = processRetryScenarioEnvironment(
			processRetryQuarantinedCoverageEnv+"=true",
			processRetryQuarantinedCoverageResultEnv+"="+resultPath,
		)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("quarantined coverage subprocess failed: %v\n%s", err, output)
		}
		if count := bytes.Count(output, []byte("=== RUN   TestProcessRetryQuarantinedCoverageIncludesIsolatedFirstAttempt")); count != 1 {
			t.Fatalf("quarantined test runner header count = %d, want 1\n%s", count, output)
		}
		if !bytes.Contains(output, []byte(processRetryQuarantinedCoverageOutputSentinel)) {
			t.Fatalf("quarantined child output was lost:\n%s", output)
		}
		profile, err := os.ReadFile(coveragePath)
		if err != nil {
			t.Fatal(err)
		}
		file, line := processRetryCoverageChildMarker()
		if count, ok := processRetryCoverageCountForLine(profile, file, line); !ok || count == 0 {
			t.Fatalf("isolated first-attempt coverage count = %d, found = %t; want positive", count, ok)
		}
		assertProcessRetryAttemptCoverage(t, resultPath)
		return
	}
	if !processRetryFixtureChild() {
		t.Fatal("quarantined first attempt ran in the parent process")
	}

	processRetryCoverageChildMarker()
	fmt.Println(processRetryQuarantinedCoverageOutputSentinel)
	var attempt int
	gotesting.GetTest(t).Run("attempt-to-fix", func(*testing.T) {
		attempt++
		processRetryCoverageParentMarker()
		if attempt > 1 {
			processRetryCoverageChildMarker()
		}
	})
}

func assertProcessRetryAttemptCoverage(t *testing.T, resultPath string) {
	t.Helper()
	payload, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Subtests []struct {
			Attempt  int `json:"attempt"`
			Coverage []struct {
				Name   string `json:"name"`
				Bitmap []byte `json:"bitmap"`
			} `json:"coverage"`
		} `json:"subtests"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Subtests) != 3 {
		t.Fatalf("attempt-to-fix result count = %d, want 3", len(result.Subtests))
	}
	for attempt, subtest := range result.Subtests {
		if subtest.Attempt != attempt || len(subtest.Coverage) == 0 {
			t.Fatalf("attempt %d result = %+v, want matching index and non-empty coverage", attempt, subtest)
		}
	}
}
