// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package retryprocess

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
)

var forcedRunChildLaunchRuns atomic.Int32
var coverageFallbackRuns atomic.Int32
var runSelectorSubtestRuns atomic.Int32
var skipSelectorSubtestRuns atomic.Int32
var processExitRuns atomic.Int32
var malformedJSONRuns atomic.Int32
var timeoutRuns atomic.Int32
var outputTimeoutRuns atomic.Int32
var descendantCleanupRuns atomic.Int32
var transportIsolationRuns atomic.Int32
var processRetryBenchmarkRuns atomic.Int32

const (
	processRetryChildLogSentinel         = "process-retry-child-output-sentinel"
	processRetryProcessExitLogSentinel   = "process-retry-process-exit-output-sentinel"
	processRetryMalformedJSONLogSentinel = "process-retry-malformed-json-output-sentinel"
	processRetryTimeoutLogSentinel       = "process-retry-timeout-output-sentinel"
	processRetryOutputTimeoutLogSentinel = "process-retry-output-timeout-child-sentinel"
	processRetryDescendantLogSentinel    = "process-retry-descendant-output-sentinel"
	processRetryDescendantHelperLifetime = 30 * time.Second
)

func skipProcessRetryFixtureChildLaunchIneligible(t *testing.T, name string) {
	t.Helper()
	if !gotesting.ProcessRetryContainmentSupported() {
		t.Skipf("process retry %s fixture requires process-tree containment", name)
	}
	if testing.CoverMode() != "" {
		t.Skipf("process retry %s fixture is intentionally ineligible while Go coverage is active", name)
	}
}

const (
	processRetrySelectorFixtureEnv           = "PROCESS_RETRY_SELECTOR_FIXTURE"
	processRetryProcessExitFixtureEnv        = "PROCESS_RETRY_PROCESS_EXIT_FIXTURE"
	processRetryMalformedJSONFixtureEnv      = "PROCESS_RETRY_MALFORMED_JSON_FIXTURE"
	processRetryTimeoutFixtureEnv            = "PROCESS_RETRY_TIMEOUT_FIXTURE"
	processRetryOutputTimeoutFixtureEnv      = "PROCESS_RETRY_OUTPUT_TIMEOUT_FIXTURE"
	processRetryDescendantCleanupFixtureEnv  = "PROCESS_RETRY_DESCENDANT_CLEANUP_FIXTURE"
	processRetryDescendantHelperEnv          = "PROCESS_RETRY_DESCENDANT_HELPER"
	processRetryDescendantLivenessPathEnv    = "PROCESS_RETRY_DESCENDANT_LIVENESS_PATH"
	processRetryDescendantIndependentPathEnv = "PROCESS_RETRY_DESCENDANT_INDEPENDENT_LIVENESS_PATH"
	processRetryTransportIsolationEnv        = "PROCESS_RETRY_TRANSPORT_ISOLATION_FIXTURE"
	processRetryTransportProbeEnv            = "PROCESS_RETRY_TRANSPORT_PROBE"
	processRetryScenarioEnv                  = "PROCESS_RETRY_FIXTURE_SCENARIO"
	processRetryControllerProbeEnv           = "PROCESS_RETRY_CONTROLLER_PROBE"
	processRetryControllerProbePathEnv       = "PROCESS_RETRY_CONTROLLER_PROBE_PATH"
	processRetryBenchmarkExecutionModeEnv    = "PROCESS_RETRY_BENCHMARK_EXECUTION_MODE"
	processRetryStartupFixtureEnv            = "PROCESS_RETRY_STARTUP_FIXTURE"
	processRetryStartupRerunFileEnv          = "PROCESS_RETRY_STARTUP_RERUN_FILE"
	processRetryStartupConflictFileEnv       = "PROCESS_RETRY_STARTUP_CONFLICT_FILE"
	processRetryStartupConflictMarkerEnv     = "PROCESS_RETRY_STARTUP_CONFLICT_MARKER_FILE"
)

