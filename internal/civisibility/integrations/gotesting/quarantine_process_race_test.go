//go:build race

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

const (
	quarantinedRaceIsolationFixtureEnv = "DD_TEST_QUARANTINED_RACE_ISOLATION"
	quarantinedRaceIsolationPIDDirEnv  = "DD_TEST_QUARANTINED_RACE_ISOLATION_PID_DIR"
)

func quarantinedRaceIsolationFixtureSelected() bool {
	return os.Getenv(quarantinedRaceIsolationFixtureEnv) != ""
}

func runQuarantinedRaceIsolationFixture(m *testing.M) {
	// A process child must enter the existing no-op CI Visibility bootstrap
	// directly. It never creates a session, fetches settings, or starts retries.
	if isProcessRetryChild() {
		os.Exit(RunM(m))
	}
	requireEnv := func(key, value string) {
		if err := os.Setenv(key, value); err != nil {
			panic(err)
		}
	}
	scenario := os.Getenv(quarantinedRaceIsolationFixtureEnv)
	requireEnv(constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	attempts := "2"
	if scenario == "failfast" {
		attempts = "3"
	}
	requireEnv(constants.CIVisibilityTestManagementAttemptToFixRetriesEnvironmentVariable, attempts)
	if scenario == "feature-gate" {
		requireEnv(constants.CIVisibilitySubtestFeaturesEnabled, "false")
	}

	module := "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
	suite := "quarantine_process_race_test.go"
	testifySuite := suite + "/quarantinedRaceTestifySuite"
	properties := func(attemptToFix bool) net.TestManagementTestsResponseDataTestProperties {
		return net.TestManagementTestsResponseDataTestProperties{
			Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{
				Quarantined: true, AttemptToFix: attemptToFix,
			},
		}
	}
	server := setUpHTTPServer(false, false, false, nil, false, nil, true,
		&net.TestManagementTestsResponseDataModules{Modules: map[string]net.TestManagementTestsResponseDataSuites{
			module: {Suites: map[string]net.TestManagementTestsResponseDataTests{
				suite: {Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
					"TestQuarantinedRaceFixture":               properties(false),
					"TestQuarantinedPanicFixture":              properties(false),
					"TestQuarantinedRaceSecondFixture":         properties(false),
					"TestQuarantinedRaceAttemptToFixFixture":   properties(true),
					"TestQuarantinedRaceLeafFixture/card/visa": properties(false),
					"TestQuarantinedRaceNestedFixture/card":    properties(false),
					"TestQuarantinedRaceNestedFixture/card/disabled": {
						Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true},
					},
					"TestQuarantinedRaceNestedATFFixture/card":      properties(false),
					"TestQuarantinedRaceNestedATFFixture/card/visa": properties(true),
					"TestQuarantinedRaceSubtestFeatureGateFixture":  properties(false),
					"TestQuarantinedRaceSubtestFeatureGateFixture/disabled": {
						Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true},
					},
					"TestQuarantinedRaceCleanupPanicFixture":                 properties(false),
					"TestQuarantinedRaceCleanupFailNowFixture":               properties(false),
					"TestQuarantinedRaceCleanupGoexitFixture":                properties(false),
					"TestQuarantinedRaceFailfastRootFixture":                 properties(true),
					"TestQuarantinedRaceFailfastDescendantFixture":           properties(false),
					"TestQuarantinedRaceFailfastDescendantFixture/child":     properties(true),
					"TestQuarantinedRaceParallelFixture/isolated":            properties(false),
					"TestQuarantinedRaceParallelCoverageFixture/isolated":    properties(false),
					"TestQuarantinedRaceBeforeParallelFixture/isolated":      properties(false),
					"TestQuarantinedRaceAncestorPanicFixture/isolated":       properties(false),
					"TestQuarantinedRaceTerminalDescendantsFixture/parallel": properties(false),
					"TestQuarantinedRaceTerminalDescendantsFixture/panic":    properties(false),
				}},
				testifySuite: {Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
					"TestQuarantinedRaceTestifyFixture/TestSource": properties(false),
				}},
			}},
		}}, false, nil)
	defer server.Close()

	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()
	exitCode := RunM(m)
	if exitCode != 0 {
		panic(fmt.Sprintf("quarantined race isolation fixture exit code = %d", exitCode))
	}

	pidDir := os.Getenv(quarantinedRaceIsolationPIDDirEnv)
	parentPID := strconv.Itoa(os.Getpid())
	readPID := func(name string) string {
		payload, err := os.ReadFile(filepath.Join(pidDir, name))
		if err != nil {
			panic(err)
		}
		return strings.TrimSpace(string(payload))
	}
	spans := mTracer.FinishedSpans()
	switch scenario {
	case "parallel-admission":
		if readPID("parallel-child") == parentPID || readPID("parallel-sibling") != parentPID {
			panic("parallel quarantined root did not overlap its parent-process sibling")
		}
		parallelSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceParallelFixture/isolated", 1)
		checkSpansByTagValue(parallelSpans, constants.TestStatus, constants.TestStatusPass, 1)
		os.Exit(0)
	case "parallel-coverage":
		if readPID("parallel-coverage-child") == parentPID {
			panic("covered parallel descendant did not run in the isolated child")
		}
		if readPID("parallel-coverage-maximum") != "1" {
			panic("covered parallel descendants were not serialized")
		}
		parallelSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceParallelCoverageFixture/isolated", 1)
		checkSpansByTagValue(parallelSpans, constants.TestStatus, constants.TestStatusPass, 1)
		for _, name := range []string{"concurrent-a", "concurrent-b"} {
			childSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceParallelCoverageFixture/isolated/"+name, 1)
			checkSpansByTagValue(childSpans, constants.TestStatus, constants.TestStatusPass, 1)
		}
		os.Exit(0)
	case "terminal-descendants":
		parallelRoot := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceTerminalDescendantsFixture/parallel", 1)
		checkSpansByTagValue(parallelRoot, constants.TestStatus, constants.TestStatusFail, 1)
		parallelChild := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceTerminalDescendantsFixture/parallel/child", 1)
		checkSpansByTagValue(parallelChild, constants.TestStatus, constants.TestStatusFail, 1)
		if rootFinish, childFinish := parallelRoot[0].StartTime().Add(parallelRoot[0].Duration()), parallelChild[0].StartTime().Add(parallelChild[0].Duration()); rootFinish.Before(childFinish) {
			panic("selected root finished before its parallel descendant")
		}
		panicRoot := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceTerminalDescendantsFixture/panic", 1)
		checkSpansByTagValue(panicRoot, constants.TestStatus, constants.TestStatusFail, 1)
		panicChild := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceTerminalDescendantsFixture/panic/child", 1)
		checkSpansByTagValue(panicChild, constants.TestStatus, constants.TestStatusFail, 1)
		if message, _ := panicChild[0].Tag(ext.ErrorMsg).(string); !strings.Contains(message, "body panic sentinel") {
			panic(fmt.Sprintf("body panic was not reported on the isolated descendant: %q", message))
		}
		os.Exit(0)
	case "race-before-parallel":
		races := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceBeforeParallelFixture/isolated", 1)
		checkSpansByTagValue(races, constants.TestStatus, constants.TestStatusFail, 1)
		for _, entry := range logsEntries {
			if entry.TestName == "TestQuarantinedRaceBeforeParallelFixture/isolated" && strings.Contains(entry.Message, "WARNING: DATA RACE") {
				os.Exit(0)
			}
		}
		panic("race before t.Parallel was not preserved in the selected root output")
	case "ancestor-terminal":
		root := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceAncestorPanicFixture/isolated", 1)
		checkSpansByTagValue(root, constants.TestStatus, constants.TestStatusPass, 1)
		checkSpansByTagValue(root, ext.ErrorType, "test_panic", 0)
		os.Exit(0)
	case "testify-source":
		if readPID("testify-source") == parentPID {
			panic("Testify method did not run in the isolated child")
		}
		testifySpans := checkSpansByResourceName(spans, testifySuite+".TestQuarantinedRaceTestifyFixture/TestSource", 1)
		method := runtime.FuncForPC(reflect.ValueOf((*quarantinedRaceTestifySuite).TestSource).Pointer())
		_, sourceLine := method.FileLine(method.Entry())
		if got := fmt.Sprint(testifySpans[0].Tag(constants.TestSourceStartLine)); got != strconv.Itoa(sourceLine) {
			panic(fmt.Sprintf("Testify source line = %s, want method line %d", got, sourceLine))
		}
		os.Exit(0)
	case "feature-gate":
		if readPID("feature-gate-disabled") == parentPID {
			panic("subtest feature gate fixture did not run in the isolated child")
		}
		featureSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceSubtestFeatureGateFixture/disabled", 1)
		checkSpansByTagValue(featureSpans, constants.TestStatus, constants.TestStatusPass, 1)
		os.Exit(0)
	case "cleanup":
		panicSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceCleanupPanicFixture/child", 1)
		checkSpansByTagValue(panicSpans, constants.TestStatus, constants.TestStatusFail, 1)
		if message, _ := panicSpans[0].Tag(ext.ErrorMsg).(string); !strings.Contains(message, "cleanup panic sentinel") {
			panic(fmt.Sprintf("cleanup panic was not reported on the isolated test: %q", message))
		}
		goexitSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceCleanupGoexitFixture/child", 1)
		checkSpansByTagValue(goexitSpans, constants.TestStatus, constants.TestStatusPass, 1)
		failNowSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceCleanupFailNowFixture/child", 1)
		checkSpansByTagValue(failNowSpans, constants.TestStatus, constants.TestStatusFail, 1)
		os.Exit(0)
	case "failfast":
		for _, name := range []string{"failfast-root-3", "failfast-descendant-3"} {
			if _, err := os.Stat(filepath.Join(pidDir, name)); err == nil || !os.IsNotExist(err) {
				panic(name + " ran after a valid failure under -failfast")
			}
		}
		readPID("failfast-root-1")
		readPID("failfast-root-2")
		readPID("failfast-descendant-1")
		readPID("failfast-descendant-2")
		for _, name := range []string{"TestQuarantinedRaceFailfastRootFixture", "TestQuarantinedRaceFailfastDescendantFixture/child"} {
			failfastSpans := checkSpansByResourceName(spans, suite+"."+name, 2)
			checkSpansByTagValue(failfastSpans, constants.TestIsRetry, "true", 1)
			checkSpansByTagValue(failfastSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 1)
			checkSpansByTagValue(failfastSpans, constants.TestAttemptToFixPassed, "false", 1)
			checkSpansByTagValue(failfastSpans, constants.TestFinalStatus, constants.TestStatusSkip, 1)
		}
		os.Exit(0)
	}
	racePID := readPID("race")
	secondPID := readPID("second")
	panicPID := readPID("panic")
	if racePID == parentPID || secondPID == parentPID || panicPID == parentPID ||
		racePID == secondPID || racePID == panicPID || secondPID == panicPID {
		panic("quarantined roots did not use distinct child processes")
	}
	cardPID := readPID("card")
	if cardPID == parentPID || readPID("visa") != cardPID || readPID("mastercard") != cardPID || readPID("skipped") != cardPID {
		panic("quarantined subtree did not stay in one child process")
	}
	if readPID("paypal") != parentPID {
		panic("non-quarantined sibling did not stay in the parent process")
	}
	atf0, atf1 := readPID("atf-1"), readPID("atf-2")
	if atf0 == parentPID || atf1 == parentPID || atf0 == atf1 {
		panic("attempt-to-fix executions did not use fresh child processes")
	}
	nestedATF0, nestedATF1 := readPID("nested-atf-visa-1"), readPID("nested-atf-visa-2")
	if nestedATF0 == parentPID || nestedATF1 == parentPID || nestedATF0 == nestedATF1 {
		panic("descendant attempt-to-fix executions did not use fresh child processes")
	}
	leafPID := readPID("leaf-visa")
	if leafPID == parentPID || readPID("leaf-card-child") != leafPID {
		panic("exact quarantined leaf did not run through its ancestor in the child process")
	}
	if readPID("leaf-card-parent") != parentPID || readPID("leaf-mastercard-parent") != parentPID {
		panic("exact quarantined leaf moved its non-quarantined family out of the parent process")
	}
	if _, err := os.Stat(filepath.Join(pidDir, "leaf-mastercard-child")); err == nil || !os.IsNotExist(err) {
		panic("exact quarantined leaf also executed its sibling in the child process")
	}
	if _, err := os.Stat(filepath.Join(pidDir, "disabled-body")); err == nil || !os.IsNotExist(err) {
		panic("disabled subtree descendant executed its body")
	}

	spanTypes := map[string]int{}
	for _, span := range spans {
		if spanType, ok := span.Tag(ext.SpanType).(string); ok {
			spanTypes[spanType]++
		}
	}
	if spanTypes[constants.SpanTypeTestSession] != 1 || spanTypes[constants.SpanTypeTestModule] != 1 ||
		spanTypes[constants.SpanTypeTestSuite] != 1 || spanTypes[constants.SpanTypeTest] != 22 {
		panic(fmt.Sprintf("unexpected parent-owned CI Visibility span counts: %#v", spanTypes))
	}
	raceSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceFixture", 1)
	checkSpansByTagValue(raceSpans, constants.TestStatus, constants.TestStatusFail, 1)
	checkSpansByTagValue(raceSpans, constants.TestIsQuarantined, "true", 1)
	checkSpansByTagValue(raceSpans, constants.TestFinalStatus, constants.TestStatusSkip, 1)
	panicSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedPanicFixture", 1)
	checkSpansByTagValue(panicSpans, constants.TestStatus, constants.TestStatusFail, 1)
	checkSpansByTagValue(panicSpans, constants.TestIsQuarantined, "true", 1)
	checkSpansByTagValue(panicSpans, constants.TestFinalStatus, constants.TestStatusSkip, 1)
	atfSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceAttemptToFixFixture", 2)
	checkSpansByTagValue(atfSpans, constants.TestIsAttempToFix, "true", 2)
	checkSpansByTagValue(atfSpans, constants.TestIsRetry, "true", 1)
	checkSpansByTagValue(atfSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 1)
	checkSpansByTagValue(atfSpans, constants.TestAttemptToFixPassed, "true", 1)
	checkSpansByTagValue(atfSpans, constants.TestFinalStatus, constants.TestStatusSkip, 1)
	atfChildSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceAttemptToFixFixture/child", 2)
	checkSpansByTagValue(atfChildSpans, constants.TestIsAttempToFix, "true", 2)
	checkSpansByTagValue(atfChildSpans, constants.TestIsRetry, "true", 1)
	checkSpansByTagValue(atfChildSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 1)
	checkSpansByTagValue(atfChildSpans, constants.TestAttemptToFixPassed, "true", 1)
	checkSpansByTagValue(atfChildSpans, constants.TestFinalStatus, constants.TestStatusSkip, 1)
	nestedATFSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceNestedATFFixture/card/visa", 2)
	checkSpansByTagValue(nestedATFSpans, constants.TestIsAttempToFix, "true", 2)
	checkSpansByTagValue(nestedATFSpans, constants.TestIsRetry, "true", 1)
	checkSpansByTagValue(nestedATFSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 1)
	checkSpansByTagValue(nestedATFSpans, constants.TestAttemptToFixPassed, "true", 1)
	checkSpansByTagValue(nestedATFSpans, constants.TestFinalStatus, constants.TestStatusSkip, 1)
	checkSpansByResourceName(spans, suite+".TestQuarantinedRaceNestedATFFixture/card", 1)
	checkSpansByResourceName(spans, suite+".TestQuarantinedRaceLeafFixture", 1)
	checkSpansByResourceName(spans, suite+".TestQuarantinedRaceLeafFixture/card", 1)
	leafSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceLeafFixture/card/visa", 1)
	checkSpansByTagValue(leafSpans, constants.TestStatus, constants.TestStatusPass, 1)
	checkSpansByTagValue(leafSpans, constants.TestIsQuarantined, "true", 1)
	checkSpansByResourceName(spans, suite+".TestQuarantinedRaceLeafFixture/card/mastercard", 1)
	for _, name := range []string{
		"TestQuarantinedRaceNestedFixture/card",
		"TestQuarantinedRaceNestedFixture/card/visa",
		"TestQuarantinedRaceNestedFixture/card/mastercard",
		"TestQuarantinedRaceNestedFixture/card/skipped",
	} {
		childSpans := checkSpansByResourceName(spans, suite+"."+name, 1)
		checkSpansByTagValue(childSpans, constants.TestIsQuarantined, "true", 1)
	}
	skippedSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceNestedFixture/card/skipped", 1)
	checkSpansByTagValue(skippedSpans, constants.TestStatus, constants.TestStatusSkip, 1)
	checkSpansByTagValue(skippedSpans, constants.TestSkipReason, "fixture skip", 1)
	disabledSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceNestedFixture/card/disabled", 1)
	checkSpansByTagValue(disabledSpans, constants.TestStatus, constants.TestStatusSkip, 1)
	checkSpansByTagValue(disabledSpans, constants.TestIsDisabled, "true", 1)
	checkSpansByTagValue(disabledSpans, constants.TestFinalStatus, constants.TestStatusSkip, 1)
	visaSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceNestedFixture/card/visa", 1)
	checkSpansByTagValue(visaSpans, constants.TestStatus, constants.TestStatusPass, 1)
	checkSpansByTagName(visaSpans, constants.TestSourceFile, 1)
	checkSpansByTagName(visaSpans, constants.TestSourceStartLine, 1)
	checkSpansByTagName(visaSpans, constants.TestSourceEndLine, 1)
	checkSpansByTagName(visaSpans, constants.TestCodeOwners, 1)
	mastercardSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceNestedFixture/card/mastercard", 1)
	checkSpansByTagValue(mastercardSpans, constants.TestStatus, constants.TestStatusFail, 1)
	checkSpansByTagValue(mastercardSpans, ext.ErrorType, "test_race", 1)
	cardSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceNestedFixture/card", 1)
	checkSpansByTagValue(cardSpans, ext.ErrorType, "test_race", 0)
	rootRaceReport := false
	for _, entry := range logsEntries {
		if entry.TestName == "TestQuarantinedRaceNestedFixture/card" && strings.Contains(entry.Message, "WARNING: DATA RACE") {
			rootRaceReport = true
			break
		}
	}
	if !rootRaceReport {
		panic("selected quarantined root did not preserve the child race-detector report")
	}
	os.Exit(0)
}

func TestQuarantinedRaceProcessEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "true", "^TestQuarantined(Race(Fixture|SecondFixture|AttemptToFixFixture|LeafFixture|NestedFixture|NestedATFFixture)|PanicFixture)$")
	if coverProfile := flag.Lookup("test.coverprofile"); coverProfile != nil && coverProfile.Value.String() != "" {
		requireFunctionCovered(t, coverProfile.Value.String(), "bufferQuarantinedRaceChildOutput")
	}
}

func TestQuarantinedRaceSubtestFeatureGateEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "feature-gate", "^TestQuarantinedRaceSubtestFeatureGateFixture$")
}

func TestQuarantinedRaceCleanupEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "cleanup", "^TestQuarantinedRaceCleanup(Panic|FailNow|Goexit)Fixture$")
}

func TestQuarantinedRaceFailfastEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "failfast", "^TestQuarantinedRaceFailfast(Root|Descendant)Fixture$", "-test.failfast")
}

func TestQuarantinedRaceParallelAdmissionEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "parallel-admission", "^TestQuarantinedRaceParallelFixture$")
}

func TestQuarantinedRaceParallelCoverageEndToEnd(t *testing.T) {
	if testing.CoverMode() == "" {
		t.Skip("requires coverage instrumentation")
	}
	runQuarantinedRaceEndToEnd(t, "parallel-coverage", "^TestQuarantinedRaceParallelCoverageFixture$")
}

