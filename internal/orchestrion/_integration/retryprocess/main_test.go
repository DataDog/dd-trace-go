// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package retryprocess

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
)

const orchestrionRetryProcessInvalidConfigEnv = "ORCHESTRION_RETRY_PROCESS_INVALID_CONFIG"
const orchestrionRetryProcessHybridEnv = "ORCHESTRION_RETRY_PROCESS_HYBRID"
const orchestrionRetryProcessHybridParentEnv = "ORCHESTRION_RETRY_PROCESS_HYBRID_PARENT"
const orchestrionRetryProcessPureParentEnv = "ORCHESTRION_RETRY_PROCESS_PURE_PARENT"
const orchestrionRetryProcessSelectedSubtestA2FEnv = "ORCHESTRION_RETRY_PROCESS_SELECTED_SUBTEST_A2F"
const orchestrionRetryProcessSequentialMRunEnv = "ORCHESTRION_RETRY_PROCESS_SEQUENTIAL_M_RUN"
const orchestrionRetryProcessDuplicateChildMRunEnv = "ORCHESTRION_RETRY_PROCESS_DUPLICATE_CHILD_M_RUN"
const orchestrionRetryProcessCleanupPathEnv = "ORCHESTRION_RETRY_PROCESS_CLEANUP_PATH"
const orchestrionRetryProcessSubtestPanicContinuedPathEnv = "ORCHESTRION_RETRY_PROCESS_SUBTEST_PANIC_CONTINUED_PATH"
const orchestrionRetryProcessChildAPIKey = "orchestrion-process-retry-child-api-key"
const orchestrionRetryProcessChildRunFilter = "^TestOrchestrionRetryProcess(Selected|Unselected)Child$"
const orchestrionRetryProcessMetadataRunFilter = "^TestOrchestrionRetryProcess(Error|Errorf|Fatal|Fatalf|Skip|Skipf|SubtestError|SubtestPanic|ParallelSubtestPanic|SubtestThenTopLevelSkip|Unselected)Child$"
const orchestrionRetryProcessHybridRunFilter = "^TestOrchestrionRetryProcessHybrid(Panic|Unselected)Child$"

const (
	orchestrionRetryProcessControlVersion        = 1
	orchestrionRetryProcessControlAttemptReady   = "attempt_ready"
	orchestrionRetryProcessControlAdmission      = "body_admission_request"
	orchestrionRetryProcessControlBodyAdmitted   = "body_admitted"
	orchestrionRetryProcessControlRunBody        = "run_body"
	orchestrionRetryProcessControlParallel       = "parallel_request"
	orchestrionRetryProcessControlParallelReady  = "parallel_resume"
	orchestrionRetryProcessControlTerminalReady  = "controlled_terminal_ready"
	orchestrionRetryProcessControlTerminalCommit = "controlled_terminal_commit"
	orchestrionRetryProcessControlTerminalDone   = "controlled_terminal_committed"
	orchestrionRetryProcessControlAbort          = "abort"
)

var orchestrionRetryProcessHybridParentRuns atomic.Int32
var orchestrionRetryProcessPureParentRuns atomic.Int32
var orchestrionRetryProcessSelectedSubtestA2FParentRuns atomic.Int32
var orchestrionRetryProcessSelectedSubtestA2FRuns atomic.Int32
var orchestrionRetryProcessSequentialMRunRuns atomic.Int32
var orchestrionRetryProcessSequentialMRunNestedRuns atomic.Int32

func requireOrchestrionProcessRetryContainmentForTesting(t testing.TB) {
	t.Helper()
	if !gotesting.ProcessRetryContainmentSupported() {
		t.Skip("process retry fixture requires process-tree containment")
	}
}

type retryProcessChildResult struct {
	Version      int    `json:"version"`
	TestName     string `json:"test_name"`
	Attempt      int    `json:"attempt"`
	RetryReason  string `json:"retry_reason"`
	Status       string `json:"status"`
	Failed       bool   `json:"failed"`
	Skipped      bool   `json:"skipped"`
	Panic        bool   `json:"panic"`
	ErrorType    string `json:"error_type"`
	ErrorMessage string `json:"error_message"`
	ErrorStack   string `json:"error_stack"`
	SkipReason   string `json:"skip_reason"`
	ResultError  string `json:"result_error"`
}

type orchestrionCountingStringer struct {
	value string
	calls atomic.Int32
}

func (s *orchestrionCountingStringer) String() string {
	s.calls.Add(1)
	return s.value
}

func requireSingleOrchestrionFormatting(t *testing.T, value string) *orchestrionCountingStringer {
	t.Helper()
	stringer := &orchestrionCountingStringer{value: value}
	t.Cleanup(func() {
		if got := stringer.calls.Load(); got != 1 {
			panic(fmt.Sprintf("formatted %d times, want exactly once", got))
		}
	})
	return stringer
}

type orchestrionRetryProcessControlConfig struct {
	Version                int    `json:"version"`
	Transport              string `json:"transport"`
	TestName               string `json:"test_name"`
	Attempt                int    `json:"attempt"`
	RetryReason            string `json:"retry_reason"`
	ReadEndpoint           uint64 `json:"read_endpoint"`
	WriteEndpoint          uint64 `json:"write_endpoint"`
	ParentDeadlineUnixNano int64  `json:"parent_deadline_unix_nano"`
	ParentDeadlineOK       bool   `json:"parent_deadline_ok"`
	ObservedGOMAXPROCS     int    `json:"observed_gomaxprocs"`
}

type orchestrionRetryProcessControlFrame struct {
	Version     int    `json:"version"`
	TestName    string `json:"test_name"`
	Attempt     int    `json:"attempt"`
	RetryReason string `json:"retry_reason"`
	Sequence    uint64 `json:"sequence"`
	Kind        string `json:"kind"`
	Reason      string `json:"reason,omitempty"`
}

type orchestrionRetryProcessControlTransport struct {
	read       *os.File
	write      *os.File
	childRead  *os.File
	childWrite *os.File
	config     orchestrionRetryProcessControlConfig
}

func (c *orchestrionRetryProcessControlTransport) closeChildEndpoints() {
	if c == nil {
		return
	}
	if c.childRead != nil {
		_ = c.childRead.Close()
	}
	if c.childWrite != nil {
		_ = c.childWrite.Close()
	}
	c.childRead = nil
	c.childWrite = nil
}

