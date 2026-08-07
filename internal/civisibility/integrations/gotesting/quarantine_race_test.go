//go:build race

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"bytes"
	"fmt"
	stdnet "net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

func quarantinedRaceScenarioAvailable() bool { return true }

const (
	unquarantinedRaceFixtureEnv             = "DD_TEST_UNQUARANTINED_RACE_FIXTURE"
	unquarantinedRaceFailureSentinel        = "unquarantined race preserved test failure"
	quarantinedRaceInProcessFixtureEnv      = "DD_TEST_QUARANTINED_RACE_IN_PROCESS_FIXTURE"
	quarantinedRaceInProcessFailureSentinel = "quarantined race remained in the parent"
	quarantinedRacePIDDirEnv                = "DD_TEST_QUARANTINED_RACE_PID_DIR"
	quarantinedRaceCustomTestMainEnv        = "DD_TEST_QUARANTINED_RACE_CUSTOM_TESTMAIN"
	quarantinedRaceCustomTestMainPIDEnv     = "DD_TEST_QUARANTINED_RACE_CUSTOM_TESTMAIN_PID"
)

var quarantinedRaceSecondFinished = make(chan struct{}, 1)

func unquarantinedRaceFixtureSelected() bool {
	return os.Getenv(unquarantinedRaceFixtureEnv) == "true"
}

func quarantinedRaceInProcessFixtureSelected() bool {
	return os.Getenv(quarantinedRaceInProcessFixtureEnv) == "true"
}

func runQuarantinedRaceTests(m *testing.M, executionMode string) {
	if err := os.Setenv(constants.CIVisibilityRetryExecutionModeEnvironmentVariable, executionMode); err != nil {
		panic(err)
	}
	isolated := executionMode == "process"
	server := setUpHTTPServer(false, false, false, nil, true, []net.SkippableResponseDataAttributes{{
		Suite: "testing_test.go",
		Name:  "Test_Foo",
	}}, true,
		&net.TestManagementTestsResponseDataModules{
			Modules: map[string]net.TestManagementTestsResponseDataSuites{
				"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting": {
					Suites: map[string]net.TestManagementTestsResponseDataTests{
						"testing_test.go": {
							Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
								"Test_Foo": {
									Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Quarantined: true},
								},
							},
						},
						"quarantine_race_test.go": {
							Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
								"TestQuarantinedRace": {
									Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{
										Quarantined: true,
									},
								},
								"TestQuarantinedRaceSecond": {
									Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{
										Quarantined: true,
									},
								},
							},
						},
					},
				},
			},
		},
		true, nil)
	defer server.Close()
	configureImpactedTestsGitDiff()

	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()
	pidDir := os.Getenv(quarantinedRacePIDDirEnv)
	if pidDir == "" {
		panic("missing quarantined race PID directory")
	}

	var exitCode int
	if isolated {
		// A generated test main has no user TestMain frame. RunM in a fresh
		// goroutine so this fixture exercises that production call stack.
		done := make(chan int, 1)
		go func() { done <- RunM(m) }()
		exitCode = <-done
	} else {
		exitCode = RunM(m)
	}
	if os.Getenv(quarantinedRaceCoverageEnabledEnv) == "true" && mockCoverageRequests.Load() == 0 {
		panic("expected quarantined process retries to upload test coverage")
	}
	if isolated && exitCode != 0 {
		panic("expected a quarantined race not to affect the test run exit code; got " + strconv.Itoa(exitCode))
	}
	if !isolated && exitCode == 0 {
		panic("expected in-process race accounting to preserve the native failing exit code")
	}

	var childPID string
	failedSpans := 0
	finishedSpans := mTracer.FinishedSpans()
	spanTypeCounts := map[string]int{}
	for _, span := range finishedSpans {
		if spanType, ok := span.Tag(ext.SpanType).(string); ok {
			spanTypeCounts[spanType]++
		}
	}
	if spanTypeCounts[constants.SpanTypeTestSession] != 1 {
		panic(fmt.Sprintf("expected one parent-owned test session, got %d", spanTypeCounts[constants.SpanTypeTestSession]))
	}
	if spanTypeCounts[constants.SpanTypeTestModule] != 1 {
		panic(fmt.Sprintf("expected one parent-owned test module, got %d", spanTypeCounts[constants.SpanTypeTestModule]))
	}
	resources := map[string]string{
		"TestQuarantinedRace":       "quarantine_race_test.go.TestQuarantinedRace",
		"TestQuarantinedRaceSecond": "quarantine_race_test.go.TestQuarantinedRaceSecond",
		"Test_Foo":                  "testing_test.go.Test_Foo",
	}
	for testName, resource := range resources {
		spans := checkSpansByResourceName(finishedSpans, resource, 1)
		checkSpansByTagValue(spans, constants.TestIsQuarantined, "true", 1)
		checkSpansByTagValue(spans, constants.TestFinalStatus, constants.TestStatusSkip, 1)
		if testName == "Test_Foo" {
			checkSpansByTagValue(spans, constants.TestIsModified, "true", 1)
		}
		status := spans[0].Tag(constants.TestStatus)
		if status == constants.TestStatusFail {
			failedSpans++
		}
		wantStatus := any(constants.TestStatusPass)
		if testName == "TestQuarantinedRace" {
			wantStatus = constants.TestStatusFail
		}
		if status != wantStatus {
			panic(fmt.Sprintf("%s status = %v, want %v", testName, status, wantStatus))
		}
		payload, readErr := os.ReadFile(filepath.Join(pidDir, testName))
		if readErr != nil {
			panic(readErr)
		}
		pid := strings.TrimSpace(string(payload))
		if pid == "" {
			panic("missing quarantined race child PID")
		}
		if childPID == "" {
			childPID = pid
		} else if childPID != pid {
			panic("quarantined race tests did not share one batch child")
		}
	}
	if failedSpans != 1 {
		panic(fmt.Sprintf("expected exactly one quarantined batch race failure, got %d", failedSpans))
	}
	parentPID := strconv.Itoa(os.Getpid())
	if isolated && childPID == parentPID {
		panic("process mode executed quarantined race bodies in the parent")
	}
	if !isolated && childPID != parentPID {
		panic("in-process mode unexpectedly executed quarantined race bodies in a child")
	}
	if !isolated {
		fmt.Fprint(os.Stdout, quarantinedRaceInProcessFailureSentinel)
	}
	os.Exit(exitCode)
}