func TestQuarantinedRaceTerminalDescendantsEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "terminal-descendants", "^TestQuarantinedRaceTerminalDescendantsFixture$")
}

func TestQuarantinedRaceBeforeParallelEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "race-before-parallel", "^TestQuarantinedRaceBeforeParallelFixture$")
}

func TestQuarantinedRaceAncestorPanicEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "ancestor-terminal", "^TestQuarantinedRaceAncestorPanicFixture$")
}

func TestQuarantinedRaceTestifySourceEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "testify-source", "^TestQuarantinedRaceTestifyFixture$")
}

func runQuarantinedRaceEndToEnd(t *testing.T, scenario, pattern string, extraArgs ...string) {
	t.Helper()
	pidDir := t.TempDir()
	args := append(buildTestControllerSubprocessArgs(os.Args[1:], pattern), extraArgs...)
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(),
		quarantinedRaceIsolationFixtureEnv+"="+scenario,
		quarantinedRaceIsolationPIDDirEnv+"="+pidDir,
	)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("quarantined race isolation fixture failed: %v\n%s", err, output.String())
	}
}

func requireFunctionCovered(t *testing.T, profilePath, functionName string) {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "quarantine_process.go", nil, 0)
	require.NoError(t, err)
	start, finish := 0, 0
	for _, declaration := range parsed.Decls {
		if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Name.Name == functionName {
			start = fset.Position(fn.Pos()).Line
			finish = fset.Position(fn.End()).Line
			break
		}
	}
	require.NotZero(t, start)
	profile, err := os.ReadFile(profilePath)
	require.NoError(t, err)
	for _, line := range strings.Split(string(profile), "\n") {
		separator := strings.LastIndexByte(line, ':')
		if separator < 0 || !strings.HasSuffix(filepath.ToSlash(line[:separator]), "/gotesting/quarantine_process.go") {
			continue
		}
		var startLine, startColumn, endLine, endColumn, statements, count int
		if _, err := fmt.Sscanf(line[separator+1:], "%d.%d,%d.%d %d %d", &startLine, &startColumn, &endLine, &endColumn, &statements, &count); err == nil &&
			startLine <= finish && endLine >= start && count > 0 {
			return
		}
	}
	t.Fatalf("%s has no covered block in merged profile %s", functionName, profilePath)
}