var (
	startupRerunRuns    atomic.Int32
	startupConflictRuns atomic.Int32
	startupConflictFile *os.File
)

func init() {
	if path := processRetryFixtureEnv(processRetryStartupRerunFileEnv); path != "" {
		appendStartupFixtureLine(path, "init")
	}
	if path := processRetryFixtureEnv(processRetryStartupConflictFileEnv); path != "" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			if processRetryFixtureChild() {
				appendStartupFixtureLine(processRetryFixtureEnv(processRetryStartupConflictMarkerEnv), "child_conflict")
			} else {
				appendStartupFixtureLine(processRetryFixtureEnv(processRetryStartupConflictMarkerEnv), "parent_conflict")
			}
			return
		}
		startupConflictFile = file
	}
}

func BenchmarkProcessRetryExecutionMode(b *testing.B) {
	for _, mode := range []string{"in_process", "process"} {
		b.Run(mode, func(b *testing.B) {
			if mode == "process" && !gotesting.ProcessRetryContainmentSupported() {
				b.Skip("process retry benchmark requires process-tree containment")
			}
			b.ResetTimer()
			for range b.N {
				cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryBenchmarkFixture$", "-test.count=1")
				cmd.Env = processRetryScenarioEnvironment(processRetryBenchmarkExecutionModeEnv + "=" + mode)
				output, err := cmd.CombinedOutput()
				if err != nil {
					b.Fatalf("%s retry benchmark subprocess failed: %v\n%s", mode, err, output)
				}
			}
			b.StopTimer()
			b.ReportMetric(1, "retries/op")
			if mode == "process" {
				b.ReportMetric(1, "retry-child-processes/op")
			} else {
				b.ReportMetric(0, "retry-child-processes/op")
			}
		})
	}
}

func TestProcessRetryBenchmarkFixture(t *testing.T) {
	mode := processRetryFixtureEnv(processRetryBenchmarkExecutionModeEnv)
	if mode == "" {
		t.Skip("benchmark fixture runs only from its benchmark subprocess")
	}
	if processRetryFixtureChild() {
		if mode != "process" {
			t.Fatalf("%s retry unexpectedly launched a child process", mode)
		}
		return
	}

	run := processRetryBenchmarkRuns.Add(1)
	if run == 1 {
		t.Fail()
		return
	}
	if mode == "process" {
		t.Fatal("process retry ran in the parent process")
	}
	if mode != "in_process" {
		t.Fatalf("unknown retry execution mode %q", mode)
	}
}

func TestProcessRetryControllersAreNotRetried(t *testing.T) {
	if processRetryFixtureEnv(processRetryControllerProbeEnv) == "true" {
		path := processRetryFixtureEnv(processRetryControllerProbePathEnv)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("x"); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		t.Fatal("controller probe fails intentionally")
	}

	path := filepath.Join(t.TempDir(), "controller-runs")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryControllersAreNotRetried$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		processRetryControllerProbeEnv+"=true",
		processRetryControllerProbePathEnv+"="+path,
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("controller probe unexpectedly passed:\n%s", output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "x" {
		t.Fatalf("controller probe was retried, got run markers %q", data)
	}
}

func TestProcessRetryFocusedMainAssertionsController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "focused main assertions")
	const testName = "TestProcessRetryITRForcedRun"
	runProcessRetryFixtureSubprocess(t, testName, []string{"-test.run=^" + testName + "$", "-test.v"})
}

//dd:test.unskippable
func TestProcessRetryITRForcedRun(t *testing.T) {
	if !processRetryFixtureScenarioEnabled() && !processRetryFixtureChild() {
		t.Skip("process retry fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if forcedRunChildLaunchRuns.Load() != 0 {
			t.Fatalf("process retry child inherited forced-run parent count: %d", forcedRunChildLaunchRuns.Load())
		}
		fmt.Println(processRetryChildLogSentinel)
		return
	}
	if testing.CoverMode() != "" {
		t.Skip("process retry is intentionally ineligible while Go coverage is active")
	}
	if forcedRunChildLaunchRuns.Add(1) == 1 {
		t.Fatal("first forced-run parent execution must fail to trigger process retry")
	}
	t.Fatalf("forced-run retry ran in the parent process with run count %d", forcedRunChildLaunchRuns.Load())
}