func (c *orchestrionRetryProcessControlTransport) close() {
	if c == nil {
		return
	}
	for _, file := range []*os.File{c.read, c.write, c.childRead, c.childWrite} {
		if file != nil {
			_ = file.Close()
		}
	}
}

type orchestrionRetryProcessParentHarness struct {
	server                     *httptest.Server
	tracer                     mocktracer.Tracer
	settingsRequests           atomic.Int32
	managementRequests         atomic.Int32
	childRequests              atomic.Int32
	unknownRequests            atomic.Int32
	expectedManagementRequests bool
}

func TestMain(m *testing.M) {
	if !built.WithOrchestrion {
		panic("Orchestrion is not enabled, please run this test with orchestrion")
	}
	if orchestrionRetryProcessChild() {
		_ = os.Setenv(constants.APIKeyEnvironmentVariable, orchestrionRetryProcessChildAPIKey)
	}
	if orchestrionRetryProcessChild() && orchestrionRetryProcessEnv(orchestrionRetryProcessDuplicateChildMRunEnv) == "true" {
		firstExitCode := m.Run()
		secondExitCode := m.Run()
		if firstExitCode != 0 {
			os.Exit(firstExitCode)
		}
		os.Exit(secondExitCode)
	}
	if orchestrionRetryProcessEnv(orchestrionRetryProcessHybridParentEnv) == "true" {
		if orchestrionRetryProcessChild() {
			os.Exit(gotesting.RunM(m))
		}
		os.Exit(runOrchestrionRetryProcessHybridParent(m))
	}
	if orchestrionRetryProcessEnv(orchestrionRetryProcessPureParentEnv) == "true" {
		if orchestrionRetryProcessChild() {
			os.Exit(m.Run())
		}
		os.Exit(runOrchestrionRetryProcessPureParent(m))
	}
	if orchestrionRetryProcessEnv(orchestrionRetryProcessSelectedSubtestA2FEnv) == "true" {
		os.Exit(runOrchestrionRetryProcessSelectedSubtestA2F(m))
	}
	if orchestrionRetryProcessEnv(orchestrionRetryProcessSequentialMRunEnv) == "true" {
		os.Exit(runOrchestrionRetryProcessSequentialMRun(m))
	}
	if orchestrionRetryProcessEnv(orchestrionRetryProcessHybridEnv) == "true" {
		os.Exit(gotesting.RunM(m))
	}
	os.Exit(m.Run())
}

func TestOrchestrionTestingMControlContract(t *testing.T) {
	const childEnv = "ORCHESTRION_TESTING_M_CONTROL_CONTRACT_CHILD"
	if os.Getenv(childEnv) == "true" {
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestOrchestrionTestingMControlContract$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(),
		childEnv+"=true",
		constants.CIVisibilityEnabledEnvironmentVariable+"=false",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("testing.M control contract subprocess failed: %v\n%s", err, output)
	}
	if !bytes.Contains(output, []byte("--- PASS: TestOrchestrionTestingMControlContract")) {
		t.Fatalf("testing.M control contract subprocess did not pass:\n%s", output)
	}
}

func TestOrchestrionRetryProcessSequentialMRunRestoresNativeWorkloadsController(t *testing.T) {
	if orchestrionRetryProcessChild() {
		t.Skip("controller runs only in the parent process")
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestOrchestrionRetryProcessSequentialMRunFixture$", "-test.v")
	cmd.Env = append(os.Environ(), orchestrionRetryProcessSequentialMRunEnv+"=true")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sequential M.Run subprocess failed: %v\n%s", err, output)
	}
}

func runOrchestrionRetryProcessSequentialMRun(m *testing.M) int {
	harness := newOrchestrionRetryProcessParentHarness("orchestrion-sequential-m-run", "orchestrion-sequential-m-run-api-key")
	defer harness.close()

	firstExitCode := m.Run()
	secondExitCode := m.Run()
	if firstExitCode != 0 {
		return firstExitCode
	}
	if secondExitCode != 0 {
		return secondExitCode
	}
	if got := orchestrionRetryProcessSequentialMRunRuns.Load(); got != 2 {
		panic(fmt.Sprintf("unexpected sequential M.Run body count: got=%d want=2", got))
	}

	const (
		resourceName = "main_test.go.TestOrchestrionRetryProcessSequentialMRunFixture"
		nestedName   = resourceName + "/nested"
	)
	if got := orchestrionRetryProcessSequentialMRunNestedRuns.Load(); got != 2 {
		panic(fmt.Sprintf("unexpected sequential M.Run nested body count: got=%d want=2", got))
	}
	testSpans := 0
	nestedSpans := 0
	for _, span := range harness.tracer.FinishedSpans() {
		switch span.Tag(ext.ResourceName) {
		case resourceName:
			testSpans++
		case nestedName:
			nestedSpans++
		}
	}
	if testSpans != 1 || nestedSpans != 1 {
		panic(fmt.Sprintf("unexpected sequential M.Run span counts: top=%d nested=%d want=1/1", testSpans, nestedSpans))
	}
	return 0
}

func TestOrchestrionRetryProcessSequentialMRunFixture(t *testing.T) {
	if orchestrionRetryProcessEnv(orchestrionRetryProcessSequentialMRunEnv) != "true" {
		t.Skip("fixture runs only in the sequential M.Run subprocess")
	}
	orchestrionRetryProcessSequentialMRunRuns.Add(1)
	t.Run("nested", func(*testing.T) {
		orchestrionRetryProcessSequentialMRunNestedRuns.Add(1)
	})
}

func TestOrchestrionRetryProcessPureParentUsesProcessRetryController(t *testing.T) {
	requireOrchestrionProcessRetryContainmentForTesting(t)
	if orchestrionRetryProcessChild() {
		t.Skip("controller runs only in the parent process")
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestOrchestrionRetryProcessPureParentFixture$", "-test.v")
	cmd.Env = append(os.Environ(), orchestrionRetryProcessPureParentEnv+"=true")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pure Orchestrion parent subprocess failed: %v\n%s", err, output)
	}
}