func writeQuarantinedRaceIsolationPID(t *testing.T, name string) {
	t.Helper()
	path := filepath.Join(os.Getenv(quarantinedRaceIsolationPIDDirEnv), name)
	require.NoError(t, os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600))
}

func writeQuarantinedRaceIsolationProcessPID(t *testing.T, name string) {
	process := "parent"
	if isProcessRetryChild() {
		process = "child"
	}
	writeQuarantinedRaceIsolationPID(t, name+"-"+process)
}

func TestQuarantinedRaceFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	writeQuarantinedRaceIsolationPID(t, "race")
	triggerQuarantinedRaceFixture()
}

func triggerQuarantinedRaceFixture() {
	var value int
	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			for range 1000 {
				value++
			}
		}()
	}
	wg.Wait()
}

func TestQuarantinedRaceSecondFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	writeQuarantinedRaceIsolationPID(t, "second")
}

func TestQuarantinedPanicFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	writeQuarantinedRaceIsolationPID(t, "panic")
	panic("quarantined panic")
}

func TestQuarantinedRaceAttemptToFixFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	cfg, err := processRetryChildConfigFromEnv()
	require.NoError(t, err)
	writeQuarantinedRaceIsolationPID(t, "atf-"+strconv.Itoa(cfg.Attempt))
	t.Run("child", instrumentTestingTFunc(func(*testing.T) {}))
}

func TestQuarantinedRaceLeafFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("card", instrumentTestingTFunc(func(t *testing.T) {
		writeQuarantinedRaceIsolationProcessPID(t, "leaf-card")
		t.Run("visa", instrumentTestingTFunc(func(t *testing.T) {
			writeQuarantinedRaceIsolationPID(t, "leaf-visa")
		}))
		t.Run("mastercard", instrumentTestingTFunc(func(t *testing.T) {
			writeQuarantinedRaceIsolationProcessPID(t, "leaf-mastercard")
		}))
	}))
}

func TestQuarantinedRaceNestedFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("card", instrumentTestingTFunc(func(t *testing.T) {
		writeQuarantinedRaceIsolationPID(t, "card")
		t.Run("visa", instrumentTestingTFunc(func(t *testing.T) {
			(*T)(t).Parallel()
			writeQuarantinedRaceIsolationPID(t, "visa")
		}))
		t.Run("mastercard", instrumentTestingTFunc(func(t *testing.T) {
			writeQuarantinedRaceIsolationPID(t, "mastercard")
			triggerQuarantinedRaceFixture()
		}))
		t.Run("skipped", instrumentTestingTFunc(func(t *testing.T) {
			writeQuarantinedRaceIsolationPID(t, "skipped")
			instrumentCaptureFormattedSkip(t, "Skip", "fixture skip\n")
			t.Skip("fixture skip")
		}))
		t.Run("disabled", instrumentTestingTFunc(func(t *testing.T) {
			writeQuarantinedRaceIsolationPID(t, "disabled-body")
		}))
	}))
	t.Run("paypal", instrumentTestingTFunc(func(t *testing.T) {
		writeQuarantinedRaceIsolationPID(t, "paypal")
	}))
}

func TestQuarantinedRaceParallelFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("isolated", instrumentTestingTFunc(func(t *testing.T) {
		(*T)(t).Parallel()
		writeQuarantinedRaceIsolationPID(t, "parallel-child")
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(os.Getenv(quarantinedRaceIsolationPIDDirEnv), "parallel-sibling")); err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("parallel sibling was not admitted while the isolated body was running")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}))
	t.Run("sibling", instrumentTestingTFunc(func(t *testing.T) {
		(*T)(t).Parallel()
		writeQuarantinedRaceIsolationPID(t, "parallel-sibling")
	}))
}

func TestQuarantinedRaceParallelCoverageFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	released := make(chan struct{})
	t.Run("isolated", instrumentTestingTFunc(func(t *testing.T) {
		(*T)(t).Parallel()
		select {
		case <-released:
			writeQuarantinedRaceIsolationPID(t, "parallel-coverage-child")
		case <-time.After(2 * time.Second):
			t.Fatal("t.Parallel did not release t.Run back to its parent")
		}
		var active atomic.Int32
		var maximum atomic.Int32
		t.Cleanup(func() {
			path := filepath.Join(os.Getenv(quarantinedRaceIsolationPIDDirEnv), "parallel-coverage-maximum")
			require.NoError(t, os.WriteFile(path, []byte(strconv.Itoa(int(maximum.Load()))), 0o600))
		})
		for _, name := range []string{"concurrent-a", "concurrent-b"} {
			t.Run(name, instrumentTestingTFunc(func(t *testing.T) {
				(*T)(t).Parallel()
				current := active.Add(1)
				defer active.Add(-1)
				for observed := maximum.Load(); current > observed && !maximum.CompareAndSwap(observed, current); observed = maximum.Load() {
				}
				time.Sleep(20 * time.Millisecond)
			}))
		}
	}))
	close(released)
}

