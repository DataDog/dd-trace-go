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
	if scenario == "failfast" || scenario == "deferred-descendant-atf-failfast" || scenario == "deferred-dynamic-descendant-finality" {
		attempts = "3"
	}
	requireEnv(constants.CIVisibilityTestManagementAttemptToFixRetriesEnvironmentVariable, attempts)
	if scenario == "feature-gate" {
		requireEnv(constants.CIVisibilitySubtestFeaturesEnabled, "false")
	}
	if scenario == "parallel-root-slots" || scenario == "parallel-root-coordination" {
		requireEnv(constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable, "1")
	}
	// These fixture limits detect protocol deadlocks, not instrumentation speed.
	// Each scenario re-execs the test binary twice (fixture child, then the
	// quarantine machinery's own grandchild), and that grandchild is race- and
	// coverage-instrumented, so process startup alone can take several seconds
	// under load or inside a sandbox (e.g. the gotip gVisor job).
	if scenario == "parallel-atf-sibling" {
		requireEnv(constants.CIVisibilityRetryProcessTimeoutEnvironmentVariable, "30s")
	}
	if scenario == "parallel-denied" {
		requireEnv(constants.CIVisibilityRetryProcessTimeoutEnvironmentVariable, "30s")
	}

	module := "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
	suite := "quarantine_process_race_test.go"
	testifySuite := suite + "/quarantinedRaceTestifySuite"
	itrEnabled := scenario == "foreign-suite"
	var itrData []net.SkippableResponseDataAttributes
	if itrEnabled {
		itrData = []net.SkippableResponseDataAttributes{{
			Suite: "quarantine_process_test.go", Name: "TestQuarantinedRaceForeignSuiteFixture/root/itr",
		}}
	}
	properties := func(attemptToFix bool) net.TestManagementTestsResponseDataTestProperties {
		return net.TestManagementTestsResponseDataTestProperties{
			Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{
				Quarantined: true, AttemptToFix: attemptToFix,
			},
		}
	}
	server := setUpHTTPServer(false, false, false, nil, itrEnabled, itrData, true,
		&net.TestManagementTestsResponseDataModules{Modules: map[string]net.TestManagementTestsResponseDataSuites{
			module: {Suites: map[string]net.TestManagementTestsResponseDataTests{
				suite: {Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
					"TestQuarantinedRaceFixture":                           properties(false),
					"TestQuarantinedPanicFixture":                          properties(false),
					"TestQuarantinedRaceSecondFixture":                     properties(false),
					"TestQuarantinedRaceAttemptToFixFixture":               properties(true),
					"TestQuarantinedRaceIndependentATFFixture":             properties(true),
					"TestQuarantinedRaceIndependentATFFixture/clear":       properties(false),
					"TestQuarantinedRaceIndependentATFFixture/clear/owner": properties(true),
					"TestQuarantinedRaceDeepFixture":                       properties(true),
					"TestQuarantinedRaceDeepFixture/clear":                 properties(false),
					"TestQuarantinedRaceDeepFixture/clear/owner":           properties(true),
					"TestQuarantinedRaceDeepFixture/clear/owner/none":      properties(false),
					"TestQuarantinedRaceDeepFixture/clear/owner/none/deep": properties(true),
					"TestQuarantinedRaceLeafFixture/card/visa":             properties(false),
					"TestQuarantinedRaceNestedFixture/card":                properties(false),
					"TestQuarantinedRaceNestedFixture/card/disabled": {
						Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true},
					},
					"TestQuarantinedRaceNestedATFFixture/card":      properties(false),
					"TestQuarantinedRaceNestedATFFixture/card/visa": properties(true),
					"TestQuarantinedRaceSubtestFeatureGateFixture":  properties(false),
					"TestQuarantinedRaceSubtestFeatureGateFixture/disabled": {
						Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true},
					},
					"TestQuarantinedRaceCleanupPanicFixture":              properties(false),
					"TestQuarantinedRaceCleanupFailNowFixture":            properties(false),
					"TestQuarantinedRaceCleanupGoexitFixture":             properties(false),
					"TestQuarantinedRaceFailfastRootFixture":              properties(true),
					"TestQuarantinedRaceFailfastDescendantFixture":        properties(false),
					"TestQuarantinedRaceFailfastDescendantFixture/child":  properties(true),
					"TestQuarantinedRaceFailfastMaskedChildFixture":       properties(true),
					"TestQuarantinedRaceFailfastMaskedChildFixture/child": properties(false),
					"TestQuarantinedRaceParallelFixture/isolated":         properties(false),
					"TestQuarantinedRaceParallelRootsFixture/one":         properties(false),
					"TestQuarantinedRaceParallelRootsFixture/two":         properties(false),
					"TestQuarantinedRaceParallelCoverageFixture/isolated": properties(false),
					"TestQuarantinedRaceParallelDurationFixture/isolated": properties(false),
					"TestQuarantinedRaceParallelWaitDurationFixture/root": properties(false),
					"TestQuarantinedRaceDynamicDescendantFixture/root":    properties(true),

					"TestQuarantinedRaceManagedDescendantFixture/root":           properties(false),
					"TestQuarantinedRaceManagedDescendantFixture/root/child":     properties(false),
					"TestQuarantinedRaceManagedDescendantFixture/root/first":     properties(false),
					"TestQuarantinedRaceManagedDescendantFixture/root/second":    properties(false),
					"TestQuarantinedRaceNestedRootTerminalFixture/panic":         properties(false),
					"TestQuarantinedRaceNestedRootTerminalFixture/goexit":        properties(false),
					"TestQuarantinedRaceNestedRootTerminalFixture/cleanup-panic": properties(false),
					"TestQuarantinedRaceOwnerGenerationFixture/root":             properties(true),
					"TestQuarantinedRaceOwnerGenerationFixture/root/clear":       properties(false),
					"TestQuarantinedRaceOwnerGenerationFixture/root/clear/owner": properties(true),
					"TestQuarantinedRaceParallelATFFixture/root":                 properties(false),
					"TestQuarantinedRaceParallelATFFixture/root/owner":           properties(true),
					"TestQuarantinedRaceSerialATFFixture/root":                   properties(false),
					"TestQuarantinedRaceSerialATFFixture/root/owner":             properties(true),
					"TestQuarantinedRaceForeignSuiteFixture/root":                properties(false),
					"TestQuarantinedRaceForeignSuiteFixture/root/parent":         properties(true),
					"TestQuarantinedRaceParallelDeniedFixture/isolated":          properties(false),
					"TestQuarantinedRaceAncestorATFFixture": {
						Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{AttemptToFix: true},
					},
					"TestQuarantinedRaceAncestorATFFixture/child": properties(true),
					"TestQuarantinedRaceDeferredDescendantATFFixture": {
						Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{AttemptToFix: true},
					},
					"TestQuarantinedRaceDeferredDescendantATFFixture/quarantined": properties(false),
					"TestQuarantinedRaceDeferredDescendantATFFixture/clear":       {},
					"TestQuarantinedRaceDeferredDescendantATFFixture/clear/owner": {
						Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{AttemptToFix: true},
					},
					"TestQuarantinedRaceDeferredDescendantATFFixture/clear/owner/inherited": {
						Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{AttemptToFix: true},
					},
					"TestQuarantinedRaceDeferredDynamicDescendantFixture": {
						Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{AttemptToFix: true},
					},
					"TestQuarantinedRaceDeferredDynamicDescendantFixture/quarantined": properties(false),
					"TestQuarantinedRaceDeferredDynamicDescendantFixture/dynamic":     properties(true),
					"TestQuarantinedRaceInitialInheritedFixture": {
						Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{AttemptToFix: true},
					},
					"TestQuarantinedRaceInitialInheritedFixture/initial": properties(false),
					"TestQuarantinedRaceDeferredAggregateCoverageFixture": {
						Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{AttemptToFix: true},
					},
					"TestQuarantinedRaceDeferredAggregateCoverageFixture/quarantined": properties(false),

					"TestQuarantinedRaceAggregateCoverageFixture/isolated":   properties(false),
					"TestQuarantinedRaceBeforeParallelFixture/isolated":      properties(false),
					"TestQuarantinedRaceAncestorPanicFixture/isolated":       properties(false),
					"TestQuarantinedRaceAncestorFailureFixture/isolated":     properties(false),
					"TestQuarantinedRaceTerminalDescendantsFixture/parallel": properties(false),
					"TestQuarantinedRaceTerminalDescendantsFixture/panic":    properties(false),
					"TestQuarantinedRaceTerminalDescendantsFixture/goexit":   properties(false),
					"TestQuarantinedRaceTerminalDescendantsFixture/output":   properties(false),
				}},
				"quarantine_process_test.go": {Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
					"TestQuarantinedRaceForeignSuiteFixture/root/foreign": {
						Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true},
					},
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
	expectFailure := scenario == "deferred-descendant-atf-failure" || scenario == "deferred-descendant-inherited-failure" || scenario == "deferred-descendant-atf-failfast"
	if (exitCode != 0) != expectFailure {
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
	case "nested-attempt-family":
		pids := map[string]struct{}{}
		entries, err := os.ReadDir(pidDir)
		if err != nil {
			panic(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "nested-attempt-owner-") {
				pids[readPID(entry.Name())] = struct{}{}
			}
		}
		if len(pids) != 4 {
			panic(fmt.Sprintf("independent nested attempt family used %d child processes, want 4", len(pids)))
		}
		delete(pids, parentPID)
		if len(pids) != 4 {
			panic("independent nested attempt family ran in the parent process")
		}
		ownerSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceIndependentATFFixture/clear/owner", 4)
		checkSpansByTagValue(ownerSpans, constants.TestIsRetry, "true", 2)
		checkSpansByTagValue(ownerSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 2)
		checkSpansByTagValue(ownerSpans, constants.TestAttemptToFixPassed, "true", 2)
		checkSpansByTagValue(ownerSpans, constants.TestFinalStatus, constants.TestStatusSkip, 2)
		os.Exit(0)
	case "deep-nested-attempt-family":
		pids := map[string]struct{}{}
		entries, err := os.ReadDir(pidDir)
		if err != nil {
			panic(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "deep-nested-attempt-leaf-") {
				pids[readPID(entry.Name())] = struct{}{}
			}
		}
		delete(pids, parentPID)
		if len(pids) != 8 {
			panic(fmt.Sprintf("deep nested attempt family used %d fresh child processes, want 8", len(pids)))
		}
		ownerSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceDeepFixture/clear/owner", 4)
		checkSpansByTagValue(ownerSpans, constants.TestIsRetry, "true", 2)
		checkSpansByTagValue(ownerSpans, constants.TestAttemptToFixPassed, "true", 2)
		leafSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceDeepFixture/clear/owner/none/deep", 8)
		checkSpansByTagValue(leafSpans, constants.TestIsRetry, "true", 4)
		checkSpansByTagValue(leafSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 4)
		checkSpansByTagValue(leafSpans, constants.TestAttemptToFixPassed, "true", 4)
		checkSpansByTagValue(leafSpans, constants.TestFinalStatus, constants.TestStatusSkip, 4)
		os.Exit(0)
	case "nested-aggregate-coverage", "nested-aggregate-parallel-coverage", "nested-aggregate-coverage-control":
		os.Exit(0)
	case "parallel-admission":
		if readPID("parallel-child") == parentPID || readPID("parallel-sibling") != parentPID {
			panic("parallel quarantined root did not overlap its parent-process sibling")
		}
		parallelSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceParallelFixture/isolated", 1)
		checkSpansByTagValue(parallelSpans, constants.TestStatus, constants.TestStatusPass, 1)
		os.Exit(0)
	case "parallel-denied":
		root := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceParallelDeniedFixture/isolated", 1)
		checkSpansByTagValue(root, constants.TestStatus, constants.TestStatusFail, 1)
		checkSpansByTagValue(root, ext.ErrorType, "panic", 1)
		checkSpansByTagValue(root, constants.TestFinalStatus, constants.TestStatusSkip, 1)
		os.Exit(0)
	case "parallel-root-slots":
		one, two := readPID("parallel-root-one-child"), readPID("parallel-root-two-child")
		if one == parentPID || two == parentPID || one == two {
			panic("parallel quarantined roots did not use distinct child processes")
		}
		for _, name := range []string{"one", "two"} {
			root := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceParallelRootsFixture/"+name, 1)
			checkSpansByTagValue(root, constants.TestStatus, constants.TestStatusPass, 1)
		}
		os.Exit(0)
	case "parallel-root-coordination":
		for _, name := range []string{"one", "two"} {
			root := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceParallelRootsFixture/"+name, 1)
			checkSpansByTagValue(root, constants.TestStatus, constants.TestStatusPass, 1)
		}
		os.Exit(0)
	case "parallel-coverage":
		if readPID("parallel-coverage-child") == parentPID {
			panic("covered parallel descendant did not run in the isolated child")
		}
		if readPID("parallel-coverage-maximum") != "2" {
			panic("covered parallel descendants did not run concurrently")
		}
		parallelSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceParallelCoverageFixture/isolated", 1)
		checkSpansByTagValue(parallelSpans, constants.TestStatus, constants.TestStatusPass, 1)
		for _, name := range []string{"concurrent-a", "concurrent-b"} {
			childSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceParallelCoverageFixture/isolated/"+name, 1)
			checkSpansByTagValue(childSpans, constants.TestStatus, constants.TestStatusPass, 1)
		}
		os.Exit(0)
	case "parallel-duration":
		root := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceParallelDurationFixture/isolated", 1)
		childSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceParallelDurationFixture/isolated/suspended", 1)
		if childSpans[0].Duration() >= root[0].Duration() {
			panic(fmt.Sprintf("parallel suspension leaked into replayed child duration: child=%s root=%s", childSpans[0].Duration(), root[0].Duration()))
		}
		os.Exit(0)
	case "parallel-wait-duration":
		root := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceParallelWaitDurationFixture/root", 1)
		child := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceParallelWaitDurationFixture/root/slow", 1)
		if root[0].Duration() >= child[0].Duration() {
			panic(fmt.Sprintf("parallel descendant wait leaked into root duration: root=%s child=%s", root[0].Duration(), child[0].Duration()))
		}
		os.Exit(0)
	case "dynamic-descendant-finality":
		checkSpansByResourceName(spans, suite+".TestQuarantinedRaceDynamicDescendantFixture/root", 2)
		dynamic := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceDynamicDescendantFixture/root/dynamic", 1)
		checkSpansByTagValue(dynamic, constants.TestIsAttempToFix, "true", 1)
		checkSpansByTagValue(dynamic, constants.TestIsRetry, "true", 0)
		checkSpansByTagValue(dynamic, constants.TestFinalStatus, constants.TestStatusSkip, 1)
		os.Exit(0)
	case "managed-descendant-masking":
		if readPID("managed-descendant-run-result") != "true" {
			panic("managed descendant failure changed the enclosing t.Run result")
		}
		root := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceManagedDescendantFixture/root", 1)
		checkSpansByTagValue(root, constants.TestStatus, constants.TestStatusPass, 1)
		child := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceManagedDescendantFixture/root/child", 1)
		checkSpansByTagValue(child, constants.TestStatus, constants.TestStatusFail, 1)
		os.Exit(0)
	case "managed-descendant-concurrent-failure":
		root := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceManagedDescendantFixture/root", 1)
		checkSpansByTagValue(root, constants.TestStatus, constants.TestStatusFail, 1)
		for _, name := range []string{"child", "sibling"} {
			span := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceManagedDescendantFixture/root/"+name, 1)
			checkSpansByTagValue(span, constants.TestStatus, constants.TestStatusFail, 1)
		}
		os.Exit(0)
	case "managed-descendant-concurrent-managed":
		root := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceManagedDescendantFixture/root", 1)
		checkSpansByTagValue(root, constants.TestStatus, constants.TestStatusPass, 1)
		first := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceManagedDescendantFixture/root/first", 1)
		checkSpansByTagValue(first, constants.TestStatus, constants.TestStatusFail, 1)
		second := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceManagedDescendantFixture/root/second", 1)
		checkSpansByTagValue(second, constants.TestStatus, constants.TestStatusPass, 1)
		os.Exit(0)
	case "managed-descendant-root-failure":
		root := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceManagedDescendantFixture/root", 1)
		checkSpansByTagValue(root, constants.TestStatus, constants.TestStatusFail, 1)
		child := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceManagedDescendantFixture/root/child", 1)
		checkSpansByTagValue(child, constants.TestStatus, constants.TestStatusFail, 1)
		os.Exit(0)
	case "managed-descendant-output":
		root := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceManagedDescendantFixture/root", 1)
		checkSpansByTagValue(root, constants.TestStatus, constants.TestStatusPass, 1)
		child := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceManagedDescendantFixture/root/child", 1)
		checkSpansByTagValue(child, constants.TestStatus, constants.TestStatusPass, 1)
		grandchildName := "TestQuarantinedRaceManagedDescendantFixture/root/child/grandchild"
		grandchild := checkSpansByResourceName(spans, suite+"."+grandchildName, 1)
		checkSpansByTagValue(grandchild, constants.TestStatus, constants.TestStatusFail, 1)
		stdout, stderr := false, false
		for _, entry := range logsEntries {
			if entry.TestName == grandchildName {
				stdout = stdout || strings.Contains(entry.Message, "managed descendant stdout sentinel")
				stderr = stderr || strings.Contains(entry.Message, "managed descendant stderr sentinel")
			}
		}
		if stdout && stderr {
			os.Exit(0)
		}
		panic("managed descendant process output was not preserved")
	case "nested-root-terminal":
		for terminal, sentinel := range map[string]string{
			"panic":         "nested root panic sentinel",
			"goexit":        "runtime.Goexit",
			"cleanup-panic": "nested root cleanup panic sentinel",
		} {
			span := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceNestedRootTerminalFixture/"+terminal, 1)
			checkSpansByTagValue(span, constants.TestStatus, constants.TestStatusFail, 1)
			if message, _ := span[0].Tag(ext.ErrorMsg).(string); !strings.Contains(message, sentinel) {
				panic(fmt.Sprintf("nested %s terminal was not reported: %q", terminal, message))
			}
		}
		os.Exit(0)
	case "owner-generation-finality":
		inherited := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceOwnerGenerationFixture/root/clear/owner/inherited", 2)
		checkSpansByTagValue(inherited, constants.TestFinalStatus, constants.TestStatusSkip, 2)
		checkSpansByTagValue(inherited, constants.TestAttemptToFixPassed, "true", 2)
		os.Exit(0)
	case "parallel-atf-sibling":
		owner := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceParallelATFFixture/root/owner", 2)
		checkSpansByTagValue(owner, constants.TestAttemptToFixPassed, "true", 1)
		checkSpansByTagValue(owner, constants.TestFinalStatus, constants.TestStatusSkip, 1)
		os.Exit(0)
	case "serial-atf-sibling":
		payload, err := os.ReadFile(filepath.Join(pidDir, "serial-atf-sibling-runs"))
		if err != nil || len(payload) != 1 {
			panic(fmt.Sprintf("serial sibling ran %d times, want 1: %v", len(payload), err))
		}
		owner := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceSerialATFFixture/root/owner", 2)
		checkSpansByTagValue(owner, constants.TestAttemptToFixPassed, "true", 1)
		checkSpansByTagValue(owner, constants.TestFinalStatus, constants.TestStatusSkip, 1)
		os.Exit(0)
	case "foreign-suite":
		foreignSuite := "quarantine_process_test.go"
		foreign := checkSpansByResourceName(spans, foreignSuite+".TestQuarantinedRaceForeignSuiteFixture/root/foreign", 1)
		checkSpansByTagValue(foreign, constants.TestStatus, constants.TestStatusSkip, 1)
		checkSpansByTagValue(foreign, constants.TestIsDisabled, "true", 1)
		itr := checkSpansByResourceName(spans, foreignSuite+".TestQuarantinedRaceForeignSuiteFixture/root/itr", 1)
		checkSpansByTagValue(itr, constants.TestSkippedByITR, "true", 1)
		parent := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceForeignSuiteFixture/root/parent", 2)
		checkSpansByTagValue(parent, constants.TestIsAttempToFix, "true", 2)
		child := checkSpansByResourceName(spans, foreignSuite+".TestQuarantinedRaceForeignSuiteFixture/root/parent/child", 2)
		checkSpansByTagValue(child, constants.TestIsAttempToFix, "true", 2)
		checkSpansByTagValue(child, constants.TestIsRetry, "true", 1)
		os.Exit(0)
	case "ancestor-atf":
		child := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceAncestorATFFixture/child", 2)
		checkSpansByTagValue(child, constants.TestIsAttempToFix, "true", 2)
		checkSpansByTagValue(child, constants.TestIsRetry, "true", 1)
		checkSpansByTagValue(child, constants.TestRetryReason, constants.AttemptToFixRetryReason, 1)
		checkSpansByTagValue(child, constants.TestAttemptToFixPassed, "false", 1)
		checkSpansByTagValue(child, constants.TestFinalStatus, constants.TestStatusSkip, 1)
		os.Exit(0)
	case "deferred-descendant-atf-failure":
		root := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceDeferredDescendantATFFixture", 2)
		checkSpansByTagValue(root, constants.TestStatus, constants.TestStatusPass, 2)
		owner := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceDeferredDescendantATFFixture/clear/owner", 2)
		checkSpansByTagValue(owner, constants.TestStatus, constants.TestStatusFail, 2)
		checkSpansByTagValue(owner, constants.TestIsAttempToFix, "true", 2)
		checkSpansByTagValue(owner, constants.TestIsRetry, "true", 1)
		os.Exit(0)
	case "deferred-descendant-inherited-failure":
		owner := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceDeferredDescendantATFFixture/clear/owner", 2)
		checkSpansByTagValue(owner, constants.TestStatus, constants.TestStatusPass, 2)
		inherited := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceDeferredDescendantATFFixture/clear/owner/inherited", 2)
		checkSpansByTagValue(inherited, constants.TestStatus, constants.TestStatusFail, 2)
		os.Exit(0)
	case "deferred-descendant-atf-failfast":
		if _, err := os.Stat(filepath.Join(pidDir, "deferred-owner-continuation-2")); err == nil || !os.IsNotExist(err) {
			panic("deferred descendant retry ran after a failure under -failfast")
		}
		owner := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceDeferredDescendantATFFixture/clear/owner", 1)
		checkSpansByTagValue(owner, constants.TestStatus, constants.TestStatusFail, 1)
		os.Exit(0)
	case "deferred-dynamic-descendant-finality":
		dynamic := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceDeferredDynamicDescendantFixture/dynamic", 1)
		checkSpansByTagValue(dynamic, constants.TestIsAttempToFix, "true", 1)
		checkSpansByTagValue(dynamic, constants.TestFinalStatus, constants.TestStatusSkip, 1)
		checkSpansByTagValue(dynamic, constants.TestAttemptToFixPassed, "true", 1)
		os.Exit(0)
	case "initial-inherited-finality":
		initial := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceInitialInheritedFixture/initial", 1)
		checkSpansByTagValue(initial, constants.TestFinalStatus, constants.TestStatusSkip, 1)
		os.Exit(0)
	case "deferred-aggregate-coverage", "deferred-aggregate-coverage-control":
		os.Exit(0)
	case "terminal-descendants":
		parallelRoot := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceTerminalDescendantsFixture/parallel", 1)
		checkSpansByTagValue(parallelRoot, constants.TestStatus, constants.TestStatusFail, 1)
		parallelChild := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceTerminalDescendantsFixture/parallel/child", 1)
		checkSpansByTagValue(parallelChild, constants.TestStatus, constants.TestStatusFail, 1)
		if parallelRoot[0].Duration() >= parallelChild[0].Duration() {
			panic(fmt.Sprintf("parallel descendant wait leaked into selected root duration: root=%s child=%s", parallelRoot[0].Duration(), parallelChild[0].Duration()))
		}
		panicRoot := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceTerminalDescendantsFixture/panic", 1)
		checkSpansByTagValue(panicRoot, constants.TestStatus, constants.TestStatusFail, 1)
		panicChild := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceTerminalDescendantsFixture/panic/child", 1)
		checkSpansByTagValue(panicChild, constants.TestStatus, constants.TestStatusFail, 1)
		if message, _ := panicChild[0].Tag(ext.ErrorMsg).(string); !strings.Contains(message, "body panic sentinel") {
			panic(fmt.Sprintf("body panic was not reported on the isolated descendant: %q", message))
		}
		goexitRoot := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceTerminalDescendantsFixture/goexit", 1)
		checkSpansByTagValue(goexitRoot, constants.TestStatus, constants.TestStatusFail, 1)
		goexitChild := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceTerminalDescendantsFixture/goexit/child", 1)
		checkSpansByTagValue(goexitChild, constants.TestStatus, constants.TestStatusFail, 1)
		if message, _ := goexitChild[0].Tag(ext.ErrorMsg).(string); !strings.Contains(message, "runtime.Goexit") {
			panic(fmt.Sprintf("bare Goexit was not reported on the isolated descendant: %q", message))
		}
		outputRootName := "TestQuarantinedRaceTerminalDescendantsFixture/output"
		outputRoot := checkSpansByResourceName(spans, suite+"."+outputRootName, 1)
		checkSpansByTagValue(outputRoot, constants.TestStatus, constants.TestStatusFail, 1)
		outputChild := checkSpansByResourceName(spans, suite+"."+outputRootName+"/child", 1)
		checkSpansByTagValue(outputChild, constants.TestStatus, constants.TestStatusFail, 1)
		stdout, stderr := false, false
		for _, entry := range logsEntries {
			if entry.TestName == outputRootName {
				stdout = stdout || strings.Contains(entry.Message, "direct stdout sentinel")
				stderr = stderr || strings.Contains(entry.Message, "direct stderr sentinel")
			}
		}
		if stdout && stderr {
			os.Exit(0)
		}
		panic("non-race child process output was not preserved on the selected root")
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
		for _, fixture := range []string{"Panic", "Failure"} {
			root := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceAncestor"+fixture+"Fixture/isolated", 1)
			checkSpansByTagValue(root, constants.TestStatus, constants.TestStatusPass, 1)
			checkSpansByTagValue(root, ext.ErrorType, "test_panic", 0)
		}
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
		for _, name := range []string{
			"failfast-root-3",
			"failfast-descendant-2", "failfast-descendant-3",
			"failfast-masked-root-2", "failfast-masked-root-3",
		} {
			if _, err := os.Stat(filepath.Join(pidDir, name)); err == nil || !os.IsNotExist(err) {
				panic(name + " ran after a valid failure under -failfast")
			}
		}
		readPID("failfast-root-1")
		readPID("failfast-root-2")
		readPID("failfast-descendant-1")
		readPID("failfast-masked-root-1")
		rootSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceFailfastRootFixture", 2)
		checkSpansByTagValue(rootSpans, constants.TestIsRetry, "true", 1)
		checkSpansByTagValue(rootSpans, constants.TestRetryReason, constants.AttemptToFixRetryReason, 1)
		checkSpansByTagValue(rootSpans, constants.TestAttemptToFixPassed, "false", 1)
		checkSpansByTagValue(rootSpans, constants.TestFinalStatus, constants.TestStatusSkip, 1)
		descendantSpans := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceFailfastDescendantFixture/child", 1)
		checkSpansByTagValue(descendantSpans, constants.TestIsRetry, "true", 0)
		checkSpansByTagValue(descendantSpans, constants.TestAttemptToFixPassed, "false", 0)
		checkSpansByTagValue(descendantSpans, constants.TestFinalStatus, constants.TestStatusSkip, 1)
		maskedChild := checkSpansByResourceName(spans, suite+".TestQuarantinedRaceFailfastMaskedChildFixture/child", 1)
		checkSpansByTagValue(maskedChild, constants.TestStatus, constants.TestStatusFail, 1)
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
	runQuarantinedRaceEndToEnd(t, "failfast", "^TestQuarantinedRaceFailfast(Root|Descendant|MaskedChild)Fixture$", "-test.failfast")
}

func TestQuarantinedRaceParallelAdmissionEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "parallel-admission", "^TestQuarantinedRaceParallelFixture$", "-test.timeout=1m")
}

func TestQuarantinedRaceParallelDeniedEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "parallel-denied", "^TestQuarantinedRaceParallelDeniedFixture$", "-test.timeout=1m")
}

func TestQuarantinedRaceParallelRootsReleaseProcessSlotsEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "parallel-root-slots", "^TestQuarantinedRaceParallelRootsFixture$", "-test.timeout=1m")
}

func TestQuarantinedRaceParallelRootsCoordinateAfterResumeEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "parallel-root-coordination", "^TestQuarantinedRaceParallelRootsFixture$", "-test.timeout=1m")
}

func TestQuarantinedRaceParallelCoverageEndToEnd(t *testing.T) {
	if testing.CoverMode() == "" {
		t.Skip("requires coverage instrumentation")
	}
	runQuarantinedRaceEndToEnd(t, "parallel-coverage", "^TestQuarantinedRaceParallelCoverageFixture$", "-test.timeout=1m")
}

func TestQuarantinedRaceNestedAggregateCoverageEndToEnd(t *testing.T) {
	if testing.CoverMode() == "" {
		t.Skip("requires coverage instrumentation")
	}
	controlPath := filepath.Join(t.TempDir(), "control.out")
	runQuarantinedRaceEndToEnd(t, "nested-aggregate-coverage-control", "^TestQuarantinedRaceAggregateCoverageFixture$", "-test.coverprofile="+controlPath)
	profilePath := filepath.Join(t.TempDir(), "coverage.out")
	runQuarantinedRaceEndToEnd(t, "nested-aggregate-coverage", "^TestQuarantinedRaceAggregateCoverageFixture$", "-test.coverprofile="+profilePath)
	controlCount := processCoverageCountForFunctionBody(t, controlPath, "quarantine_process.go", "processRetryCommonInfoFromSubtreeResult")
	serialCount := processCoverageCountForFunctionBody(t, profilePath, "quarantine_process.go", "processRetryCommonInfoFromSubtreeResult")
	require.Equal(t, controlCount+1, serialCount)
	// The nested subprocesses share Go's outer coverage directory, so compare
	// successive profiles to assert that each scenario merges the marker once.
	parallelPath := filepath.Join(t.TempDir(), "parallel.out")
	runQuarantinedRaceEndToEnd(t, "nested-aggregate-parallel-coverage", "^TestQuarantinedRaceAggregateCoverageFixture$", "-test.coverprofile="+parallelPath)
	require.Equal(t, serialCount+1, processCoverageCountForFunctionBody(t, parallelPath, "quarantine_process.go", "processRetryCommonInfoFromSubtreeResult"))
}

