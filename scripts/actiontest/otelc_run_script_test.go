// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package actiontest

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// otelcWorkflow is the workflow whose "Run Tests" step the tests cover,
// relative to this package directory.
const otelcWorkflow = "../../.github/workflows/otelc.yml"

// prefix is the module path prefix of the integration suites.
const prefix = "github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration"

// stubOtelc is a stub otelc binary. OTELC_STUB_MODE selects the behavior and
// every call is recorded in OTELC_STUB_LOG as one line "otelc <mode> <args>".
//   - ok: emits test2json events that report every package as passing.
//   - fail: emits test2json events where OTELC_STUB_FAIL_PKG fails.
//   - setupfail: emits the plain go output of a failed setup, like otelc does
//     before it reaches the test command, and exits 1.
const stubOtelc = `#!/usr/bin/env bash
mode="${OTELC_STUB_MODE}"
printf 'otelc %s %s\n' "${mode}" "$*" >> "${OTELC_STUB_LOG}"
pkgs=()
for arg in "$@"; do
  case "${arg}" in
    -*|go|test|build) ;;
    *) pkgs+=("${arg}") ;;
  esac
done
event() {
  printf '{"Time":"2026-01-01T00:00:00Z","Action":"%s","Package":"%s"%s}\n' "$1" "$2" "$3"
}
case "${mode}" in
ok)
  for pkg in "${pkgs[@]}"; do
    event start "${pkg}"
    event pass "${pkg}" ',"Test":"TestStub"'
    event pass "${pkg}" ',"Elapsed":1'
  done
  exit 0
  ;;
fail)
  for pkg in "${pkgs[@]}"; do
    event start "${pkg}"
    if [[ "${pkg}" == "${OTELC_STUB_FAIL_PKG}" ]]; then
      event run "${pkg}" ',"Test":"TestStub"'
      event fail "${pkg}" ',"Test":"TestStub"'
      event fail "${pkg}" ',"Elapsed":1'
    else
      event pass "${pkg}" ',"Test":"TestStub"'
      event pass "${pkg}" ',"Elapsed":1'
    fi
  done
  exit 1
  ;;
setupfail)
  for pkg in "${pkgs[@]}"; do
    printf 'FAIL %s [setup failed]\n' "${pkg}"
  done
  printf 'FAIL\n'
  exit 1
  ;;
*)
  echo "stub otelc: unknown mode ${mode}" >&2
  exit 2
  ;;
esac
`

// stubGo is a stub go binary. It answers the two go calls the run script
// makes: "go list" for the has_go_files check, where every package listed in
// GO_STUB_NO_FILES reports no files, and "go tool test2json", which converts
// the plain lines it reads into test2json events, as the real tool does.
const stubGo = `#!/usr/bin/env bash
if [[ "$1" == "list" ]]; then
  pkg="${!#}"
  for nofiles in ${GO_STUB_NO_FILES}; do
    if [[ "${pkg}" == "${nofiles}" ]]; then
      echo 000
      exit 0
    fi
  done
  echo 100
  exit 0
fi
if [[ "$1" == "tool" && "$2" == "test2json" ]]; then
  pkg=""
  prev=""
  for arg in "$@"; do
    if [[ "${prev}" == "-p" ]]; then
      pkg="${arg}"
    fi
    prev="${arg}"
  done
  printf '{"Time":"2026-01-01T00:00:00Z","Action":"start","Package":"%s"}\n' "${pkg}"
  failed=0
  while IFS= read -r line; do
    printf '{"Time":"2026-01-01T00:00:00Z","Action":"output","Package":"%s","Output":"%s\\n"}\n' "${pkg}" "${line}"
    case "${line}" in
    FAIL*) failed=1 ;;
    esac
  done
  if [[ "${failed}" == 1 ]]; then
    printf '{"Time":"2026-01-01T00:00:01Z","Action":"fail","Package":"%s","Elapsed":1}\n' "${pkg}"
  else
    printf '{"Time":"2026-01-01T00:00:01Z","Action":"pass","Package":"%s","Elapsed":1}\n' "${pkg}"
  fi
  exit 0
fi
echo "stub go: unsupported call: $*" >&2
exit 1
`

// testEvent is one line of the test2json stream the run script must keep on
// stdout, because gotestsum turns exactly these events into the JUnit report.
type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