func TestQuarantinedRaceTerminalDescendantsFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("parallel", instrumentTestingTFunc(func(t *testing.T) {
		t.Run("child", instrumentTestingTFunc(func(t *testing.T) {
			(*T)(t).Parallel()
			time.Sleep(20 * time.Millisecond)
			t.Error("parallel descendant sentinel")
		}))
	}))
	t.Run("panic", instrumentTestingTFunc(func(t *testing.T) {
		t.Run("child", instrumentTestingTFunc(func(*testing.T) {
			panic("body panic sentinel")
		}))
	}))
}

func TestQuarantinedRaceBeforeParallelFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("isolated", instrumentTestingTFunc(func(t *testing.T) {
		triggerQuarantinedRaceFixture()
		(*T)(t).Parallel()
	}))
}

func TestQuarantinedRaceAncestorPanicFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("isolated", instrumentTestingTFunc(func(*testing.T) {}))
	if isProcessRetryChild() {
		panic("ancestor panic sentinel")
	}
}

type quarantinedRaceTestifySuite struct {
	t *testing.T
}

func (s *quarantinedRaceTestifySuite) TestSource() {
	writeQuarantinedRaceIsolationPID(s.t, "testify-source")
}

func TestQuarantinedRaceTestifyFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	suite := &quarantinedRaceTestifySuite{}
	instrumentTestifySuiteRun(t, suite)
	t.Run("TestSource", instrumentTestingTFunc(func(t *testing.T) {
		suite.t = t
		suite.TestSource()
	}))
}

func TestQuarantinedRaceNestedATFFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("card", instrumentTestingTFunc(func(t *testing.T) {
		t.Run("visa", instrumentTestingTFunc(func(t *testing.T) {
			cfg, err := processRetryChildConfigFromEnv()
			require.NoError(t, err)
			writeQuarantinedRaceIsolationPID(t, "nested-atf-visa-"+strconv.Itoa(cfg.Attempt))
		}))
	}))
}

func TestQuarantinedRaceSubtestFeatureGateFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("disabled", instrumentTestingTFunc(func(t *testing.T) {
		writeQuarantinedRaceIsolationPID(t, "feature-gate-disabled")
	}))
}

func TestQuarantinedRaceCleanupPanicFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("child", instrumentTestingTFunc(func(t *testing.T) {
		t.Cleanup(func() { panic("cleanup panic sentinel") })
	}))
}

func TestQuarantinedRaceCleanupGoexitFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("child", instrumentTestingTFunc(func(t *testing.T) {
		t.Cleanup(runtime.Goexit)
	}))
}

func TestQuarantinedRaceCleanupFailNowFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("child", instrumentTestingTFunc(func(t *testing.T) {
		t.Cleanup(t.FailNow)
	}))
}

func TestQuarantinedRaceFailfastRootFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	cfg, err := processRetryChildConfigFromEnv()
	require.NoError(t, err)
	writeQuarantinedRaceIsolationPID(t, "failfast-root-"+strconv.Itoa(cfg.Attempt))
	if cfg.Attempt > 1 {
		t.Fail()
	}
}

func TestQuarantinedRaceFailfastDescendantFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("child", instrumentTestingTFunc(func(t *testing.T) {
		cfg, err := processRetryChildConfigFromEnv()
		require.NoError(t, err)
		writeQuarantinedRaceIsolationPID(t, "failfast-descendant-"+strconv.Itoa(cfg.Attempt))
		if cfg.Attempt > 1 {
			t.Fail()
		}
	}))
}