func TestQuarantinedRaceNestedAttemptFamilyEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "nested-attempt-family", "^TestQuarantinedRaceIndependentATFFixture$")
}

func TestQuarantinedRaceDeepNestedAttemptFamilyEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "deep-nested-attempt-family", "^TestQuarantinedRaceDeepFixture$")
}

func TestQuarantinedRaceParallelDurationEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "parallel-duration", "^TestQuarantinedRaceParallelDurationFixture$")
}

func TestQuarantinedRaceParallelWaitDurationEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "parallel-wait-duration", "^TestQuarantinedRaceParallelWaitDurationFixture$")
}

func TestQuarantinedRaceDynamicDescendantFinalityEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "dynamic-descendant-finality", "^TestQuarantinedRaceDynamicDescendantFixture$")
}

func TestQuarantinedRaceManagedDescendantMaskingEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "managed-descendant-masking", "^TestQuarantinedRaceManagedDescendantFixture$")
}

func TestQuarantinedRaceManagedDescendantPreservesConcurrentFailureEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "managed-descendant-concurrent-failure", "^TestQuarantinedRaceManagedDescendantFixture$")
}

func TestQuarantinedRaceManagedDescendantsDoNotLeakConcurrentFailureEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "managed-descendant-concurrent-managed", "^TestQuarantinedRaceManagedDescendantFixture$")
}