func TestProcessRetryCoverageFallback(t *testing.T) {
	if !processRetryFixtureScenarioEnabled() && !processRetryFixtureChild() {
		if testing.CoverMode() == "" {
			t.Skip("coverage fallback fixture runs only with Go coverage enabled")
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryCoverageFallback$", "-test.v")
		cmd.Env = processRetryScenarioEnvironment()
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("coverage fallback subprocess failed: %v\n%s", err, output)
		}
		return
	}
	if processRetryFixtureChild() {
		t.Fatal("process retry child launched while Go coverage was active")
	}
	if coverageFallbackRuns.Add(1) == 1 {
		t.Fatal("first coverage execution must fail and retry in-process")
	}
}

func TestProcessRetryRunSelectorController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "selector")
	runProcessRetryFixtureSubprocess(t, "run-selector", []string{
		"-test.run=^(TestProcessRetryRunSelectorParent|Other/Name)$/(OnlyThisSubtest)", "-test.v",
	}, processRetrySelectorFixtureEnv+"=true")
}

func TestProcessRetrySkipSelectorController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "selector")
	runProcessRetryFixtureSubprocess(t, "skip-selector", []string{
		"-test.run=^TestProcessRetrySkipSelectorParent$",
		"-test.skip=^TestProcessRetrySkipSelectorParent/SkippedSubtest$",
		"-test.v",
	}, processRetrySelectorFixtureEnv+"=true")
}

func TestProcessRetryProcessExitController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "process-exit")
	runProcessRetryFixtureSubprocess(t, "process-exit", []string{"-test.run=^TestProcessRetryProcessExitParent$", "-test.v"}, processRetryProcessExitFixtureEnv+"=true")
}

func TestProcessRetryMalformedJSONController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "malformed-json")
	runProcessRetryFixtureSubprocess(t, "malformed-json", []string{"-test.run=^TestProcessRetryMalformedJSONParent$", "-test.v"}, processRetryMalformedJSONFixtureEnv+"=true")
}

func TestProcessRetryTimeoutController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "timeout")
	runProcessRetryFixtureSubprocess(t, "timeout", []string{"-test.run=^TestProcessRetryTimeoutParent$", "-test.v"},
		processRetryTimeoutFixtureEnv+"=true",
		constants.CIVisibilityRetryProcessTimeoutEnvironmentVariable+"=1s",
	)
}

func TestProcessRetryOutputTimeoutController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "output-timeout")
	runProcessRetryFixtureSubprocess(t, "output-timeout", []string{"-test.run=^TestProcessRetryOutputTimeoutParent$", "-test.v"},
		processRetryOutputTimeoutFixtureEnv+"=true",
		constants.CIVisibilityRetryProcessTimeoutEnvironmentVariable+"=1s",
	)
}

func TestProcessRetryDescendantCleanupController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "descendant-cleanup")
	livenessPath := filepath.Join(t.TempDir(), "descendant-liveness")
	independentLivenessPath := filepath.Join(t.TempDir(), "descendant-independent-liveness")
	args := []string{"-test.run=^TestProcessRetryDescendantCleanupParent$", "-test.v"}
	env := []string{
		processRetryDescendantCleanupFixtureEnv + "=true",
		processRetryDescendantLivenessPathEnv + "=" + livenessPath,
		processRetryDescendantIndependentPathEnv + "=" + independentLivenessPath,
	}
	started := time.Now()
	runProcessRetryFixtureSubprocess(t, "descendant-cleanup", args, env...)
	if elapsed := time.Since(started); elapsed >= processRetryDescendantHelperLifetime {
		t.Fatalf("process retry waited for descendant helpers to exit: %s", elapsed)
	}
	for _, path := range []string{livenessPath, independentLivenessPath} {
		address := processRetryDescendantAddress(t, path)
		waitForProcessRetryDescendantListenerClosed(t, address)
	}
}