func runOrchestrionRetryProcessPureParent(m *testing.M) int {
	harness := newOrchestrionRetryProcessParentHarness("orchestrion-pure-parent", "orchestrion-pure-parent-api-key")
	defer harness.close()
	exitCode := m.Run()
	if exitCode == 0 {
		const resourceName = "main_test.go.TestOrchestrionRetryProcessPureParentFixture"
		testSpans := 0
		processRetrySpans := 0
		for _, span := range harness.tracer.FinishedSpans() {
			if span.Tag(ext.ResourceName) != resourceName {
				continue
			}
			testSpans++
			if span.Tag(constants.TestRetryExecutionMode) == "process" {
				processRetrySpans++
			}
		}
		if testSpans != 2 || processRetrySpans != 1 {
			panic(fmt.Sprintf(
				"unexpected pure Orchestrion parent spans: tests=%d process_retries=%d",
				testSpans,
				processRetrySpans,
			))
		}
		harness.assertRequests("pure parent")
	}
	return exitCode
}

func TestOrchestrionRetryProcessHybridParentOwnershipController(t *testing.T) {
	requireOrchestrionProcessRetryContainmentForTesting(t)
	if orchestrionRetryProcessChild() {
		t.Skip("controller runs only in the parent process")
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestOrchestrionRetryProcessHybridParentFixture$", "-test.v")
	cmd.Env = append(os.Environ(), orchestrionRetryProcessHybridParentEnv+"=true")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("orchestrion hybrid parent subprocess failed: %v\n%s", err, output)
	}
}

func runOrchestrionRetryProcessHybridParent(m *testing.M) int {
	harness := newOrchestrionRetryProcessParentHarness("orchestrion-hybrid-parent", "orchestrion-hybrid-parent-api-key")
	defer harness.close()
	exitCode := gotesting.RunM(m)
	if exitCode == 0 {
		assertOrchestrionRetryProcessHybridParentSpans(harness.tracer)
		harness.assertRequests("hybrid parent")
	}
	return exitCode
}

func TestOrchestrionRetryProcessSelectedSubtestAttemptToFixController(t *testing.T) {
	if orchestrionRetryProcessChild() {
		t.Skip("controller runs only in the parent process")
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestOrchestrionRetryProcessSelectedSubtestAttemptToFixFixture$", "-test.v")
	cmd.Env = append(os.Environ(), orchestrionRetryProcessSelectedSubtestA2FEnv+"=true")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Orchestrion selected-subtest attempt-to-fix subprocess failed: %v\n%s", err, output)
	}
}

func runOrchestrionRetryProcessSelectedSubtestA2F(m *testing.M) int {
	const (
		moduleName = "github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/retryprocess"
		suiteName  = "main_test.go"
		testName   = "TestOrchestrionRetryProcessSelectedSubtestAttemptToFixFixture/selected"
	)
	managementData := &civisibilitynet.TestManagementTestsResponseDataModules{
		Modules: map[string]civisibilitynet.TestManagementTestsResponseDataSuites{
			moduleName: {
				Suites: map[string]civisibilitynet.TestManagementTestsResponseDataTests{
					suiteName: {
						Tests: map[string]civisibilitynet.TestManagementTestsResponseDataTestProperties{
							testName: {
								Properties: civisibilitynet.TestManagementTestsResponseDataTestPropertiesAttributes{
									AttemptToFix: true,
								},
							},
						},
					},
				},
			},
		},
	}
	harness := newOrchestrionRetryProcessParentHarnessWithConfig(
		"orchestrion-selected-subtest-a2f",
		"orchestrion-selected-subtest-a2f-api-key",
		orchestrionRetryProcessHarnessConfig{
			attemptToFixRetries: 2,
			managementData:      managementData,
		},
	)
	defer harness.close()
	exitCode := m.Run()
	if exitCode == 0 {
		if got := orchestrionRetryProcessSelectedSubtestA2FParentRuns.Load(); got != 1 {
			panic(fmt.Sprintf("unexpected selected-subtest parent run count: got=%d want=1", got))
		}
		if got := orchestrionRetryProcessSelectedSubtestA2FRuns.Load(); got != 1 {
			panic(fmt.Sprintf("unexpected selected-subtest attempt count: got=%d want=1", got))
		}
		assertOrchestrionRetryProcessSelectedSubtestA2FSpans(harness.tracer)
		harness.assertRequests("selected-subtest attempt-to-fix")
	}
	return exitCode
}

type orchestrionRetryProcessHarnessConfig struct {
	flakyRetries        bool
	attemptToFixRetries int
	managementData      *civisibilitynet.TestManagementTestsResponseDataModules
}

func newOrchestrionRetryProcessParentHarness(settingsID, apiKey string) *orchestrionRetryProcessParentHarness {
	return newOrchestrionRetryProcessParentHarnessWithConfig(settingsID, apiKey, orchestrionRetryProcessHarnessConfig{
		flakyRetries: true,
	})
}

func newOrchestrionRetryProcessParentHarnessWithConfig(
	settingsID, apiKey string,
	config orchestrionRetryProcessHarnessConfig,
) *orchestrionRetryProcessParentHarness {
	harness := &orchestrionRetryProcessParentHarness{
		expectedManagementRequests: config.managementData != nil,
	}
	harness.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("dd-api-key") == orchestrionRetryProcessChildAPIKey {
			harness.childRequests.Add(1)
		}
		switch r.URL.Path {
		case "/api/v2/libraries/tests/services/setting":
			harness.settingsRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			response := struct {
				Data struct {
					ID         string                               `json:"id"`
					Type       string                               `json:"type"`
					Attributes civisibilitynet.SettingsResponseData `json:"attributes"`
				} `json:"data"`
			}{}
			response.Data.ID = settingsID
			response.Data.Type = "ci_app_libraries_settings"
			response.Data.Attributes.FlakyTestRetriesEnabled = config.flakyRetries
			response.Data.Attributes.TestManagement.Enabled = config.managementData != nil
			response.Data.Attributes.TestManagement.AttemptToFixRetries = config.attemptToFixRetries
			_ = json.NewEncoder(w).Encode(&response)
		case "/api/v2/test/libraries/test-management/tests":
			if config.managementData == nil {
				harness.unknownRequests.Add(1)
				http.NotFound(w, r)
				return
			}
			harness.managementRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			response := struct {
				Data struct {
					ID         string                                                 `json:"id"`
					Type       string                                                 `json:"type"`
					Attributes civisibilitynet.TestManagementTestsResponseDataModules `json:"attributes"`
				} `json:"data"`
			}{}
			response.Data.ID = "test-management"
			response.Data.Type = "ci_app_libraries_tests"
			response.Data.Attributes = *config.managementData
			_ = json.NewEncoder(w).Encode(&response)
		case "/api/v2/git/repository/search_commits":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{}"))
		default:
			harness.unknownRequests.Add(1)
			http.NotFound(w, r)
		}
	}))

	_ = os.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "1")
	_ = os.Setenv(constants.CIVisibilityAgentlessURLEnvironmentVariable, harness.server.URL)
	_ = os.Setenv(constants.CIVisibilityGitUploadEnabledEnvironmentVariable, "false")
	_ = os.Setenv(constants.APIKeyEnvironmentVariable, apiKey)
	_ = os.Setenv(constants.CIVisibilityFlakyRetryCountEnvironmentVariable, "1")
	_ = os.Setenv(constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	_ = os.Setenv(constants.CIVisibilitySubtestFeaturesEnabled, "true")
	if config.attemptToFixRetries > 0 {
		_ = os.Setenv(constants.CIVisibilityTestManagementAttemptToFixRetriesEnvironmentVariable, strconv.Itoa(config.attemptToFixRetries))
	}
	harness.tracer = integrations.InitializeCIVisibilityMock()
	return harness
}