func TestQuarantinedRaceManagedDescendantPreservesRootFailureEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "managed-descendant-root-failure", "^TestQuarantinedRaceManagedDescendantFixture$")
}

func TestQuarantinedRaceManagedDescendantPreservesProcessOutputEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "managed-descendant-output", "^TestQuarantinedRaceManagedDescendantFixture$")
}

func TestQuarantinedRaceDeferredDescendantATFFamilyCompletesAndFailsPackageEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "deferred-descendant-atf-failure", "^TestQuarantinedRaceDeferredDescendantATFFixture$")
}

func TestQuarantinedRaceDeferredInheritedATFFailureFailsPackageEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "deferred-descendant-inherited-failure", "^TestQuarantinedRaceDeferredDescendantATFFixture$")
}

func TestQuarantinedRaceDeferredDescendantATFFailfastEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "deferred-descendant-atf-failfast", "^TestQuarantinedRaceDeferredDescendantATFFixture$", "-test.failfast")
}

func TestQuarantinedRaceDeferredDynamicDescendantFinalityEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "deferred-dynamic-descendant-finality", "^TestQuarantinedRaceDeferredDynamicDescendantFixture$")
}

func TestQuarantinedRaceInitialInheritedFinalityEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "initial-inherited-finality", "^TestQuarantinedRaceInitialInheritedFixture$")
}

