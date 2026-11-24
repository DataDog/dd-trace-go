// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package actiontest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const adaptiveCIWaitScript = "../../.github/workflows/apps/adaptive-ci-wait.sh"

type adaptiveWaitResult struct {
	sleeps           []string
	nextDelay        string
	remainingRetries string
}

func runAdaptiveWait(
	t *testing.T,
	runsJSON string,
	checkRunsSequence []string,
	statusJSON string,
) adaptiveWaitResult {
	t.Helper()
	for _, bin := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not found: %v", bin, err)
		}
	}

	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	sleepLog := filepath.Join(tmpDir, "sleeps.log")
	ghCountFile := filepath.Join(tmpDir, "check_count")
	outputFile := filepath.Join(tmpDir, "github_output")

	// Write fixture files for the mock gh binary.
	if err := os.WriteFile(filepath.Join(tmpDir, "runs.json"), []byte(runsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "status.json"), []byte(statusJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	for i, cr := range checkRunsSequence {
		p := filepath.Join(tmpDir, fmt.Sprintf("check_runs_%d.json", i))
		if err := os.WriteFile(p, []byte(cr), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	mockSleep := fmt.Sprintf("#!/usr/bin/env bash\necho \"$1\" >> %q\n", sleepLog)
	sleepPath := filepath.Join(binDir, "sleep")
	if err := os.WriteFile(sleepPath, []byte(mockSleep), 0o755); err != nil {
		t.Fatal(err)
	}

	mockGH := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
# Strip optional --paginate flag.
if [[ "${1:-}" == "api" ]]; then shift; fi
if [[ "${1:-}" == "--paginate" ]]; then shift; fi
endpoint="${1:-}"
case "$endpoint" in
  *"/actions/runs"*)
    if [[ ! -s %q ]]; then exit 1; fi
    cat %q
    ;;
  *"/check-runs"*)
    idx=0
    if [[ -f %q ]]; then idx="$(cat %q)"; fi
    next=$((idx + 1))
    echo "$next" > %q
    file=%q"/check_runs_${idx}.json"
    if [[ ! -f "$file" ]]; then
      file=%q"/check_runs_%d.json"
    fi
    cat "$file"
    ;;
  *"/status"*)
    cat %q
    ;;
  *)
    exit 1
    ;;
