// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
)

type retryProcessManualChildResult struct {
	Version     int    `json:"version"`
	TestName    string `json:"test_name"`
	Attempt     int    `json:"attempt"`
	RetryReason string `json:"retry_reason"`
	Status      string `json:"status"`
	Failed      bool   `json:"failed"`
	Skipped     bool   `json:"skipped"`
}

const manualRetryProcessChildRunFilter = "^TestManualRetryProcess(Selected|Unselected)Child$"

func TestMain(m *testing.M) {
	os.Exit(gotesting.RunM(m))
}

func TestManualRetryProcessChildModeController(t *testing.T) {
	if manualRetryProcessChild() {
		t.Skip("controller runs only in the parent process")
	}

	result := runManualRetryProcessChild(t, "TestManualRetryProcessSelectedChild")
	if result.Version != 1 ||
		result.TestName != "TestManualRetryProcessSelectedChild" ||
		result.Attempt != 1 ||
		result.RetryReason != constants.AutoTestRetriesRetryReason ||
		result.Status != "pass" ||
		result.Failed ||
		result.Skipped {
		t.Fatalf("unexpected child result: %+v", result)
	}
}

func TestManualRetryProcessNoMatchingChildModeController(t *testing.T) {
	if manualRetryProcessChild() {
		t.Skip("controller runs only in the parent process")
	}

	result := runManualRetryProcessChild(t, "TestManualRetryProcessMissingChild")
	if result.Version != 1 ||
		result.TestName != "TestManualRetryProcessMissingChild" ||
		result.Attempt != 1 ||
		result.RetryReason != constants.AutoTestRetriesRetryReason ||
		result.Status != "not_run" ||
		result.Failed ||
		result.Skipped {
		t.Fatalf("unexpected no-matching child result: %+v", result)
	}
}

func TestManualRetryProcessSelectedChild(t *testing.T) {
	if !manualRetryProcessChild() {
		t.Skip("selected child fixture runs only in process retry child mode")
	}
}

func TestManualRetryProcessUnselectedChild(t *testing.T) {
	if manualRetryProcessChild() {
		t.Fatal("unselected test ran in process retry child mode")
	}
}

func runManualRetryProcessChild(t *testing.T, testName string) retryProcessManualChildResult {
	t.Helper()

	tempDir := t.TempDir()
	resultPath := filepath.Join(tempDir, "result.json")
	cmd := exec.Command(os.Args[0], "-test.run="+manualRetryProcessChildRunFilter, "-test.v")
	cmd.Env = append(os.Environ(),
		constants.CIVisibilityInternalRetryProcessChild+"=true",
		constants.CIVisibilityInternalRetryProcessResultPath+"="+resultPath,
		constants.CIVisibilityInternalRetryProcessTestName+"="+testName,
		constants.CIVisibilityInternalRetryProcessAttempt+"=1",
		constants.CIVisibilityInternalRetryProcessReason+"="+constants.AutoTestRetriesRetryReason,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("manual child process failed: %v\n%s", err, output)
	}

	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("reading child result: %v\n%s", err, output)
	}
	var result retryProcessManualChildResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decoding child result: %v\n%s", err, output)
	}
	return result
}

func manualRetryProcessChild() bool {
	return integrations.IsProcessRetryChild()
}