func TestQuarantinedRaceDeferredAggregateCoverageEndToEnd(t *testing.T) {
	if testing.CoverMode() == "" {
		t.Skip("requires coverage instrumentation")
	}
	controlPath := filepath.Join(t.TempDir(), "control.out")
	runQuarantinedRaceEndToEnd(t, "deferred-aggregate-coverage-control", "^TestQuarantinedRaceDeferredAggregateCoverageFixture$", "-test.coverprofile="+controlPath, "-test.gocoverdir="+t.TempDir())
	profilePath := filepath.Join(t.TempDir(), "coverage.out")
	runQuarantinedRaceEndToEnd(t, "deferred-aggregate-coverage", "^TestQuarantinedRaceDeferredAggregateCoverageFixture$", "-test.coverprofile="+profilePath, "-test.gocoverdir="+t.TempDir())
	controlCount := processCoverageCountForFunctionBody(t, controlPath, "quarantine_process.go", "processRetryAttemptToFixExecutionCount")
	coveredCount := processCoverageCountForFunctionBody(t, profilePath, "quarantine_process.go", "processRetryAttemptToFixExecutionCount")
	require.Equal(t, controlCount+1, coveredCount)
}

func TestQuarantinedRaceNestedRootTerminalsEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "nested-root-terminal", "^TestQuarantinedRaceNestedRootTerminalFixture$")
}