esac
`,
		filepath.Join(tmpDir, "runs.json"),
		filepath.Join(tmpDir, "runs.json"),
		ghCountFile, ghCountFile, ghCountFile,
		tmpDir,
		tmpDir, max(0, len(checkRunsSequence)-1),
		filepath.Join(tmpDir, "status.json"),
	)
	ghPath := filepath.Join(binDir, "gh")
	if err := os.WriteFile(ghPath, []byte(mockGH), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), "bash", adaptiveCIWaitScript)
	cmd.Env = []string{
		"PATH=" + binDir + ":" + os.Getenv("PATH"),
		"GITHUB_REPOSITORY=DataDog/dd-trace-go",
		"TARGET_SHA=abc123",
		"CURRENT_JOB_NAME=all-jobs-are-green",
		"IGNORED_NAME_PATTERNS=devflow/merge\ndevflow/mergegate\nlabel_issues\ncheck-title\ndd-gitlab/.*\nDDCI Status",
		"GITHUB_OUTPUT=" + outputFile,
		"SLEEP_CMD=" + sleepPath,
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("adaptive-ci-wait.sh failed: %v\noutput:\n%s", err, out)
	}

	var sleeps []string
	if raw, err := os.ReadFile(sleepLog); err == nil {
		trimmed := strings.TrimSpace(string(raw))
		if trimmed != "" {
			sleeps = strings.Split(trimmed, "\n")
		}
	}

	outBytes, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("reading GITHUB_OUTPUT: %v", err)
	}
	res := adaptiveWaitResult{sleeps: sleeps}
	for line := range strings.SplitSeq(strings.TrimSpace(string(outBytes)), "\n") {
		if v, ok := strings.CutPrefix(line, "next-delay-seconds="); ok {
			res.nextDelay = v
		}
		if v, ok := strings.CutPrefix(line, "remaining-retries="); ok {
			res.remainingRetries = v
		}
	}
	return res
}

func TestAdaptiveCIWait(t *testing.T) {
	// 2400s run duration (40 minutes) and an excluded 9000s all-green.yml run.
	runs2400 := `{
		"workflow_runs": [
			{"path": ".github/workflows/all-green.yml", "run_started_at": "2026-11-24T10:00:00Z", "updated_at": "2026-11-24T12:30:00Z"},
			{"path": ".github/workflows/system-tests.yml", "run_started_at": "2026-11-24T10:00:00Z", "updated_at": "2026-11-24T10:40:00Z"}
		]
	}`
	pendingCheckRuns := `{"check_runs": [{"name": "System Tests", "status": "in_progress", "conclusion": null}]}`
	passedCheckRuns := `{"check_runs": [{"name": "System Tests", "status": "completed", "conclusion": "success"}]}`
	failedCheckRuns := `{"check_runs": [{"name": "System Tests", "status": "completed", "conclusion": "failure"}]}`
	ignoredPendingStatus := `{"statuses": [{"context": "devflow/mergegate", "state": "pending"}, {"context": "dd-gitlab/default-pipeline", "state": "failure"}]}`
	emptyStatus := `{"statuses": []}`

	t.Run("halves wait time from half-time down to 300s threshold", func(t *testing.T) {
		res := runAdaptiveWait(t, runs2400, []string{pendingCheckRuns, pendingCheckRuns}, emptyStatus)
		wantSleeps := []string{"1200", "600"}
		if strings.Join(res.sleeps, ",") != strings.Join(wantSleeps, ",") {
			t.Fatalf("sleeps = %v, want %v", res.sleeps, wantSleeps)
		}
		if res.nextDelay != "300" || res.remainingRetries != "30" {
			t.Fatalf("got nextDelay=%q remainingRetries=%q, want 300 and 30", res.nextDelay, res.remainingRetries)
		}
	})

	t.Run("hands off immediately when all non-ignored checks pass", func(t *testing.T) {
		res := runAdaptiveWait(t, runs2400, []string{passedCheckRuns}, ignoredPendingStatus)
		wantSleeps := []string{"1200"}
		if strings.Join(res.sleeps, ",") != strings.Join(wantSleeps, ",") {
			t.Fatalf("sleeps = %v, want %v", res.sleeps, wantSleeps)
		}
		if res.nextDelay != "0" || res.remainingRetries != "1" {
			t.Fatalf("got nextDelay=%q remainingRetries=%q, want 0 and 1", res.nextDelay, res.remainingRetries)
		}
	})

	t.Run("hands off immediately when a non-ignored check fails", func(t *testing.T) {
		res := runAdaptiveWait(t, runs2400, []string{failedCheckRuns}, emptyStatus)
		wantSleeps := []string{"1200"}
		if strings.Join(res.sleeps, ",") != strings.Join(wantSleeps, ",") {
			t.Fatalf("sleeps = %v, want %v", res.sleeps, wantSleeps)
		}
		if res.nextDelay != "0" || res.remainingRetries != "1" {
			t.Fatalf("got nextDelay=%q remainingRetries=%q, want 0 and 1", res.nextDelay, res.remainingRetries)
		}
	})

	t.Run("falls back to 300s minimum when API fails", func(t *testing.T) {
		res := runAdaptiveWait(t, "", []string{pendingCheckRuns}, emptyStatus)
		if len(res.sleeps) != 0 {
			t.Fatalf("sleeps = %v, want none", res.sleeps)
		}
		if res.nextDelay != "300" || res.remainingRetries != "36" {
			t.Fatalf("got nextDelay=%q remainingRetries=%q, want 300 and 36", res.nextDelay, res.remainingRetries)
		}
	})
}