func (h *orchestrionRetryProcessParentHarness) close() {
	h.server.Close()
}

func (h *orchestrionRetryProcessParentHarness) assertRequests(name string) {
	if got := h.settingsRequests.Load(); got != 1 {
		panic(fmt.Sprintf("expected one %s settings request, got %d", name, got))
	}
	if got := h.childRequests.Load(); got != 0 {
		panic(fmt.Sprintf("expected zero %s child-owned CI Visibility requests, got %d", name, got))
	}
	if got := h.unknownRequests.Load(); got != 0 {
		panic(fmt.Sprintf("unexpected %s request count: %d", name, got))
	}
	wantManagementRequests := int32(0)
	if h.expectedManagementRequests {
		wantManagementRequests = 1
	}
	if got := h.managementRequests.Load(); got != wantManagementRequests {
		panic(fmt.Sprintf("unexpected %s test-management request count: got=%d want=%d", name, got, wantManagementRequests))
	}
}

func assertOrchestrionRetryProcessSelectedSubtestA2FSpans(tracer mocktracer.Tracer) {
	const (
		parentResource  = "main_test.go.TestOrchestrionRetryProcessSelectedSubtestAttemptToFixFixture"
		subtestResource = parentResource + "/selected"
	)
	parentSpans := 0
	subtestSpans := 0
	processRetrySpans := 0
	retrySpans := 0
	for _, span := range tracer.FinishedSpans() {
		resource, _ := span.Tag(ext.ResourceName).(string)
		switch resource {
		case parentResource:
			parentSpans++
		case subtestResource:
			subtestSpans++
			if span.Tag(constants.TestRetryExecutionMode) == "process" {
				processRetrySpans++
			}
			if span.Tag(constants.TestIsRetry) == "true" {
				retrySpans++
			}
		}
	}
	if parentSpans != 1 || subtestSpans != 1 || processRetrySpans != 0 || retrySpans != 0 {
		panic(fmt.Sprintf(
			"unexpected selected-subtest attempt-to-fix spans: parent=%d subtest=%d process=%d retries=%d",
			parentSpans,
			subtestSpans,
			processRetrySpans,
			retrySpans,
		))
	}
}

func assertOrchestrionRetryProcessHybridParentSpans(tracer mocktracer.Tracer) {
	const resourceName = "main_test.go.TestOrchestrionRetryProcessHybridParentFixture"
	counts := map[string]int{}
	processRetrySpans := 0
	testSpans := 0
	for _, span := range tracer.FinishedSpans() {
		spanType, _ := span.Tag(ext.SpanType).(string)
		counts[spanType]++
		if span.Tag(ext.ResourceName) != resourceName {
			continue
		}
		testSpans++
		if span.Tag(constants.TestRetryExecutionMode) == "process" {
			processRetrySpans++
		}
	}
	if testSpans != 2 || processRetrySpans != 1 {
		panic(fmt.Sprintf("unexpected hybrid parent test spans: tests=%d process_retries=%d", testSpans, processRetrySpans))
	}
	for spanType, want := range map[string]int{
		constants.SpanTypeTestSession: 1,
		constants.SpanTypeTestModule:  1,
		constants.SpanTypeTestSuite:   1,
		constants.SpanTypeTest:        2,
	} {
		if got := counts[spanType]; got != want {
			panic(fmt.Sprintf("unexpected hybrid parent %s span count: got=%d want=%d", spanType, got, want))
		}
	}
}

func TestOrchestrionRetryProcessHybridOwnershipController(t *testing.T) {
	if orchestrionRetryProcessChild() {
		t.Skip("controller runs only in the parent process")
	}

	tempDir := t.TempDir()
	resultPath := filepath.Join(tempDir, "result.json")
	cleanupPath := filepath.Join(tempDir, "cleanup.txt")
	cmd := exec.Command(os.Args[0], "-test.run="+orchestrionRetryProcessHybridRunFilter, "-test.v")
	cmd.Env = append(os.Environ(),
		orchestrionRetryProcessHybridEnv+"=true",
		orchestrionRetryProcessCleanupPathEnv+"="+cleanupPath,
		constants.CIVisibilityInternalRetryProcessChild+"=true",
		constants.CIVisibilityInternalRetryProcessResultPath+"="+resultPath,
		constants.CIVisibilityInternalRetryProcessTestName+"=TestOrchestrionRetryProcessHybridPanicChild",
		constants.CIVisibilityInternalRetryProcessAttempt+"=1",
		constants.CIVisibilityInternalRetryProcessReason+"="+constants.AutoTestRetriesRetryReason,
	)
	output, err := runOrchestrionRetryProcessChildCommand(
		t,
		cmd,
		resultPath,
		"TestOrchestrionRetryProcessHybridPanicChild",
		true,
	)
	if err == nil {
		t.Fatalf("orchestrion hybrid panic child unexpectedly passed\n%s", output)
	}

	result := decodeOrchestrionRetryProcessResult(t, resultPath, output)
	if result.Status != "controlled_panic_ready" || !result.Failed || !result.Panic || result.ErrorType != "panic" ||
		result.ErrorMessage != "orchestrion hybrid panic sentinel" || result.ErrorStack == "" {
		t.Fatalf("unexpected hybrid panic result: %+v\n%s", result, output)
	}
	cleanup, readErr := os.ReadFile(cleanupPath)
	if readErr != nil {
		t.Fatalf("reading hybrid cleanup result: %v\n%s", readErr, output)
	}
	if string(cleanup) != "x" {
		t.Fatalf("hybrid cleanup ran more than once: %q\n%s", cleanup, output)
	}
}