func acquireQuarantinedRaceCustomTestMainResource() func() {
	address := os.Getenv(quarantinedRaceCustomTestMainEnv)
	if address == "" {
		return func() {}
	}
	listener, err := stdnet.Listen("tcp", address)
	if err != nil {
		panic("quarantined race child re-entered active TestMain setup: " + err.Error())
	}
	if err := os.Setenv(quarantinedRaceCustomTestMainEnv, listener.Addr().String()); err != nil {
		_ = listener.Close()
		panic(err)
	}
	return func() { _ = listener.Close() }
}

func runQuarantinedRaceCustomTestMainTests(m *testing.M) {
	if err := os.Setenv(constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process"); err != nil {
		panic(err)
	}
	server := setUpHTTPServer(false, false, false, nil, true, nil, true,
		&net.TestManagementTestsResponseDataModules{
			Modules: map[string]net.TestManagementTestsResponseDataSuites{
				"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting": {
					Suites: map[string]net.TestManagementTestsResponseDataTests{
						"quarantine_race_test.go": {
							Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
								"TestQuarantinedRaceCustomTestMain": {
									Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Quarantined: true},
								},
							},
						},
					},
				},
			},
		}, true, nil)
	defer server.Close()
	configureImpactedTestsGitDiff()

	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()
	if exitCode := RunM(m); exitCode != 0 {
		panic("custom TestMain quarantined fixture failed with exit code " + strconv.Itoa(exitCode))
	}
	payload, err := os.ReadFile(os.Getenv(quarantinedRaceCustomTestMainPIDEnv))
	if err != nil {
		panic(err)
	}
	if strings.TrimSpace(string(payload)) != strconv.Itoa(os.Getpid()) {
		panic("custom TestMain quarantined fixture did not run in the parent")
	}
	os.Exit(0)
}

