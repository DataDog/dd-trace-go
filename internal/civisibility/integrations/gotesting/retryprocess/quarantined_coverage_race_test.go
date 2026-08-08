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
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
)

const (
	processRetryQuarantinedCoverageOutputStart = "quarantined coverage child output start"
	processRetryQuarantinedCoverageOutputEnd   = "quarantined coverage child output end"
	processRetryQuarantinedCoverageErrorStart  = "quarantined coverage child error start"
	processRetryQuarantinedCoverageErrorEnd    = "quarantined coverage child error end"
)

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
			t.Errorf("quarantined test runner header count = %d, want 1\n%s", count, output)
		}
		assertProcessRetryAttemptCoverage(t, resultPath)
		if !bytes.Contains(output, []byte(processRetryQuarantinedCoverageOutputStart)) ||
			!bytes.Contains(output, []byte(processRetryQuarantinedCoverageOutputEnd)) ||
			!bytes.Contains(output, []byte(processRetryQuarantinedCoverageErrorStart)) ||
			!bytes.Contains(output, []byte(processRetryQuarantinedCoverageErrorEnd)) {
			t.Fatalf("quarantined child output was truncated (bytes = %d)", len(output))
		}
		profile, err := os.ReadFile(coveragePath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(profile, []byte("mode: atomic\n")) {
			t.Errorf("quarantined coverage mode is not atomic:\n%s", profile)
		}
		file, line := processRetryCoverageChildMarker()
		if count, ok := processRetryCoverageCountForLine(profile, file, line); !ok || count != 3 {
			t.Errorf("isolated first-attempt coverage count = %d, found = %t; want 3", count, ok)
		}
		return
	}
	if !processRetryFixtureChild() {
		t.Fatal("quarantined first attempt ran in the parent process")
	}

	processRetryCoverageChildMarker()
	fmt.Println(processRetryQuarantinedCoverageOutputStart)
	fmt.Println(strings.Repeat("x", 40*1024))
	fmt.Println(processRetryQuarantinedCoverageOutputEnd)
	fmt.Fprintln(os.Stderr, processRetryQuarantinedCoverageErrorStart)
	fmt.Fprintln(os.Stderr, strings.Repeat("y", 40*1024))
	fmt.Fprintln(os.Stderr, processRetryQuarantinedCoverageErrorEnd)
	var attempt int
	gotesting.GetTest(t).Run("attempt-to-fix", func(*testing.T) {
		attempt++
		processRetryCoverageParentMarker()
		if attempt > 1 {
			processRetryCoverageChildMarker()
		}
	})
	gotesting.GetTest(t).Run("quarantined", func(t *testing.T) {
		t.Error("quarantined subtest failure")
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
			TestName string `json:"test_name"`
			Attempt  int    `json:"attempt"`
			Status   string `json:"status"`
			Failed   bool   `json:"failed"`
			Skipped  bool   `json:"skipped"`
			Coverage []struct {
				Name   string `json:"name"`
				Bitmap []byte `json:"bitmap"`
			} `json:"coverage"`
		} `json:"subtests"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Subtests) != 4 {
		t.Fatalf("managed subtest result count = %d, want 4", len(result.Subtests))
	}
	attempt := 0
	for _, subtest := range result.Subtests {
		switch {
		case strings.HasSuffix(subtest.TestName, "/attempt-to-fix"):
			if subtest.Attempt != attempt || len(subtest.Coverage) == 0 {
				t.Fatalf("attempt %d result = %+v, want matching index and non-empty coverage", attempt, subtest)
			}
			attempt++
		case strings.HasSuffix(subtest.TestName, "/quarantined"):
			if subtest.Status != "fail" || !subtest.Failed || subtest.Skipped || len(subtest.Coverage) == 0 {
				t.Fatalf("quarantined result = %+v, want raw failure with coverage", subtest)
			}
		default:
			t.Fatalf("unexpected managed subtest result: %+v", subtest)
		}
	}
	if attempt != 3 {
		t.Fatalf("attempt-to-fix result count = %d, want 3", attempt)
	}
}
