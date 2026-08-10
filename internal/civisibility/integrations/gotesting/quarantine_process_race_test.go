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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/stretchr/testify/require"
)

const (
	quarantinedRaceIsolationFixtureEnv = "DD_TEST_QUARANTINED_RACE_ISOLATION"
	quarantinedRaceIsolationPIDDirEnv  = "DD_TEST_QUARANTINED_RACE_ISOLATION_PID_DIR"
)

func quarantinedRaceIsolationFixtureSelected() bool {
	return os.Getenv(quarantinedRaceIsolationFixtureEnv) == "true"
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
	requireEnv(constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	requireEnv(constants.CIVisibilityTestManagementAttemptToFixRetriesEnvironmentVariable, "2")

	module := "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
	suite := "quarantine_process_race_test.go"
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
					"TestQuarantinedRaceFixture":                    properties(false),
					"TestQuarantinedPanicFixture":                   properties(false),
					"TestQuarantinedRaceSecondFixture":              properties(false),
					"TestQuarantinedRaceAttemptToFixFixture":        properties(true),
					"TestQuarantinedRaceNestedFixture/card":         properties(false),
					"TestQuarantinedRaceNestedATFFixture/card":      properties(false),
					"TestQuarantinedRaceNestedATFFixture/card/visa": properties(true),
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
	if testing.CoverMode() != "" && readPID("coverage-serialized") == parentPID {
		panic("covered concurrent subtests were not serialized in the child process")
	}
	atf0, atf1 := readPID("atf-1"), readPID("atf-2")
	if atf0 == parentPID || atf1 == parentPID || atf0 == atf1 {
		panic("attempt-to-fix executions did not use fresh child processes")
	}
	nestedATF0, nestedATF1 := readPID("nested-atf-visa-1"), readPID("nested-atf-visa-2")
	if nestedATF0 == parentPID || nestedATF1 == parentPID || nestedATF0 == nestedATF1 {
		panic("descendant attempt-to-fix executions did not use fresh child processes")
	}

	spans := mTracer.FinishedSpans()
	spanTypes := map[string]int{}
	for _, span := range spans {
		if spanType, ok := span.Tag(ext.SpanType).(string); ok {
			spanTypes[spanType]++
		}
	}
	if spanTypes[constants.SpanTypeTestSession] != 1 || spanTypes[constants.SpanTypeTestModule] != 1 ||
		spanTypes[constants.SpanTypeTestSuite] != 1 || spanTypes[constants.SpanTypeTest] != 19 {
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
	checkSpansByTagValue(atfChildSpans, constants.TestIsRetry, "true", 0)
	checkSpansByTagValue(atfChildSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 0)
	checkSpansByTagValue(atfChildSpans, constants.TestAttemptToFixPassed, "true", 0)
	nestedATFSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceNestedATFFixture/card/visa", 2)
	checkSpansByTagValue(nestedATFSpans, constants.TestIsAttempToFix, "true", 2)
	checkSpansByTagValue(nestedATFSpans, constants.TestIsRetry, "true", 1)
	checkSpansByTagValue(nestedATFSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 1)
	checkSpansByTagValue(nestedATFSpans, constants.TestAttemptToFixPassed, "true", 1)
	checkSpansByTagValue(nestedATFSpans, constants.TestFinalStatus, constants.TestStatusSkip, 1)
	checkSpansByResourceName(spans, suite+".TestQuarantinedRaceNestedATFFixture/card", 1)
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
	visaSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceNestedFixture/card/visa", 1)
	checkSpansByTagValue(visaSpans, constants.TestStatus, constants.TestStatusPass, 1)
	checkSpansByTagName(visaSpans, constants.TestSourceFile, 1)
	checkSpansByTagName(visaSpans, constants.TestSourceStartLine, 1)
	checkSpansByTagName(visaSpans, constants.TestSourceEndLine, 1)
	checkSpansByTagName(visaSpans, constants.TestCodeOwners, 1)
	mastercardSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceNestedFixture/card/mastercard", 1)
	checkSpansByTagValue(mastercardSpans, constants.TestStatus, constants.TestStatusFail, 1)
	for _, name := range []string{"concurrent-a", "concurrent-b"} {
		childSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceNestedFixture/card/"+name, 1)
		checkSpansByTagValue(childSpans, constants.TestStatus, constants.TestStatusPass, 1)
	}
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
	pidDir := t.TempDir()
	cmd := exec.Command(os.Args[0], buildTestControllerSubprocessArgs(os.Args[1:], "^TestQuarantined(Race(Fixture|SecondFixture|AttemptToFixFixture|NestedFixture|NestedATFFixture)|PanicFixture)$")...)
	cmd.Env = append(os.Environ(),
		quarantinedRaceIsolationFixtureEnv+"=true",
		quarantinedRaceIsolationPIDDirEnv+"="+pidDir,
	)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("quarantined race isolation fixture failed: %v\n%s", err, output.String())
	}
	if coverProfile := flag.Lookup("test.coverprofile"); coverProfile != nil && coverProfile.Value.String() != "" {
		requireFunctionCovered(t, coverProfile.Value.String(), "bufferQuarantinedRaceChildOutput")
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

func TestQuarantinedRaceNestedFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("card", instrumentTestingTFunc(func(t *testing.T) {
		writeQuarantinedRaceIsolationPID(t, "card")
		t.Run("visa", instrumentTestingTFunc(func(t *testing.T) {
			t.Parallel()
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
		var active atomic.Int32
		var maximum atomic.Int32
		var concurrent sync.WaitGroup
		for _, name := range []string{"concurrent-a", "concurrent-b"} {
			concurrent.Add(1)
			go func() {
				defer concurrent.Done()
				t.Run(name, instrumentTestingTFunc(func(t *testing.T) {
					if testing.CoverMode() != "" {
						t.Parallel()
					}
					current := active.Add(1)
					defer active.Add(-1)
					for observed := maximum.Load(); current > observed && !maximum.CompareAndSwap(observed, current); observed = maximum.Load() {
					}
					time.Sleep(20 * time.Millisecond)
				}))
			}()
		}
		concurrent.Wait()
		if testing.CoverMode() != "" && maximum.Load() == 1 {
			writeQuarantinedRaceIsolationPID(t, "coverage-serialized")
		}
	}))
	t.Run("paypal", instrumentTestingTFunc(func(t *testing.T) {
		writeQuarantinedRaceIsolationPID(t, "paypal")
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