func runUnquarantinedRaceFixture(m *testing.M) {
	server := setUpHTTPServer(true, false, false, nil, false, nil, true,
		&net.TestManagementTestsResponseDataModules{}, false, nil)
	defer server.Close()

	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()
	exitCode := RunM(m)
	if exitCode == 0 {
		panic("expected an unquarantined race to fail the test run")
	}
	fmt.Fprint(os.Stdout, unquarantinedRaceFailureSentinel)
	os.Exit(exitCode)
}

func TestUnquarantinedRaceRemainsFailure(t *testing.T) {
	runFailingRaceFixture(t, "^TestUnquarantinedRace$", unquarantinedRaceFailureSentinel,
		unquarantinedRaceFixtureEnv+"=true")
}

func TestProcessModeOptInLeavesQuarantinedRaceInProcess(t *testing.T) {
	pidDir := t.TempDir()
	runFailingRaceFixture(t, "^TestQuarantinedRace", quarantinedRaceInProcessFailureSentinel,
		quarantinedRaceInProcessFixtureEnv+"=true",
		quarantinedRacePIDDirEnv+"="+pidDir)
}

func runFailingRaceFixture(t *testing.T, runFilter, sentinel string, environment ...string) {
	t.Helper()
	overrides := make(map[string]struct{}, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		overrides[key] = struct{}{}
	}
	cmd := exec.Command(os.Args[0], buildTestControllerSubprocessArgs(os.Args[1:], runFilter)...)
	cmd.Env = make([]string, 0, len(os.Environ())+len(environment))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, overridden := overrides[key]; !overridden {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, environment...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() == 0 {
		t.Fatalf("expected race subprocess to fail; err=%v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), sentinel) {
		t.Fatalf("race subprocess did not emit %q\n%s", sentinel, output.String())
	}
}

func TestQuarantinedRace(t *testing.T) {
	if execMeta := getTestMetadata(t); !isProcessRetryChild() && (execMeta == nil || !execMeta.hasAdditionalFeatureWrapper) {
		t.Skip("no CI Visibility quarantine wrapper active; skipping race injection")
	}
	t.Parallel()

	runRaceFixture()
	select {
	case <-quarantinedRaceSecondFinished:
	case <-time.After(250 * time.Millisecond):
	}
	writeQuarantinedRacePID(t)
	fmt.Fprint(os.Stdout, "quarantined race fixture completed")
}

func TestQuarantinedRaceSecond(t *testing.T) {
	if execMeta := getTestMetadata(t); !isProcessRetryChild() && (execMeta == nil || !execMeta.hasAdditionalFeatureWrapper) {
		t.Skip("no CI Visibility quarantine wrapper active; skipping race injection")
	}
	t.Parallel()
	writeQuarantinedRacePID(t)
	select {
	case quarantinedRaceSecondFinished <- struct{}{}:
	default:
	}
}

func TestQuarantinedRaceCustomTestMain(t *testing.T) {
	if execMeta := getTestMetadata(t); !isProcessRetryChild() && (execMeta == nil || !execMeta.hasAdditionalFeatureWrapper) {
		t.Skip("no CI Visibility quarantine wrapper active")
	}
	if err := os.WriteFile(os.Getenv(quarantinedRaceCustomTestMainPIDEnv), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUnquarantinedRace(t *testing.T) {
	if execMeta := getTestMetadata(t); execMeta == nil || !execMeta.hasAdditionalFeatureWrapper {
		t.Skip("no CI Visibility wrapper active; skipping race injection")
	}
	runRaceFixture()
}

func runRaceFixture() {
	var value int
	start := make(chan struct{})
	done := make(chan struct{}, 2)
	write := func(next int) {
		<-start
		value = next
		done <- struct{}{}
	}
	go write(1)
	go write(2)
	close(start)
	<-done
	<-done
	runtime.KeepAlive(value)
}