// extractRunScript returns the script that the "Run Tests" step of
// otelc.yml writes to $RUNNER_TEMP/run-otelc-tests.sh, with the YAML block
// indentation removed.
func extractRunScript(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(otelcWorkflow)
	require.NoError(t, err)
	lines := strings.Split(string(data), "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, `<<'EOF'`) {
			require.Equal(t, -1, start, "otelc.yml must have exactly one heredoc")
			start = i + 1
		}
	}
	require.NotEqual(t, -1, start, "the run script heredoc was not found in otelc.yml")
	script := make([]string, 0, 64)
	for _, line := range lines[start:] {
		if strings.TrimSpace(line) == "EOF" {
			break
		}
		script = append(script, line)
	}
	require.NotEmpty(t, script, "the heredoc is empty")
	indent := script[0][:len(script[0])-len(strings.TrimLeft(script[0], " "))]
	for i, line := range script {
		require.Truef(t, line == "" || strings.HasPrefix(line, indent),
			"line %d breaks the heredoc indent: %q", i, line)
		if line != "" {
			script[i] = strings.TrimPrefix(line, indent)
		}
	}
	return strings.Join(script, "\n")
}

// runStepResult is what a run of the extracted script produced.
type runStepResult struct {
	rc     int
	stdout []testEvent
	stderr string
	calls  []string
}

// runRunScript runs the extracted run script with the stub otelc and go on
// PATH. shared and serial are the package lists of the step, noGoFiles are the
// packages that has_go_files skips, and otelcMode selects the stub behavior.
func runRunScript(t *testing.T, otelcMode, failPkg string, shared, serial, noGoFiles []string) runStepResult {
	t.Helper()
	for _, tool := range []string{"bash", "tr", "grep", "cat"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not available", tool)
		}
	}
	binDir := t.TempDir()
	for name, content := range map[string]string{"otelc": stubOtelc, "go": stubGo} {
		require.NoError(t, os.WriteFile(filepath.Join(binDir, name), []byte(content), 0o755))
	}
	runnerTemp := t.TempDir()
	scriptPath := filepath.Join(runnerTemp, "run-otelc-tests.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte(extractRunScript(t)), 0o755))
	calls := filepath.Join(t.TempDir(), "calls.log")
	require.NoError(t, os.WriteFile(calls, nil, 0o644))

	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = runnerTemp
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"RUNNER_TEMP="+runnerTemp,
		"PACKAGE_NAMES="+strings.Join(shared, " "),
		"SERIAL_PACKAGE_NAMES="+strings.Join(serial, " "),
		"OTELC_STUB_MODE="+otelcMode,
		"OTELC_STUB_LOG="+calls,
		fmt.Sprintf("OTELC_STUB_FAIL_PKG=%s", failPkg),
		fmt.Sprintf("GO_STUB_NO_FILES=%s", strings.Join(noGoFiles, " ")),
	)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	rc := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		rc = exitErr.ExitCode()
	} else {
		require.NoError(t, err)
	}

	// Stdout must be a pure test2json stream in every path: a line that is
	// not valid JSON makes gotestsum report an invalid TestEvent and can
	// cost it the whole JUnit report.
	var events []testEvent
	for i, line := range strings.Split(stdout.String(), "\n") {
		if line == "" {
			continue
		}
		require.Truef(t, strings.HasPrefix(line, "{"),
			"stdout line %d is not test2json: %q", i, line)
		var event testEvent
		require.NoErrorf(t, json.Unmarshal([]byte(line), &event),
			"stdout line %d is not valid JSON: %q", i, line)
		events = append(events, event)
	}

	var recorded []string
	for _, line := range strings.Split(readFile(t, calls), "\n") {
		if line != "" {
			recorded = append(recorded, line)
		}
	}
	return runStepResult{rc: rc, stdout: events, stderr: stderr.String(), calls: recorded}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// suiteActions returns the package events of one suite: the events that make
// gotestsum write the suite into the JUnit report.
func suiteActions(events []testEvent, pkg string) []string {
	var actions []string
	for _, event := range events {
		if event.Package == pkg && event.Test == "" {
			actions = append(actions, event.Action)
		}
	}
	return actions
}

// The stub scenarios use the suites that use maintest.Build: chi, chi.v5 and
// gin build package main apps, and aws.v1 is the serial exception of the
// workflow. redigo stands in for a suite the platform cannot build.
func sharedSuites() []string {
	return []string{prefix + "/chi", prefix + "/chi.v5", prefix + "/gin"}
}

func serialSuite() []string {
	return []string{prefix + "/aws.v1"}
}