func TestOrchestrionRetryProcessHybridPanicChild(t *testing.T) {
	if !orchestrionRetryProcessChild() {
		t.Skip("selected child fixture runs only in process retry child mode")
	}
	cleanupPath := orchestrionRetryProcessEnv(orchestrionRetryProcessCleanupPathEnv)
	t.Cleanup(func() {
		file, err := os.OpenFile(cleanupPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatalf("open hybrid cleanup result: %v", err)
		}
		defer file.Close()
		if _, err := file.WriteString("x"); err != nil {
			t.Fatalf("write hybrid cleanup result: %v", err)
		}
	})
	panic("orchestrion hybrid panic sentinel")
}

func TestOrchestrionRetryProcessHybridUnselectedChild(t *testing.T) {
	if orchestrionRetryProcessChild() && orchestrionRetryProcessEnv(orchestrionRetryProcessHybridEnv) == "true" {
		t.Fatal("unselected hybrid test ran in process retry child mode")
	}
}

func TestOrchestrionRetryProcessChildModeController(t *testing.T) {
	if orchestrionRetryProcessChild() {
		t.Skip("controller runs only in the parent process")
	}

	result, output, err, _ := runOrchestrionRetryProcessChild(
		t,
		orchestrionRetryProcessChildRunFilter,
		"TestOrchestrionRetryProcessSelectedChild",
		true,
		nil,
	)
	if err != nil {
		t.Fatalf("orchestrion child process failed: %v\n%s", err, output)
	}

	if result.Version != 1 ||
		result.TestName != "TestOrchestrionRetryProcessSelectedChild" ||
		result.Attempt != 1 ||
		result.RetryReason != constants.AutoTestRetriesRetryReason ||
		result.Status != "pass" ||
		result.Failed ||
		result.Skipped {
		t.Fatalf("unexpected child result: %+v\n%s", result, output)
	}
}

func TestOrchestrionRetryProcessDuplicateChildMRunIsTerminalController(t *testing.T) {
	if orchestrionRetryProcessChild() {
		t.Skip("controller runs only in the parent process")
	}

	result, output, err, _ := runOrchestrionRetryProcessChild(
		t,
		orchestrionRetryProcessChildRunFilter,
		"TestOrchestrionRetryProcessSelectedChild",
		true,
		func(string) []string { return []string{orchestrionRetryProcessDuplicateChildMRunEnv + "=true"} },
	)
	if err == nil {
		t.Fatalf("duplicate child M.Run unexpectedly succeeded\n%s", output)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("duplicate child M.Run returned unexpected error: %v\n%s", err, output)
	}
	if !strings.Contains(err.Error(), "testmain_multiple_m_run") {
		t.Fatalf("duplicate child M.Run did not report its terminal reason: %v\n%s", err, output)
	}
	if result.Status != "pass" {
		t.Fatalf("first owned body result was not preserved: %+v\n%s", result, output)
	}
}

