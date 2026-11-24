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
	"strconv"
	"strings"
	"testing"
)

const adaptiveCIWaitScript = "../../.github/workflows/apps/adaptive-ci-wait.sh"

type adaptiveWaitResult struct {
	sleeps           []string
	nextDelay        string
	remainingRetries string
	pollingInterval  string
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
	runsFile := filepath.Join(tmpDir, "runs.json")
	statusFile := filepath.Join(tmpDir, "status.json")

	if err := os.WriteFile(runsFile, []byte(runsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusFile, []byte(statusJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	for i, cr := range checkRunsSequence {
		p := filepath.Join(tmpDir, fmt.Sprintf("check_runs_%d.json", i))
		if err := os.WriteFile(p, []byte(cr), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Place mock sleep outside binDir so only SLEEP_CMD controls it.
	sleepPath := filepath.Join(tmpDir, "mock-sleep")
	mockSleep := "#!/usr/bin/env bash\necho \"$1\" >> \"$MOCK_SLEEP_LOG\"\n"
	if err := os.WriteFile(sleepPath, []byte(mockSleep), 0o755); err != nil {
		t.Fatal(err)
	}

	mockGH := `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "api" ]]; then shift; fi
if [[ "${1:-}" == "--paginate" ]]; then shift; fi
endpoint="${1:-}"
case "$endpoint" in
  *"/actions/runs"*)
    if [[ ! -s "$MOCK_RUNS_FILE" ]]; then exit 1; fi
    cat "$MOCK_RUNS_FILE"
    ;;
  *"/check-runs"*)
    idx=0
    if [[ -f "$MOCK_CHECK_COUNT_FILE" ]]; then idx="$(cat "$MOCK_CHECK_COUNT_FILE")"; fi
    next=$((idx + 1))
    echo "$next" > "$MOCK_CHECK_COUNT_FILE"
    file="${MOCK_CHECK_DIR}/check_runs_${idx}.json"
    if [[ ! -f "$file" ]]; then
      file="${MOCK_CHECK_DIR}/check_runs_${MOCK_LAST_CHECK_IDX}.json"
    fi
    if [[ ! -s "$file" ]]; then exit 1; fi
    cat "$file"
    ;;
  *"/status"*)
    if [[ ! -s "$MOCK_STATUS_FILE" ]]; then exit 1; fi
    cat "$MOCK_STATUS_FILE"
    ;;
  *)
    exit 1
    ;;
esac
`
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
		"MOCK_SLEEP_LOG=" + sleepLog,
		"MOCK_RUNS_FILE=" + runsFile,
		"MOCK_STATUS_FILE=" + statusFile,
		"MOCK_CHECK_COUNT_FILE=" + ghCountFile,
		"MOCK_CHECK_DIR=" + tmpDir,
		"MOCK_LAST_CHECK_IDX=" + strconv.Itoa(max(0, len(checkRunsSequence)-1)),
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
		if v, ok := strings.CutPrefix(line, "polling-interval-seconds="); ok {
			res.pollingInterval = v
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
	ignoredPendingStatus := `{"statuses": [{"context": "devflow/mergegate", "state": "pending"}, {"context": "dd-gitlab/default-pipeline", "state": "failure"}]}`
	emptyStatus := `{"statuses": []}`

	t.Run("halves wait time from half-time down to 300s threshold", func(t *testing.T) {
		res := runAdaptiveWait(t, runs2400, []string{pendingCheckRuns, pendingCheckRuns}, emptyStatus)
		wantSleeps := []string{"1200", "600"}
		if strings.Join(res.sleeps, ",") != strings.Join(wantSleeps, ",") {
			t.Fatalf("sleeps = %v, want %v", res.sleeps, wantSleeps)
		}
		if res.nextDelay != "300" || res.remainingRetries != "30" || res.pollingInterval != "300" {
			t.Fatalf("got nextDelay=%q remainingRetries=%q pollingInterval=%q, want 300, 30, 300", res.nextDelay, res.remainingRetries, res.pollingInterval)
		}
	})

	t.Run("hands off immediately with remaining retries when all non-ignored checks pass", func(t *testing.T) {
		res := runAdaptiveWait(t, runs2400, []string{passedCheckRuns}, ignoredPendingStatus)
		wantSleeps := []string{"1200"}
		if strings.Join(res.sleeps, ",") != strings.Join(wantSleeps, ",") {
			t.Fatalf("sleeps = %v, want %v", res.sleeps, wantSleeps)
		}
		if res.nextDelay != "0" || res.remainingRetries != "32" {
			t.Fatalf("got nextDelay=%q remainingRetries=%q, want 0 and 32", res.nextDelay, res.remainingRetries)
		}
	})

	t.Run("hands off immediately when a non-ignored check fails alongside pending checks", func(t *testing.T) {
		mixed := `{"check_runs": [
			{"name": "System Tests", "status": "completed", "conclusion": "failure"},
			{"name": "OTelc", "status": "in_progress", "conclusion": null}
		]}`
		res := runAdaptiveWait(t, runs2400, []string{mixed}, emptyStatus)
		wantSleeps := []string{"1200"}
		if strings.Join(res.sleeps, ",") != strings.Join(wantSleeps, ",") {
			t.Fatalf("sleeps = %v, want %v", res.sleeps, wantSleeps)
		}
		if res.nextDelay != "0" || res.remainingRetries != "32" {
			t.Fatalf("got nextDelay=%q remainingRetries=%q, want 0 and 32", res.nextDelay, res.remainingRetries)
		}
	})

	t.Run("falls back to 300s minimum when actions/runs API fails", func(t *testing.T) {
		res := runAdaptiveWait(t, "", []string{pendingCheckRuns}, emptyStatus)
		if len(res.sleeps) != 0 {
			t.Fatalf("sleeps = %v, want none", res.sleeps)
		}
		if res.nextDelay != "300" || res.remainingRetries != "36" {
			t.Fatalf("got nextDelay=%q remainingRetries=%q, want 300 and 36", res.nextDelay, res.remainingRetries)
		}
	})

	t.Run("excludes CURRENT_JOB_NAME when evaluating completion", func(t *testing.T) {
		selfInProgress := `{"check_runs": [
			{"name": "all-jobs-are-green", "status": "in_progress", "conclusion": null},
			{"name": "System Tests", "status": "completed", "conclusion": "success"}
		]}`
		res := runAdaptiveWait(t, runs2400, []string{selfInProgress}, emptyStatus)
		wantSleeps := []string{"1200"}
		if strings.Join(res.sleeps, ",") != strings.Join(wantSleeps, ",") {
			t.Fatalf("sleeps = %v, want %v", res.sleeps, wantSleeps)
		}
		if res.nextDelay != "0" {
			t.Fatalf("got nextDelay=%q, want 0", res.nextDelay)
		}
	})

	t.Run("treats empty non-ignored checks as pending", func(t *testing.T) {
		onlyIgnored := `{"check_runs": [{"name": "check-title", "status": "completed", "conclusion": "success"}]}`
		res := runAdaptiveWait(t, runs2400, []string{onlyIgnored, onlyIgnored}, emptyStatus)
		wantSleeps := []string{"1200", "600"}
		if strings.Join(res.sleeps, ",") != strings.Join(wantSleeps, ",") {
			t.Fatalf("sleeps = %v, want %v", res.sleeps, wantSleeps)
		}
	})

	t.Run("handles multi-page paginated check-runs and statuses", func(t *testing.T) {
		paginatedRuns := `{"check_runs": [{"name": "Unit", "status": "completed", "conclusion": "success"}]}
{"check_runs": [{"name": "System Tests", "status": "completed", "conclusion": "success"}]}`
		paginatedStatuses := `{"statuses": [{"context": "s1", "state": "success"}]}
{"statuses": [{"context": "s2", "state": "success"}]}`
		res := runAdaptiveWait(t, runs2400, []string{paginatedRuns}, paginatedStatuses)
		wantSleeps := []string{"1200"}
		if strings.Join(res.sleeps, ",") != strings.Join(wantSleeps, ",") {
			t.Fatalf("sleeps = %v, want %v", res.sleeps, wantSleeps)
		}
		if res.nextDelay != "0" {
			t.Fatalf("got nextDelay=%q, want 0", res.nextDelay)
		}
	})

	t.Run("treats check-runs or status API failures as pending", func(t *testing.T) {
		res := runAdaptiveWait(t, runs2400, []string{"", ""}, "")
		wantSleeps := []string{"1200", "600"}
		if strings.Join(res.sleeps, ",") != strings.Join(wantSleeps, ",") {
			t.Fatalf("sleeps = %v, want %v", res.sleeps, wantSleeps)
		}
		if res.nextDelay != "300" || res.remainingRetries != "30" {
			t.Fatalf("got nextDelay=%q remainingRetries=%q, want 300 and 30", res.nextDelay, res.remainingRetries)
		}
	})

	t.Run("handles large check-runs payload exceeding 128 KiB without E2BIG", func(t *testing.T) {
		padding := strings.Repeat("x", 4096)
		var items []string
		for i := range 45 {
			items = append(items, fmt.Sprintf(`{"name": "job-%d", "status": "completed", "conclusion": "success", "output": {"text": %q}}`, i, padding))
		}
		largePayload := `{"check_runs": [` + strings.Join(items, ",") + `]}`
		if len(largePayload) <= 131072 {
			t.Fatalf("test payload size %d must exceed 131072 bytes", len(largePayload))
		}
		res := runAdaptiveWait(t, runs2400, []string{largePayload}, emptyStatus)
		wantSleeps := []string{"1200"}
		if strings.Join(res.sleeps, ",") != strings.Join(wantSleeps, ",") {
			t.Fatalf("sleeps = %v, want %v", res.sleeps, wantSleeps)
		}
		if res.nextDelay != "0" {
			t.Fatalf("got nextDelay=%q, want 0", res.nextDelay)
		}
	})
}