// TestOtelcRunScriptSuccess covers the healthy path: one shared invocation
// for the shared suites, one invocation for the serial suite, a skip notice
// for a suite with no files for the platform, and rc=0.
func TestOtelcRunScriptSuccess(t *testing.T) {
	noFiles := []string{prefix + "/redigo"}
	// PACKAGE_NAMES carries the full workflow list, including the suite that
	// has no files for this platform; the script must skip that one.
	result := runRunScript(t, "ok", "", append(sharedSuites(), noFiles...), serialSuite(), noFiles)

	require.Equal(t, 0, result.rc, "stderr:\n%s", result.stderr)
	for _, pkg := range append(sharedSuites(), serialSuite()...) {
		require.Containsf(t, suiteActions(result.stdout, pkg), "pass",
			"suite %s must report in the test2json stream", pkg)
	}
	require.Empty(t, suiteActions(result.stdout, prefix+"/redigo"),
		"a suite with no files must not report")
	require.Contains(t, result.stderr, "Skipping "+prefix+"/redigo: no Go files for this platform")

	require.Len(t, result.calls, 2, "the stub otelc calls were:\n%s", strings.Join(result.calls, "\n"))
	sharedCall, serialCall := result.calls[0], result.calls[1]
	require.Contains(t, sharedCall, "ok")
	for _, pkg := range sharedSuites() {
		require.Contains(t, sharedCall, pkg, "the shared invocation must include %s", pkg)
	}
	require.NotContains(t, sharedCall, prefix+"/redigo", "a skipped suite must not run")
	require.Contains(t, serialCall, "ok")
	require.Contains(t, serialCall, prefix+"/aws.v1")
	require.NotContains(t, serialCall, prefix+"/chi", "the serial invocation must run the serial suite only")
}

// TestOtelcRunScriptTestFailure covers a suite whose tests fail: the shared
// invocation emits test2json events and fails, the script relays the events
// unchanged, does not rerun anything, and the step fails.
func TestOtelcRunScriptTestFailure(t *testing.T) {
	failPkg := prefix + "/chi"
	result := runRunScript(t, "fail", failPkg, sharedSuites(), serialSuite(), nil)

	require.Equal(t, 1, result.rc, "stderr:\n%s", result.stderr)
	require.Contains(t, suiteActions(result.stdout, failPkg), "fail",
		"the failing suite must report its failure")
	for _, pkg := range []string{prefix + "/chi.v5", prefix + "/gin", prefix + "/aws.v1"} {
		require.Containsf(t, suiteActions(result.stdout, pkg), "pass",
			"the other suites must report, including %s", pkg)
	}
	require.Len(t, result.calls, 2, "a test failure must not trigger the per-suite fallback:\n%s",
		strings.Join(result.calls, "\n"))
}

// invokedWith reports whether a recorded stub call ran pkg as an argument.
// Package paths are prefixes of each other (chi and chi.v5), so the check
// compares whole arguments instead of substrings.
func invokedWith(call, pkg string) bool {
	fields := strings.Fields(call)
	// Skip "otelc" and the mode; the rest are the command arguments.
	for _, field := range fields[2:] {
		if field == pkg {
			return true
		}
	}
	return false
}

// TestOtelcRunScriptSetupFailure covers the setup failure that the review
// reproduced: the shared invocation aborts with plain go output and no
// test2json events, so every suite would be lost from the JUnit report. The
// script must still fail, but rerun the suites one invocation at a time and
// convert the output, so every suite reports.
func TestOtelcRunScriptSetupFailure(t *testing.T) {
	result := runRunScript(t, "setupfail", "", sharedSuites(), serialSuite(), nil)

	require.Equal(t, 1, result.rc, "the step must still fail, stderr:\n%s", result.stderr)
	require.NotContains(t, result.stderr, "stub otelc: unknown mode")
	require.Contains(t, result.stderr, "::group::otelc output for shared")
	require.Contains(t, result.stderr, prefix+"/chi [setup failed]",
		"the plain output of the failed shared run must stay in the stderr group")

	for _, pkg := range append(sharedSuites(), serialSuite()...) {
		actions := suiteActions(result.stdout, pkg)
		require.Containsf(t, actions, "fail",
			"every suite must report through the per-suite fallback, %s reported %v", pkg, actions)
	}

	require.Len(t, result.calls, 6, "the stub otelc calls were:\n%s", strings.Join(result.calls, "\n"))
	sharedCall := result.calls[0]
	require.Contains(t, sharedCall, "setupfail")
	for _, pkg := range sharedSuites() {
		require.Contains(t, sharedCall, pkg)
	}
	// Three fallback invocations for the shared suites, one for the serial
	// invocation, and one fallback for the serial suite.
	for _, pkg := range sharedSuites() {
		count := 0
		for _, call := range result.calls[1:] {
			if invokedWith(call, pkg) {
				count++
			}
		}
		require.Equalf(t, 1, count, "suite %s must run exactly one fallback invocation", pkg)
	}
	serialCalls := 0
	for _, call := range result.calls {
		if invokedWith(call, prefix+"/aws.v1") {
			serialCalls++
		}
	}
	require.Equal(t, 2, serialCalls,
		"the serial suite must keep its own invocation and run one fallback")
}