func TestOrchestrionRetryProcessChildMetadataController(t *testing.T) {
	if orchestrionRetryProcessChild() {
		t.Skip("controller runs only in the parent process")
	}

	tests := []struct {
		name        string
		testName    string
		wantExitErr bool
		assert      func(*testing.T, retryProcessChildResult, []byte)
	}{
		{
			name:        "error",
			testName:    "TestOrchestrionRetryProcessErrorChild",
			wantExitErr: true,
			assert: func(t *testing.T, result retryProcessChildResult, output []byte) {
				if result.Status != "fail" || !result.Failed || result.Panic || result.ErrorType == "" ||
					result.ErrorMessage != "orchestrion error sentinel" || result.ErrorStack == "" {
					t.Fatalf("unexpected orchestrion error result: %+v\n%s", result, output)
				}
			},
		},
		{
			name:        "errorf",
			testName:    "TestOrchestrionRetryProcessErrorfChild",
			wantExitErr: true,
			assert: func(t *testing.T, result retryProcessChildResult, output []byte) {
				if result.Status != "fail" || !result.Failed || result.Panic || result.ErrorType != "Errorf" ||
					result.ErrorMessage != "orchestrion errorf sentinel" || result.ErrorStack == "" {
					t.Fatalf("unexpected orchestrion errorf result: %+v\n%s", result, output)
				}
			},
		},
		{
			name:        "fatal",
			testName:    "TestOrchestrionRetryProcessFatalChild",
			wantExitErr: true,
			assert: func(t *testing.T, result retryProcessChildResult, output []byte) {
				if result.Status != "fail" || !result.Failed || result.Panic || result.ErrorType != "Fatal" ||
					result.ErrorMessage != "orchestrion fatal sentinel" || result.ErrorStack == "" {
					t.Fatalf("unexpected orchestrion fatal result: %+v\n%s", result, output)
				}
			},
		},
		{
			name:        "fatalf",
			testName:    "TestOrchestrionRetryProcessFatalfChild",
			wantExitErr: true,
			assert: func(t *testing.T, result retryProcessChildResult, output []byte) {
				if result.Status != "fail" || !result.Failed || result.Panic || result.ErrorType != "Fatalf" ||
					result.ErrorMessage != "orchestrion fatalf sentinel" || result.ErrorStack == "" {
					t.Fatalf("unexpected orchestrion fatalf result: %+v\n%s", result, output)
				}
			},
		},
		{
			name:     "skip",
			testName: "TestOrchestrionRetryProcessSkipChild",
			assert: func(t *testing.T, result retryProcessChildResult, output []byte) {
				if result.Status != "skip" || result.Failed || !result.Skipped || result.SkipReason != "orchestrion skip sentinel" {
					t.Fatalf("unexpected orchestrion skip result: %+v\n%s", result, output)
				}
			},
		},
		{
			name:     "skipf",
			testName: "TestOrchestrionRetryProcessSkipfChild",
			assert: func(t *testing.T, result retryProcessChildResult, output []byte) {
				if result.Status != "skip" || result.Failed || !result.Skipped || result.SkipReason != "orchestrion skipf sentinel" {
					t.Fatalf("unexpected orchestrion skipf result: %+v\n%s", result, output)
				}
			},
		},
		{
			name:     "subtest then top-level skip",
			testName: "TestOrchestrionRetryProcessSubtestThenTopLevelSkipChild",
			assert: func(t *testing.T, result retryProcessChildResult, output []byte) {
				if result.Status != "skip" || result.Failed || !result.Skipped || result.SkipReason != "orchestrion top-level skip sentinel" {
					t.Fatalf("unexpected orchestrion top-level skip result: %+v\n%s", result, output)
				}
			},
		},
		{
			name:        "subtest error",
			testName:    "TestOrchestrionRetryProcessSubtestErrorChild",
			wantExitErr: true,
			assert: func(t *testing.T, result retryProcessChildResult, output []byte) {
				if result.Status != "fail" || !result.Failed || result.Panic || result.ErrorType == "" ||
					result.ErrorMessage != "orchestrion subtest error sentinel" || result.ErrorStack == "" {
					t.Fatalf("unexpected orchestrion subtest error result: %+v\n%s", result, output)
				}
			},
		},
		{
			name:        "subtest panic",
			testName:    "TestOrchestrionRetryProcessSubtestPanicChild",
			wantExitErr: true,
			assert: func(t *testing.T, result retryProcessChildResult, output []byte) {
				if result.Status != "controlled_panic_ready" || !result.Failed || !result.Panic || result.ErrorType != "panic" ||
					result.ErrorMessage != "orchestrion subtest panic sentinel" || result.ErrorStack == "" {
					t.Fatalf("unexpected orchestrion subtest panic result: %+v\n%s", result, output)
				}
			},
		},
		{
			name:        "parallel subtest panic",
			testName:    "TestOrchestrionRetryProcessParallelSubtestPanicChild",
			wantExitErr: true,
			assert: func(t *testing.T, result retryProcessChildResult, output []byte) {
				if result.Status != "controlled_panic_ready" || !result.Failed || !result.Panic || result.ErrorType != "panic" ||
					result.ErrorMessage != "orchestrion parallel subtest panic sentinel" || result.ErrorStack == "" {
					t.Fatalf("unexpected orchestrion parallel subtest panic result: %+v\n%s", result, output)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extraEnv := func(tempDir string) []string { return nil }
			if tt.testName == "TestOrchestrionRetryProcessSubtestPanicChild" {
				extraEnv = func(tempDir string) []string {
					return []string{orchestrionRetryProcessSubtestPanicContinuedPathEnv + "=" + filepath.Join(tempDir, "subtest-panic-continued")}
				}
			}
			result, output, err, tempDir := runOrchestrionRetryProcessChild(
				t, orchestrionRetryProcessMetadataRunFilter, tt.testName, true, extraEnv,
			)
			if tt.wantExitErr == (err == nil) {
				t.Fatalf("unexpected orchestrion child exit: %v\n%s", err, output)
			}
			tt.assert(t, result, output)
			if tt.testName == "TestOrchestrionRetryProcessSubtestPanicChild" {
				continuedPath := filepath.Join(tempDir, "subtest-panic-continued")
				if _, err := os.Stat(continuedPath); err == nil {
					t.Fatalf("subtest panic returned to the top-level test; continuation marker was written at %s", continuedPath)
				} else if !os.IsNotExist(err) {
					t.Fatalf("checking subtest panic continuation marker: %v", err)
				}
			}
		})
	}
}

func TestOrchestrionRetryProcessNoMatchingChildModeController(t *testing.T) {
	if orchestrionRetryProcessChild() {
		t.Skip("controller runs only in the parent process")
	}

	result, output, err, _ := runOrchestrionRetryProcessChild(
		t,
		orchestrionRetryProcessChildRunFilter,
		"TestOrchestrionRetryProcessMissingChild",
		true,
		nil,
	)
	if err != nil {
		t.Fatalf("orchestrion no-matching child process failed: %v\n%s", err, output)
	}

	if result.Version != 1 ||
		result.TestName != "TestOrchestrionRetryProcessMissingChild" ||
		result.Attempt != 1 ||
		result.RetryReason != constants.AutoTestRetriesRetryReason ||
		result.Status != "not_run" ||
		result.Failed ||
		result.Skipped {
		t.Fatalf("unexpected no-matching child result: %+v\n%s", result, output)
	}
}

func TestOrchestrionRetryProcessInvalidConfigController(t *testing.T) {
	if orchestrionRetryProcessChild() {
		t.Skip("controller runs only in the parent process")
	}

	result, output, err, _ := runOrchestrionRetryProcessChild(
		t,
		"^TestOrchestrionRetryProcessSelectedChild$",
		"TestOrchestrionRetryProcessSelectedChild",
		false,
		func(string) []string { return []string{orchestrionRetryProcessInvalidConfigEnv + "=true"} },
	)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("orchestrion invalid-config child process returned %v, want exit code 1\n%s", err, output)
	}

	if result.Version != 1 ||
		result.Status != "not_run" ||
		result.ResultError != "missing_attempt" ||
		result.Failed ||
		result.Skipped {
		t.Fatalf("unexpected invalid-config child result: %+v\n%s", result, output)
	}
}

func runOrchestrionRetryProcessChild(
	t *testing.T,
	runFilter string,
	testName string,
	completeConfig bool,
	extraEnv func(tempDir string) []string,
) (retryProcessChildResult, []byte, error, string) {
	t.Helper()
	tempDir := t.TempDir()
	resultPath := filepath.Join(tempDir, "result.json")
	server, requests := newOrchestrionRetryProcessChildActivityServer(t)
	cmd := exec.Command(os.Args[0], "-test.run="+runFilter, "-test.v")
	cmd.Env = append(os.Environ(),
		constants.CIVisibilityInternalRetryProcessChild+"=true",
		constants.CIVisibilityInternalRetryProcessResultPath+"="+resultPath,
		constants.CIVisibilityInternalRetryProcessTestName+"="+testName,
	)
	if completeConfig {
		cmd.Env = append(cmd.Env,
			constants.CIVisibilityInternalRetryProcessAttempt+"=1",
			constants.CIVisibilityInternalRetryProcessReason+"="+constants.AutoTestRetriesRetryReason,
		)
	}
	if extraEnv != nil {
		cmd.Env = append(cmd.Env, extraEnv(tempDir)...)
	}
	cmd.Env = append(cmd.Env, orchestrionRetryProcessChildActivityEnv(server.URL)...)
	output, err := runOrchestrionRetryProcessChildCommand(t, cmd, resultPath, testName, completeConfig)
	result := decodeOrchestrionRetryProcessResult(t, resultPath, output)
	if got := requests.Load(); got != 0 {
		t.Fatalf("expected zero Orchestrion child CI Visibility requests, got %d", got)
	}
	return result, output, err, tempDir
}