func TestQuarantinedRaceOwnerGenerationFinalityEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "owner-generation-finality", "^TestQuarantinedRaceOwnerGenerationFixture$")
}

func TestQuarantinedRaceParallelATFSiblingEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "parallel-atf-sibling", "^TestQuarantinedRaceParallelATFFixture$", "-test.timeout=1m")
}

func TestQuarantinedRaceSerialATFDoesNotRepeatSiblingEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "serial-atf-sibling", "^TestQuarantinedRaceSerialATFFixture$")
}

func TestQuarantinedRacePreservesDescendantSuiteEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "foreign-suite", "^TestQuarantinedRaceForeignSuiteFixture$")
}

func TestQuarantinedRacePreservesAncestorATFMetadataEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "ancestor-atf", "^TestQuarantinedRaceAncestorATFFixture$")
}

func TestQuarantinedRaceTerminalDescendantsEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "terminal-descendants", "^TestQuarantinedRaceTerminalDescendantsFixture$")
}

func TestQuarantinedRaceBeforeParallelEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "race-before-parallel", "^TestQuarantinedRaceBeforeParallelFixture$")
}

func TestQuarantinedRaceAncestorPanicEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "ancestor-terminal", "^TestQuarantinedRaceAncestor(Panic|Failure)Fixture$")
}

func TestQuarantinedRaceTestifySourceEndToEnd(t *testing.T) {
	runQuarantinedRaceEndToEnd(t, "testify-source", "^TestQuarantinedRaceTestifyFixture$")
}

func runQuarantinedRaceEndToEnd(t *testing.T, scenario, pattern string, extraArgs ...string) {
	t.Helper()
	pidDir := t.TempDir()
	args := buildTestControllerSubprocessArgs(os.Args[1:], pattern, extraArgs...)
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
	if processCoverageCountForFunction(t, profilePath, "quarantine_process.go", functionName) > 0 {
		return
	}
	t.Fatalf("%s has no covered block in merged profile %s", functionName, profilePath)
}

func processCoverageCountForFunction(t *testing.T, profilePath, sourcePath, functionName string) int {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, sourcePath, nil, 0)
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
	total := 0
	for _, line := range strings.Split(string(profile), "\n") {
		separator := strings.LastIndexByte(line, ':')
		if separator < 0 || !strings.HasSuffix(filepath.ToSlash(line[:separator]), "/gotesting/"+filepath.Base(sourcePath)) {
			continue
		}
		var startLine, startColumn, endLine, endColumn, statements, count int
		if _, err := fmt.Sscanf(line[separator+1:], "%d.%d,%d.%d %d %d", &startLine, &startColumn, &endLine, &endColumn, &statements, &count); err == nil &&
			startLine <= finish && endLine >= start && count > 0 {
			total += count
		}
	}
	return total
}

// processCoverageCountForFunctionBody returns the execution count of the
// entry block of functionName's body: among the profile's blocks contained
// within the body's brace positions, the one that starts earliest.
//
// This can't match the body's brace positions exactly: Go 1.27's cmd/cover
// trims blocks to lines containing executable code, so a body block no
// longer spans from '{' to '}' the way it did on earlier toolchains (e.g. a
// gapless single-statement body now starts at the first code token instead
// of the opening brace). Both shapes still yield exactly one contained
// block for the functions this helper is used on, so containment plus
// "earliest start" preserves the original per-entry counting semantics
// across toolchains.
func processCoverageCountForFunctionBody(t *testing.T, profilePath, sourcePath, functionName string) int {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, sourcePath, nil, 0)
	require.NoError(t, err)
	var start, finish token.Position
	for _, declaration := range parsed.Decls {
		if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Name.Name == functionName {
			start, finish = fset.Position(fn.Body.Pos()), fset.Position(fn.Body.End())
			break
		}
	}
	require.NotZero(t, start.Line)
	profile, err := os.ReadFile(profilePath)
	require.NoError(t, err)
	found := false
	var entryStartLine, entryStartColumn, entryCount int
	for _, line := range strings.Split(string(profile), "\n") {
		separator := strings.LastIndexByte(line, ':')
		if separator < 0 || !strings.HasSuffix(filepath.ToSlash(line[:separator]), "/gotesting/"+filepath.Base(sourcePath)) {
			continue
		}
		var startLine, startColumn, endLine, endColumn, statements, count int
		if _, err := fmt.Sscanf(line[separator+1:], "%d.%d,%d.%d %d %d", &startLine, &startColumn, &endLine, &endColumn, &statements, &count); err != nil {
			continue
		}
		if startLine < start.Line || (startLine == start.Line && startColumn < start.Column) {
			continue
		}
		if endLine > finish.Line || (endLine == finish.Line && endColumn > finish.Column) {
			continue
		}
		if !found || startLine < entryStartLine || (startLine == entryStartLine && startColumn < entryStartColumn) {
			found, entryStartLine, entryStartColumn, entryCount = true, startLine, startColumn, count
		}
	}
	if !found {
		t.Fatalf("coverage block for %s not found", functionName)
	}
	return entryCount
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

func TestQuarantinedRaceIndependentATFFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("clear", instrumentTestingTFunc(func(t *testing.T) {
		t.Run("owner", instrumentTestingTFunc(func(t *testing.T) {
			writeQuarantinedRaceIsolationPID(t, "nested-attempt-owner-"+strconv.Itoa(os.Getpid()))
		}))
	}))
}

func TestQuarantinedRaceDeepFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("clear", instrumentTestingTFunc(func(t *testing.T) {
		t.Run("owner", instrumentTestingTFunc(func(t *testing.T) {
			t.Run("none", instrumentTestingTFunc(func(t *testing.T) {
				t.Run("deep", instrumentTestingTFunc(func(t *testing.T) {
					writeQuarantinedRaceIsolationPID(t, "deep-nested-attempt-leaf-"+strconv.Itoa(os.Getpid()))
				}))
			}))
		}))
	}))
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

func TestQuarantinedRaceParallelRootsFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	for _, name := range []string{"one", "two"} {
		t.Run(name, instrumentTestingTFunc(func(t *testing.T) {
			(*T)(t).Parallel()
			writeQuarantinedRaceIsolationProcessPID(t, "parallel-root-"+name)
			if os.Getenv(quarantinedRaceIsolationFixtureEnv) == "parallel-root-coordination" && isProcessRetryChild() {
				peer := "two"
				if name == "two" {
					peer = "one"
				}
				for {
					if _, err := os.Stat(filepath.Join(os.Getenv(quarantinedRaceIsolationPIDDirEnv), "parallel-root-"+peer+"-child")); err == nil {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
		}))
	}
}

func TestQuarantinedRaceParallelCoverageFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	released := make(chan struct{})
	t.Run("isolated", instrumentTestingTFunc(func(t *testing.T) {
		(*T)(t).Parallel()
		<-released
		writeQuarantinedRaceIsolationPID(t, "parallel-coverage-child")
		ready := make(chan struct{}, 2)
		allReady := make(chan struct{})
		go func() {
			<-ready
			<-ready
			close(allReady)
		}()
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
				ready <- struct{}{}
				<-allReady
			}))
		}
	}))
	close(released)
}

func TestQuarantinedRaceParallelDurationFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("isolated", instrumentTestingTFunc(func(t *testing.T) {
		t.Run("suspended", instrumentTestingTFunc(func(t *testing.T) {
			(*T)(t).Parallel()
		}))
		runQuarantinedRaceActiveWork()
	}))
}

func TestQuarantinedRaceParallelWaitDurationFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("root", instrumentTestingTFunc(func(t *testing.T) {
		t.Run("slow", instrumentTestingTFunc(func(t *testing.T) {
			(*T)(t).Parallel()
			runQuarantinedRaceActiveWork()
		}))
	}))
}

func runQuarantinedRaceActiveWork() {
	const rounds = 1_000_000
	request := make(chan struct{})
	acknowledged := make(chan struct{})
	go func() {
		for range rounds {
			<-request
			acknowledged <- struct{}{}
		}
	}()
	for range rounds {
		request <- struct{}{}
		<-acknowledged
	}
}

func TestQuarantinedRaceDynamicDescendantFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("root", instrumentTestingTFunc(func(t *testing.T) {
		marker := filepath.Join(os.Getenv(quarantinedRaceIsolationPIDDirEnv), "dynamic-descendant-seen")
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			return
		}
		require.NoError(t, os.WriteFile(marker, []byte("seen"), 0o600))
		t.Run("dynamic", instrumentTestingTFunc(func(*testing.T) {}))
	}))
}

func TestQuarantinedRaceManagedDescendantFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("root", instrumentTestingTFunc(func(t *testing.T) {
		scenario := os.Getenv(quarantinedRaceIsolationFixtureEnv)
		if scenario == "managed-descendant-concurrent-managed" {
			failed := make(chan struct{})
			firstRelease := make(chan struct{})
			firstDone := make(chan struct{})
			secondStarted := make(chan struct{})
			secondRelease := make(chan struct{})
			var runs sync.WaitGroup
			runs.Add(2)
			go func() {
				defer runs.Done()
				t.Run("first", instrumentTestingTFunc(func(t *testing.T) {
					t.Error("first managed descendant sentinel")
					close(failed)
					<-firstRelease
				}))
				close(firstDone)
			}()
			<-failed
			go func() {
				defer runs.Done()
				t.Run("second", instrumentTestingTFunc(func(*testing.T) {
					close(secondStarted)
					<-secondRelease
				}))
			}()
			<-secondStarted
			close(firstRelease)
			<-firstDone
			close(secondRelease)
			runs.Wait()
			return
		}
		if scenario == "managed-descendant-concurrent-failure" {
			siblingFailed := make(chan struct{})
			t.Run("child", instrumentTestingTFunc(func(t *testing.T) {
				(*T)(t).Parallel()
				<-siblingFailed
				t.Error("managed descendant sentinel")
			}))
			t.Run("sibling", instrumentTestingTFunc(func(t *testing.T) {
				(*T)(t).Parallel()
				t.Error("unmanaged sibling sentinel")
				close(siblingFailed)
			}))
			return
		}
		if scenario == "managed-descendant-root-failure" {
			rootFailed := make(chan struct{})
			t.Run("child", instrumentTestingTFunc(func(t *testing.T) {
				(*T)(t).Parallel()
				<-rootFailed
				t.Error("managed descendant sentinel")
			}))
			t.Error("selected root failure sentinel")
			close(rootFailed)
			return
		}
		passed := t.Run("child", instrumentTestingTFunc(func(t *testing.T) {
			if scenario == "managed-descendant-output" {
				t.Run("grandchild", instrumentTestingTFunc(func(t *testing.T) {
					_, _ = fmt.Fprintln(os.Stdout, "managed descendant stdout sentinel")
					_, _ = fmt.Fprintln(os.Stderr, "managed descendant stderr sentinel")
					t.Error("managed descendant sentinel")
				}))
				return
			}
			t.Error("managed descendant sentinel")
		}))
		if scenario == "managed-descendant-output" {
			return
		}
		path := filepath.Join(os.Getenv(quarantinedRaceIsolationPIDDirEnv), "managed-descendant-run-result")
		require.NoError(t, os.WriteFile(path, []byte(strconv.FormatBool(passed)), 0o600))
	}))
}

func TestQuarantinedRaceNestedRootTerminalFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("panic", instrumentTestingTFunc(func(*testing.T) {
		panic("nested root panic sentinel")
	}))
	t.Run("goexit", instrumentTestingTFunc(func(*testing.T) {
		runtime.Goexit()
	}))
	t.Run("cleanup-panic", instrumentTestingTFunc(func(t *testing.T) {
		t.Cleanup(func() { panic("nested root cleanup panic sentinel") })
	}))
}

func TestQuarantinedRaceOwnerGenerationFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("root", instrumentTestingTFunc(func(t *testing.T) {
		t.Run("clear", instrumentTestingTFunc(func(t *testing.T) {
			t.Run("owner", instrumentTestingTFunc(func(t *testing.T) {
				cfg, err := processRetryChildConfigFromEnv()
				require.NoError(t, err)
				if strings.HasSuffix(cfg.TestName, "/clear/owner") {
					t.Run("inherited", instrumentTestingTFunc(func(*testing.T) {}))
				}
			}))
		}))
	}))
}

func TestQuarantinedRaceParallelATFFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("root", instrumentTestingTFunc(func(t *testing.T) {
		ready := make(chan struct{})
		t.Run("owner", instrumentTestingTFunc(func(t *testing.T) {
			(*T)(t).Parallel()
			<-ready
		}))
		t.Run("sibling", instrumentTestingTFunc(func(t *testing.T) {
			(*T)(t).Parallel()
			close(ready)
		}))
	}))
}

func TestQuarantinedRaceAggregateCoverageFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	scenario := os.Getenv(quarantinedRaceIsolationFixtureEnv)
	t.Run("isolated", instrumentTestingTFunc(func(t *testing.T) {
		if scenario == "nested-aggregate-parallel-coverage" {
			(*T)(t).Parallel()
		}
	}))
	if scenario == "nested-aggregate-coverage" || scenario == "nested-aggregate-parallel-coverage" {
		marker := processRetryCommonInfoFromSubtreeResult(processRetrySubtreeResult{TestName: "aggregate-coverage-marker"})
		quarantinedRaceAncestorCoverage.Store(int64(len(marker.testName)))
	}
}

var quarantinedRaceAncestorCoverage atomic.Int64

func TestQuarantinedRaceTerminalDescendantsFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("parallel", instrumentTestingTFunc(func(t *testing.T) {
		t.Run("child", instrumentTestingTFunc(func(t *testing.T) {
			(*T)(t).Parallel()
			runQuarantinedRaceActiveWork()
			t.Error("parallel descendant sentinel")
		}))
	}))
	t.Run("panic", instrumentTestingTFunc(func(t *testing.T) {
		t.Run("child", instrumentTestingTFunc(func(*testing.T) {
			panic("body panic sentinel")
		}))
	}))
	t.Run("goexit", instrumentTestingTFunc(func(t *testing.T) {
		t.Run("child", instrumentTestingTFunc(func(*testing.T) {
			runtime.Goexit()
		}))
	}))
	t.Run("output", instrumentTestingTFunc(func(t *testing.T) {
		t.Run("child", instrumentTestingTFunc(func(t *testing.T) {
			_, _ = fmt.Fprintln(os.Stdout, "direct stdout sentinel")
			_, _ = fmt.Fprintln(os.Stderr, "direct stderr sentinel")
			t.Error("non-race failure sentinel")
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

func TestQuarantinedRaceAncestorFailureFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("isolated", instrumentTestingTFunc(func(*testing.T) {}))
	if isProcessRetryChild() {
		t.Error("ancestor failure sentinel")
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
		t.Fail()
	}))
}

func TestQuarantinedRaceFailfastMaskedChildFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	cfg, err := processRetryChildConfigFromEnv()
	require.NoError(t, err)
	writeQuarantinedRaceIsolationPID(t, "failfast-masked-root-"+strconv.Itoa(cfg.Attempt))
	t.Run("child", instrumentTestingTFunc(func(t *testing.T) {
		t.Fail()
	}))
}

func TestQuarantinedRaceSerialATFFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("root", instrumentTestingTFunc(func(t *testing.T) {
		t.Run("sibling", instrumentTestingTFunc(func(t *testing.T) {
			path := filepath.Join(os.Getenv(quarantinedRaceIsolationPIDDirEnv), "serial-atf-sibling-runs")
			file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			require.NoError(t, err)
			_, err = file.WriteString("x")
			require.NoError(t, err)
			require.NoError(t, file.Close())
		}))
		t.Run("owner", instrumentTestingTFunc(func(*testing.T) {}))
	}))
}

func TestQuarantinedRaceForeignSuiteFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("root", instrumentTestingTFunc(func(t *testing.T) {
		t.Run("foreign", instrumentTestingTFunc(quarantinedRaceForeignSuiteCallback))
		t.Run("itr", instrumentTestingTFunc(quarantinedRaceForeignSuiteITRCallback))
		t.Run("parent", instrumentTestingTFunc(quarantinedRaceForeignSuiteParentCallback))
	}))
}

func quarantinedRaceForeignSuiteParentCallback(t *testing.T) {
	t.Run("child", instrumentTestingTFunc(quarantinedRaceHomeSuiteCallback))
}

func TestQuarantinedRaceParallelDeniedFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("isolated", instrumentTestingTFunc(func(t *testing.T) {
		t.Setenv("DD_TEST_PARALLEL_ADMISSION", "denied")
		t.Parallel()
	}))
}

func TestQuarantinedRaceAncestorATFFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("child", instrumentTestingTFunc(func(t *testing.T) {
		cfg, err := processRetryChildConfigFromEnv()
		require.NoError(t, err)
		if cfg.RetryReason == processRetrySubtreeReason {
			t.Fail()
		}
	}))
}

func TestQuarantinedRaceDeferredDescendantATFFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("quarantined", instrumentTestingTFunc(func(*testing.T) {}))
	t.Run("clear", instrumentTestingTFunc(func(t *testing.T) {
		if isProcessRetryChild() {
			t.Run("owner", instrumentTestingTFunc(func(t *testing.T) {
				scenario := os.Getenv(quarantinedRaceIsolationFixtureEnv)
				cfg, err := processRetryChildConfigFromEnv()
				require.NoError(t, err)
				if scenario == "deferred-descendant-atf-failfast" && strings.HasSuffix(cfg.TestName, "/clear/owner") {
					writeQuarantinedRaceIsolationPID(t, "deferred-owner-continuation-"+strconv.Itoa(cfg.Attempt))
				}
				if scenario == "deferred-descendant-inherited-failure" {
					t.Run("inherited", instrumentTestingTFunc(func(t *testing.T) { t.Error("inherited failure sentinel") }))
					return
				}
				t.Error("deferred descendant attempt-to-fix sentinel")
			}))
		}
	}))
}

func TestQuarantinedRaceDeferredDynamicDescendantFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("quarantined", instrumentTestingTFunc(func(*testing.T) {}))
	if !isProcessRetryChild() {
		return
	}
	cfg, err := processRetryChildConfigFromEnv()
	require.NoError(t, err)
	if cfg.Attempt == 1 {
		t.Run("dynamic", instrumentTestingTFunc(func(*testing.T) {}))
	}
}

func TestQuarantinedRaceInitialInheritedFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	if !isProcessRetryChild() {
		t.Run("initial", instrumentTestingTFunc(func(*testing.T) {}))
		return
	}
	cfg, err := processRetryChildConfigFromEnv()
	require.NoError(t, err)
	if strings.HasSuffix(cfg.TestName, "/initial") {
		t.Run("initial", instrumentTestingTFunc(func(*testing.T) {}))
	}
}

func TestQuarantinedRaceDeferredAggregateCoverageFixture(t *testing.T) {
	if !quarantinedRaceIsolationFixtureSelected() {
		t.Skip("fixture subprocess only")
	}
	t.Run("quarantined", instrumentTestingTFunc(func(*testing.T) {}))
	if !isProcessRetryChild() {
		return
	}
	cfg, err := processRetryChildConfigFromEnv()
	require.NoError(t, err)
	if cfg.TestName == "TestQuarantinedRaceDeferredAggregateCoverageFixture" && os.Getenv(quarantinedRaceIsolationFixtureEnv) == "deferred-aggregate-coverage" {
		require.Equal(t, 1, processRetryAttemptToFixExecutionCount(1))
	}
}