func processRetryDescendantAddress(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			address := string(data)
			if _, _, err := net.SplitHostPort(address); err == nil {
				return address
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("process retry descendant helper did not publish a valid listener address: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForProcessRetryDescendantListenerClosed(t *testing.T, address string) {
	t.Helper()
	const stableFailuresRequired = 3
	consecutiveFailures := 0
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err != nil {
			consecutiveFailures++
			if consecutiveFailures >= stableFailuresRequired {
				return
			}
		} else {
			consecutiveFailures = 0
			_ = conn.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("process retry descendant helper survived cleanup: listener %s did not remain closed", address)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestProcessRetryTransportIsolationController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "transport-isolation")
	runProcessRetryFixtureSubprocess(t, "transport-isolation", []string{"-test.run=^TestProcessRetryTransportIsolationParent$", "-test.v"}, processRetryTransportIsolationEnv+"=true")
}

func runProcessRetryFixtureSubprocess(t *testing.T, name string, args []string, environment ...string) []byte {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = processRetryScenarioEnvironment(environment...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s process retry fixture failed: %v\n%s", name, err, output)
	}
	return output
}

func TestProcessRetryRunSelectorParent(t *testing.T) {
	if processRetryFixtureEnv(processRetrySelectorFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("selector fixture runs only from its controller subprocess")
	}
	t.Run("OnlyThisSubtest", func(t *testing.T) {
		if processRetryFixtureChild() {
			if runSelectorSubtestRuns.Load() != 0 {
				t.Fatalf("process retry child inherited parent run selector count: %d", runSelectorSubtestRuns.Load())
			}
			return
		}
		if runSelectorSubtestRuns.Add(1) == 1 {
			t.Fatal("first run-selector execution must fail to trigger process retry")
		}
		t.Fatalf("run-selector retry ran in the parent process with run count %d", runSelectorSubtestRuns.Load())
	})
	t.Run("SiblingSubtest", func(t *testing.T) {
		t.Fatal("sibling subtest ran despite parent -run tail selector")
	})
}

func TestProcessRetrySkipSelectorParent(t *testing.T) {
	if processRetryFixtureEnv(processRetrySelectorFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("selector fixture runs only from its controller subprocess")
	}
	t.Run("ExecutedSubtest", func(t *testing.T) {
		if processRetryFixtureChild() {
			if skipSelectorSubtestRuns.Load() != 0 {
				t.Fatalf("process retry child inherited parent skip selector count: %d", skipSelectorSubtestRuns.Load())
			}
			return
		}
		if skipSelectorSubtestRuns.Add(1) == 1 {
			t.Fatal("first skip-selector execution must fail to trigger process retry")
		}
		t.Fatalf("skip-selector retry ran in the parent process with run count %d", skipSelectorSubtestRuns.Load())
	})
	t.Run("SkippedSubtest", func(t *testing.T) {
		t.Fatal("subtest ran despite parent -skip selector")
	})
}

func TestProcessRetryProcessExitParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryProcessExitFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("process-exit fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if processExitRuns.Load() != 0 {
			t.Fatalf("process retry child inherited parent process-exit count: %d", processExitRuns.Load())
		}
		fmt.Println(processRetryProcessExitLogSentinel)
		return
	}
	if processExitRuns.Add(1) == 1 {
		t.Fatal("first process-exit execution must fail to trigger process retry")
	}
	t.Fatalf("process-exit retry ran in the parent process with run count %d", processExitRuns.Load())
}

func TestProcessRetryMalformedJSONParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryMalformedJSONFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("malformed-json fixture runs only from its controller subprocess")
	}
	if malformedJSONRuns.Add(1) == 1 {
		t.Fatal("first malformed-json execution must fail to trigger process retry")
	}
	t.Fatalf("malformed-json retry ran in the parent process with run count %d", malformedJSONRuns.Load())
}

func TestProcessRetryTimeoutParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryTimeoutFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("timeout fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if timeoutRuns.Load() != 0 {
			t.Fatalf("process retry child inherited parent timeout count: %d", timeoutRuns.Load())
		}
		fmt.Println(processRetryTimeoutLogSentinel)
		time.Sleep(5 * time.Second)
		return
	}
	if timeoutRuns.Add(1) == 1 {
		t.Fatal("first timeout execution must fail to trigger process retry")
	}
	t.Fatalf("timeout retry ran in the parent process with run count %d", timeoutRuns.Load())
}

func TestProcessRetryOutputTimeoutParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryOutputTimeoutFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("output-timeout fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if outputTimeoutRuns.Load() != 0 {
			t.Fatalf("process retry child inherited parent output-timeout count: %d", outputTimeoutRuns.Load())
		}
		for i := range 2048 {
			fmt.Fprintf(os.Stdout, "%s stdout %04d\n", processRetryOutputTimeoutLogSentinel, i)
			fmt.Fprintf(os.Stderr, "%s stderr %04d\n", processRetryOutputTimeoutLogSentinel, i)
		}
		time.Sleep(5 * time.Second)
		return
	}
	if outputTimeoutRuns.Add(1) == 1 {
		t.Fatal("first output-timeout execution must fail to trigger process retry")
	}
	t.Fatalf("output-timeout retry ran in the parent process with run count %d", outputTimeoutRuns.Load())
}

func TestProcessRetryDescendantCleanupParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryDescendantCleanupFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("descendant-cleanup fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if descendantCleanupRuns.Load() != 0 {
			t.Fatalf("process retry child inherited parent descendant-cleanup count: %d", descendantCleanupRuns.Load())
		}
		startDescendant := func(path string, inheritOutput bool) {
			cmd := exec.Command(os.Args[0], "-test.run=^$")
			cmd.Env = append(os.Environ(),
				processRetryDescendantHelperEnv+"=true",
				processRetryDescendantLivenessPathEnv+"="+path,
			)
			if inheritOutput {
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
			}
			if err := cmd.Start(); err != nil {
				t.Fatalf("start process retry descendant helper: %v", err)
			}
			address := processRetryDescendantAddress(t, path)
			conn, err := net.DialTimeout("tcp", address, 250*time.Millisecond)
			if err != nil {
				t.Fatalf("connect to process retry descendant helper: %v", err)
			}
			if err := conn.Close(); err != nil {
				t.Fatalf("close process retry descendant helper connection: %v", err)
			}
			if err := cmd.Process.Release(); err != nil {
				t.Fatalf("release process retry descendant helper handle: %v", err)
			}
		}
		startDescendant(processRetryFixtureEnv(processRetryDescendantLivenessPathEnv), true)
		startDescendant(processRetryFixtureEnv(processRetryDescendantIndependentPathEnv), false)
		fmt.Println(processRetryDescendantLogSentinel)
		return
	}
	if descendantCleanupRuns.Add(1) == 1 {
		t.Fatal("first descendant-cleanup execution must fail to trigger process retry")
	}
	t.Fatalf("descendant-cleanup retry ran in the parent process with run count %d", descendantCleanupRuns.Load())
}

func TestProcessRetryTransportIsolationParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryTransportIsolationEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("transport-isolation fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		for _, key := range []string{
			constants.CIVisibilityInternalRetryProcessChild,
			constants.CIVisibilityInternalRetryProcessResultPath,
			constants.CIVisibilityInternalRetryProcessTestName,
			constants.CIVisibilityInternalRetryProcessAttempt,
			constants.CIVisibilityInternalRetryProcessReason,
		} {
			if _, inherited := os.LookupEnv(key); inherited {
				t.Fatalf("process retry transport key remained inheritable: %s", key)
			}
		}

		cmd := exec.Command(os.Args[0], "-test.run=^$", "-test.v")
		cmd.Env = append(os.Environ(), processRetryTransportProbeEnv+"=true")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("process retry transport descendant failed: %v\n%s", err, output)
		}

		if err := os.Setenv(constants.CIVisibilityInternalRetryProcessChild, "false"); err != nil {
			t.Fatalf("mutate process retry child marker: %v", err)
		}
		t.Cleanup(func() { _ = os.Unsetenv(constants.CIVisibilityInternalRetryProcessChild) })
		session := integrations.CreateTestSession()
		if session.SessionID() != 0 {
			t.Fatal("process retry child mode changed after mutating the live environment")
		}
		return
	}
	if transportIsolationRuns.Add(1) == 1 {
		t.Fatal("first transport-isolation execution must fail to trigger process retry")
	}
	t.Fatalf("transport-isolation retry ran in the parent process with run count %d", transportIsolationRuns.Load())
}

func TestProcessRetryStartupRerunsController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "startup")
	path := filepathForStartupFixture(t, "startup-reruns")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryStartupRerunsParent$", "-test.v")
	cmd.Env = processRetryScenarioEnvironment(
		processRetryStartupFixtureEnv+"=true",
		processRetryStartupRerunFileEnv+"="+path,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("startup-rerun subprocess failed: %v\n%s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(data))
	if len(lines) != 2 || lines[0] != "init" || lines[1] != "init" {
		t.Fatalf("expected exactly one parent and one child package init event, got %q", lines)
	}
}

func TestProcessRetryStartupConflictController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "startup conflict")
	resourcePath := filepathForStartupFixture(t, "startup-conflict-resource")
	markerPath := filepathForStartupFixture(t, "startup-conflict-marker")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryStartupConflictParent$", "-test.v")
	cmd.Env = processRetryScenarioEnvironment(
		processRetryStartupFixtureEnv+"=true",
		processRetryStartupConflictFileEnv+"="+resourcePath,
		processRetryStartupConflictMarkerEnv+"="+markerPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("startup-conflict subprocess failed: %v\n%s", err, output)
	}
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(data))
	if len(lines) != 1 || lines[0] != "child_conflict" {
		t.Fatalf("expected exactly one child conflict and no parent conflicts, got %q", lines)
	}
}

func TestProcessRetryStartupRerunsParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryStartupFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("startup fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if startupRerunRuns.Load() != 0 {
			t.Fatalf("process retry child inherited parent startup count: %d", startupRerunRuns.Load())
		}
		return
	}
	if startupRerunRuns.Add(1) == 1 {
		t.Fatal("first startup-rerun execution must fail to trigger process retry")
	}
	t.Fatalf("startup-rerun retry ran in the parent process with run count %d", startupRerunRuns.Load())
}

func TestProcessRetryStartupConflictParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryStartupFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("startup fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if startupConflictRuns.Load() != 0 {
			t.Fatalf("process retry child inherited parent startup conflict count: %d", startupConflictRuns.Load())
		}
		return
	}
	if startupConflictRuns.Add(1) == 1 {
		t.Fatal("first startup-conflict execution must fail to trigger process retry")
	}
	t.Fatalf("startup-conflict retry ran in the parent process with run count %d", startupConflictRuns.Load())
}

func filepathForStartupFixture(t *testing.T, name string) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), name+"-*")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendStartupFixtureLine(path, line string) {
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.WriteString(line + "\n")
}

func processRetryFixtureChild() bool {
	return integrations.IsProcessRetryChild()
}

func processRetryFixtureEnv(name string) string {
	value, _ := env.Lookup(name)
	return value
}

func processRetryFixtureCommitSHA() string {
	if sha := env.Get("GITHUB_SHA"); sha != "" {
		return sha
	}
	return "local"
}