func runOrchestrionRetryProcessChildCommand(
	t *testing.T,
	cmd *exec.Cmd,
	resultPath string,
	testName string,
	withControl bool,
) ([]byte, error) {
	t.Helper()
	if !withControl {
		return cmd.CombinedOutput()
	}

	control, err := prepareOrchestrionRetryProcessControlTransport(cmd)
	if err != nil {
		t.Fatalf("preparing process retry control transport: %v", err)
	}
	defer control.close()
	control.config.Version = orchestrionRetryProcessControlVersion
	control.config.TestName = testName
	control.config.Attempt = 1
	control.config.RetryReason = constants.AutoTestRetriesRetryReason
	control.config.ObservedGOMAXPROCS = runtime.GOMAXPROCS(0)
	configPath := filepath.Clean(resultPath) + ".control.json"
	payload, err := json.Marshal(control.config)
	if err != nil {
		t.Fatalf("encoding process retry control config: %v", err)
	}
	if err := os.WriteFile(configPath, payload, 0o600); err != nil {
		t.Fatalf("writing process retry control config: %v", err)
	}

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		return output.Bytes(), err
	}
	control.closeChildEndpoints()
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- serveOrchestrionRetryProcessControl(control, testName)
	}()
	waitErr := cmd.Wait()
	return output.Bytes(), errors.Join(waitErr, <-serveErr)
}

func serveOrchestrionRetryProcessControl(control *orchestrionRetryProcessControlTransport, testName string) error {
	reader := bufio.NewReader(control.read)
	var recvSequence uint64
	var sendSequence uint64
	receive := func() (orchestrionRetryProcessControlFrame, error) {
		payload, err := reader.ReadBytes('\n')
		if err != nil {
			return orchestrionRetryProcessControlFrame{}, err
		}
		var frame orchestrionRetryProcessControlFrame
		if err := json.Unmarshal(bytes.TrimSuffix(payload, []byte{'\n'}), &frame); err != nil {
			return orchestrionRetryProcessControlFrame{}, err
		}
		recvSequence++
		if frame.Version != orchestrionRetryProcessControlVersion ||
			frame.TestName != testName ||
			frame.Attempt != 1 ||
			frame.RetryReason != constants.AutoTestRetriesRetryReason ||
			frame.Sequence != recvSequence {
			return orchestrionRetryProcessControlFrame{}, fmt.Errorf("invalid process retry control frame: %+v", frame)
		}
		return frame, nil
	}
	send := func(kind, reason string) error {
		sendSequence++
		return json.NewEncoder(control.write).Encode(orchestrionRetryProcessControlFrame{
			Version:     orchestrionRetryProcessControlVersion,
			TestName:    testName,
			Attempt:     1,
			RetryReason: constants.AutoTestRetriesRetryReason,
			Sequence:    sendSequence,
			Kind:        kind,
			Reason:      reason,
		})
	}

	frame, err := receive()
	if err != nil {
		return err
	}
	if frame.Kind != orchestrionRetryProcessControlAttemptReady {
		return fmt.Errorf("expected process retry attempt readiness, got %s", frame.Kind)
	}
	if err := send(orchestrionRetryProcessControlAdmission, ""); err != nil {
		return err
	}
	frame, err = receive()
	if err != nil {
		return err
	}
	if frame.Kind != orchestrionRetryProcessControlBodyAdmitted {
		return fmt.Errorf("expected process retry body admission, got %s", frame.Kind)
	}
	if err := send(orchestrionRetryProcessControlRunBody, ""); err != nil {
		return err
	}

	for {
		frame, err = receive()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch frame.Kind {
		case orchestrionRetryProcessControlParallel:
			if err := send(orchestrionRetryProcessControlParallelReady, ""); err != nil {
				return err
			}
		case orchestrionRetryProcessControlTerminalReady:
			if err := send(orchestrionRetryProcessControlTerminalCommit, frame.Reason); err != nil {
				return err
			}
			committed, err := receive()
			if err != nil {
				return err
			}
			if committed.Kind != orchestrionRetryProcessControlTerminalDone || committed.Reason != frame.Reason {
				return fmt.Errorf("invalid process retry terminal commit: %+v", committed)
			}
		case orchestrionRetryProcessControlAbort:
			return fmt.Errorf("process retry child aborted: %s", frame.Reason)
		default:
			return fmt.Errorf("unexpected process retry control frame: %+v", frame)
		}
	}
}

func newOrchestrionRetryProcessChildActivityServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	requests := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func orchestrionRetryProcessChildActivityEnv(serverURL string) []string {
	return []string{
		constants.CIVisibilityEnabledEnvironmentVariable + "=1",
		constants.CIVisibilityAgentlessEnabledEnvironmentVariable + "=1",
		constants.CIVisibilityAgentlessURLEnvironmentVariable + "=" + serverURL,
		constants.CIVisibilityGitUploadEnabledEnvironmentVariable + "=false",
		constants.APIKeyEnvironmentVariable + "=orchestrion-process-retry-parent-api-key",
	}
}

func orchestrionRetryProcessChild() bool {
	return integrations.IsProcessRetryChild()
}

func orchestrionRetryProcessEnv(name string) string {
	raw, _ := env.Lookup(name)
	return raw
}

func decodeOrchestrionRetryProcessResult(t *testing.T, resultPath string, output []byte) retryProcessChildResult {
	t.Helper()
	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("reading child result: %v\n%s", err, output)
	}
	var result retryProcessChildResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decoding child result: %v\n%s", err, output)
	}
	return result
}

func TestOrchestrionRetryProcessSelectedChild(t *testing.T) {
	if orchestrionRetryProcessEnv(orchestrionRetryProcessInvalidConfigEnv) == "true" {
		t.Fatal("selected test ran despite invalid process retry child config")
	}
	if !orchestrionRetryProcessChild() {
		t.Skip("selected child fixture runs only in process retry child mode")
	}
}

func TestOrchestrionRetryProcessUnselectedChild(t *testing.T) {
	if orchestrionRetryProcessChild() {
		t.Fatal("unselected test ran in process retry child mode")
	}
}

func TestOrchestrionRetryProcessErrorChild(t *testing.T) {
	if !orchestrionRetryProcessChild() {
		t.Skip("error child fixture runs only in process retry child mode")
	}
	stringer := requireSingleOrchestrionFormatting(t, "orchestrion error sentinel")
	t.Error(stringer)
}

func TestOrchestrionRetryProcessErrorfChild(t *testing.T) {
	if !orchestrionRetryProcessChild() {
		t.Skip("errorf child fixture runs only in process retry child mode")
	}
	stringer := requireSingleOrchestrionFormatting(t, "sentinel")
	t.Errorf("orchestrion errorf %s", stringer)
}

func TestOrchestrionRetryProcessFatalChild(t *testing.T) {
	if !orchestrionRetryProcessChild() {
		t.Skip("fatal child fixture runs only in process retry child mode")
	}
	t.Fatal(requireSingleOrchestrionFormatting(t, "orchestrion fatal sentinel"))
}

func TestOrchestrionRetryProcessFatalfChild(t *testing.T) {
	if !orchestrionRetryProcessChild() {
		t.Skip("fatalf child fixture runs only in process retry child mode")
	}
	t.Fatalf("orchestrion fatalf %s", requireSingleOrchestrionFormatting(t, "sentinel"))
}

func TestOrchestrionRetryProcessSkipChild(t *testing.T) {
	if !orchestrionRetryProcessChild() {
		t.Skip("skip child fixture runs only in process retry child mode")
	}
	t.Skip(requireSingleOrchestrionFormatting(t, "orchestrion skip sentinel"))
}

func TestOrchestrionRetryProcessSkipfChild(t *testing.T) {
	if !orchestrionRetryProcessChild() {
		t.Skip("skipf child fixture runs only in process retry child mode")
	}
	t.Skipf("orchestrion skipf %s", requireSingleOrchestrionFormatting(t, "sentinel"))
}

func TestOrchestrionRetryProcessSubtestThenTopLevelSkipChild(t *testing.T) {
	if !orchestrionRetryProcessChild() {
		t.Skip("subtest/top-level skip child fixture runs only in process retry child mode")
	}
	t.Run("subtest", func(t *testing.T) {
		t.Skip("orchestrion subtest skip sentinel")
	})
	t.Skip("orchestrion top-level skip sentinel")
}

func TestOrchestrionRetryProcessSubtestErrorChild(t *testing.T) {
	if !orchestrionRetryProcessChild() {
		t.Skip("subtest error child fixture runs only in process retry child mode")
	}
	t.Run("subtest", func(t *testing.T) {
		t.Error("orchestrion subtest error sentinel")
	})
}

func TestOrchestrionRetryProcessSubtestPanicChild(t *testing.T) {
	if !orchestrionRetryProcessChild() {
		t.Skip("subtest panic child fixture runs only in process retry child mode")
	}
	t.Run("subtest", func(*testing.T) {
		panic("orchestrion subtest panic sentinel")
	})
	markerPath := orchestrionRetryProcessEnv(orchestrionRetryProcessSubtestPanicContinuedPathEnv)
	if markerPath == "" {
		t.Fatal("missing subtest panic continuation marker path")
	}
	if err := os.WriteFile(markerPath, []byte("continued after subtest panic"), 0o600); err != nil {
		t.Fatalf("writing subtest panic continuation marker: %v", err)
	}
}

func TestOrchestrionRetryProcessParallelSubtestPanicChild(t *testing.T) {
	if !orchestrionRetryProcessChild() {
		t.Skip("parallel subtest panic child fixture runs only in process retry child mode")
	}
	t.Run("subtest", func(t *testing.T) {
		t.Parallel()
		panic("orchestrion parallel subtest panic sentinel")
	})
}

func TestOrchestrionRetryProcessHybridParentFixture(t *testing.T) {
	if orchestrionRetryProcessEnv(orchestrionRetryProcessHybridParentEnv) != "true" && !orchestrionRetryProcessChild() {
		t.Skip("hybrid parent fixture runs only from its controller subprocess")
	}
	if orchestrionRetryProcessChild() {
		if orchestrionRetryProcessHybridParentRuns.Load() != 0 {
			t.Fatalf("hybrid child inherited parent run count: %d", orchestrionRetryProcessHybridParentRuns.Load())
		}
		return
	}
	if orchestrionRetryProcessHybridParentRuns.Add(1) == 1 {
		t.Fatal("first hybrid parent execution must fail to trigger process retry")
	}
	t.Fatalf("hybrid retry ran in the parent process with run count %d", orchestrionRetryProcessHybridParentRuns.Load())
}

func TestOrchestrionRetryProcessPureParentFixture(t *testing.T) {
	if orchestrionRetryProcessEnv(orchestrionRetryProcessPureParentEnv) != "true" {
		t.Skip("pure parent fixture runs only from its controller subprocess")
	}
	if orchestrionRetryProcessChild() {
		if orchestrionRetryProcessPureParentRuns.Load() != 0 {
			t.Fatalf("pure Orchestrion child inherited parent run count: %d", orchestrionRetryProcessPureParentRuns.Load())
		}
		return
	}
	if orchestrionRetryProcessPureParentRuns.Add(1) == 1 {
		t.Fatal("first pure Orchestrion parent execution must fail to trigger process retry")
	}
	t.Fatal("pure Orchestrion retry ran in the parent process")
}

func TestOrchestrionRetryProcessSelectedSubtestAttemptToFixFixture(t *testing.T) {
	if orchestrionRetryProcessEnv(orchestrionRetryProcessSelectedSubtestA2FEnv) != "true" {
		t.Skip("selected-subtest attempt-to-fix fixture runs only from its controller subprocess")
	}
	if orchestrionRetryProcessSelectedSubtestA2FParentRuns.Add(1) != 1 {
		t.Fatal("selected-subtest attempt-to-fix retried the top-level test")
	}
	t.Run("selected", func(t *testing.T) {
		t.Parallel()
		if orchestrionRetryProcessChild() {
			t.Fatal("selected-subtest attempt-to-fix retry ran in a process child")
		}
		if attempt := orchestrionRetryProcessSelectedSubtestA2FRuns.Add(1); attempt > 1 {
			t.Fatalf("unexpected selected-subtest attempt %d", attempt)
		}
	})
}
