// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package retryprocess

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
const orchestrionRetryProcessCleanupPathEnv = "ORCHESTRION_RETRY_PROCESS_CLEANUP_PATH"
const orchestrionRetryProcessSubtestPanicContinuedPathEnv = "ORCHESTRION_RETRY_PROCESS_SUBTEST_PANIC_CONTINUED_PATH"
const orchestrionRetryProcessChildAPIKey = "orchestrion-process-retry-child-api-key"
const orchestrionRetryProcessChildRunFilter = "^TestOrchestrionRetryProcess(Selected|Unselected)Child$"
const orchestrionRetryProcessMetadataRunFilter = "^TestOrchestrionRetryProcess(Error|Skip|SubtestError|SubtestPanic|ParallelSubtestPanic|SubtestThenTopLevelSkip|Unselected)Child$"
const orchestrionRetryProcessHybridRunFilter = "^TestOrchestrionRetryProcessHybrid(Panic|Unselected)Child$"

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

func TestMain(m *testing.M) {
	if !built.WithOrchestrion {
		panic("Orchestrion is not enabled, please run this test with orchestrion")
	}
	if orchestrionRetryProcessChild() {
		_ = os.Setenv(constants.APIKeyEnvironmentVariable, orchestrionRetryProcessChildAPIKey)
	}
	if orchestrionRetryProcessEnv(orchestrionRetryProcessHybridParentEnv) == "true" {
		if orchestrionRetryProcessChild() {
			os.Exit(gotesting.RunM(m))
		}
		os.Exit(runOrchestrionRetryProcessHybridParent(m))
	}
	if orchestrionRetryProcessEnv(orchestrionRetryProcessPureParentEnv) == "true" {
		os.Exit(runOrchestrionRetryProcessPureParent(m))
	}
	if orchestrionRetryProcessEnv(orchestrionRetryProcessHybridEnv) == "true" {
		os.Exit(gotesting.RunM(m))
	}
	os.Exit(m.Run())
}

func TestOrchestrionRetryProcessPureParentFallsBackInProcessController(t *testing.T) {
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
	settingsRequests := atomic.Int32{}
	unknownRequests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/libraries/tests/services/setting":
			settingsRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			response := struct {
				Data struct {
					ID         string                               `json:"id"`
					Type       string                               `json:"type"`
					Attributes civisibilitynet.SettingsResponseData `json:"attributes"`
				} `json:"data"`
			}{}
			response.Data.ID = "orchestrion-pure-parent"
			response.Data.Type = "ci_app_libraries_settings"
			response.Data.Attributes.FlakyTestRetriesEnabled = true
			_ = json.NewEncoder(w).Encode(&response)
		case "/api/v2/git/repository/search_commits":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{}"))
		default:
			unknownRequests.Add(1)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_ = os.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "1")
	_ = os.Setenv(constants.CIVisibilityAgentlessURLEnvironmentVariable, server.URL)
	_ = os.Setenv(constants.CIVisibilityGitUploadEnabledEnvironmentVariable, "false")
	_ = os.Setenv(constants.APIKeyEnvironmentVariable, "orchestrion-pure-parent-api-key")
	_ = os.Setenv(constants.CIVisibilityFlakyRetryCountEnvironmentVariable, "1")
	_ = os.Setenv(constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")

	tracer := integrations.InitializeCIVisibilityMock()
	exitCode := m.Run()
	if exitCode == 0 {
		const resourceName = "fixtures_test.go.TestOrchestrionRetryProcessPureParentFixture"
		testSpans := 0
		processRetrySpans := 0
		unexpectedTerminationSpans := 0
		for _, span := range tracer.FinishedSpans() {
			if span.Tag(ext.ResourceName) != resourceName {
				continue
			}
			testSpans++
			if span.Tag(constants.TestRetryExecutionMode) == "process" {
				processRetrySpans++
			}
			errorMessage, _ := span.Tag(ext.ErrorMsg).(string)
			if span.Tag(ext.ErrorType) == "panic" && strings.Contains(errorMessage, "runtime.Goexit") {
				unexpectedTerminationSpans++
			}
		}
		if testSpans != 2 || processRetrySpans != 0 || unexpectedTerminationSpans != 1 {
			panic(fmt.Sprintf(
				"unexpected pure Orchestrion parent spans: tests=%d process_retries=%d unexpected_terminations=%d",
				testSpans,
				processRetrySpans,
				unexpectedTerminationSpans,
			))
		}
		if got := settingsRequests.Load(); got != 1 {
			panic(fmt.Sprintf("expected one pure parent settings request, got %d", got))
		}
		if got := unknownRequests.Load(); got != 0 {
			panic(fmt.Sprintf("unexpected pure parent request count: %d", got))
		}
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
	settingsRequests := atomic.Int32{}
	childRequests := atomic.Int32{}
	unknownRequests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("dd-api-key") == orchestrionRetryProcessChildAPIKey {
			childRequests.Add(1)
		}
		switch r.URL.Path {
		case "/api/v2/libraries/tests/services/setting":
			settingsRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			response := struct {
				Data struct {
					ID         string                               `json:"id"`
					Type       string                               `json:"type"`
					Attributes civisibilitynet.SettingsResponseData `json:"attributes"`
				} `json:"data"`
			}{}
			response.Data.ID = "orchestrion-hybrid-parent"
			response.Data.Type = "ci_app_libraries_settings"
			response.Data.Attributes.FlakyTestRetriesEnabled = true
			_ = json.NewEncoder(w).Encode(&response)
		case "/api/v2/git/repository/search_commits":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{}"))
		default:
			unknownRequests.Add(1)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_ = os.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "1")
	_ = os.Setenv(constants.CIVisibilityAgentlessURLEnvironmentVariable, server.URL)
	_ = os.Setenv(constants.CIVisibilityGitUploadEnabledEnvironmentVariable, "false")
	_ = os.Setenv(constants.APIKeyEnvironmentVariable, "orchestrion-hybrid-parent-api-key")
	_ = os.Setenv(constants.CIVisibilityFlakyRetryCountEnvironmentVariable, "1")
	_ = os.Setenv(constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")

	tracer := integrations.InitializeCIVisibilityMock()
	exitCode := gotesting.RunM(m)
	if exitCode == 0 {
		assertOrchestrionRetryProcessHybridParentSpans(tracer)
		if got := settingsRequests.Load(); got != 1 {
			panic(fmt.Sprintf("expected one parent settings request, got %d", got))
		}
		if got := childRequests.Load(); got != 0 {
			panic(fmt.Sprintf("expected zero child-owned CI Visibility requests, got %d", got))
		}
		if got := unknownRequests.Load(); got != 0 {
			panic(fmt.Sprintf("unexpected hybrid parent request count: %d", got))
		}
	}
	return exitCode
}

func assertOrchestrionRetryProcessHybridParentSpans(tracer mocktracer.Tracer) {
	const resourceName = "fixtures_test.go.TestOrchestrionRetryProcessHybridParentFixture"
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
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("orchestrion hybrid panic child unexpectedly passed\n%s", output)
	}

	result := decodeOrchestrionRetryProcessResult(t, resultPath, output)
	if result.Status != "fail" || !result.Failed || !result.Panic || result.ErrorType != "panic" ||
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

	tempDir := t.TempDir()
	resultPath := filepath.Join(tempDir, "result.json")
	server, requests := newOrchestrionRetryProcessChildActivityServer(t)
	cmd := exec.Command(os.Args[0], "-test.run="+orchestrionRetryProcessChildRunFilter, "-test.v")
	cmd.Env = append(os.Environ(),
		constants.CIVisibilityInternalRetryProcessChild+"=true",
		constants.CIVisibilityInternalRetryProcessResultPath+"="+resultPath,
		constants.CIVisibilityInternalRetryProcessTestName+"=TestOrchestrionRetryProcessSelectedChild",
		constants.CIVisibilityInternalRetryProcessAttempt+"=1",
		constants.CIVisibilityInternalRetryProcessReason+"="+constants.AutoTestRetriesRetryReason,
	)
	cmd.Env = append(cmd.Env, orchestrionRetryProcessChildActivityEnv(server.URL)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("orchestrion child process failed: %v\n%s", err, output)
	}

	result := decodeOrchestrionRetryProcessResult(t, resultPath, output)
	if result.Version != 1 ||
		result.TestName != "TestOrchestrionRetryProcessSelectedChild" ||
		result.Attempt != 1 ||
		result.RetryReason != constants.AutoTestRetriesRetryReason ||
		result.Status != "pass" ||
		result.Failed ||
		result.Skipped {
		t.Fatalf("unexpected child result: %+v\n%s", result, output)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("expected zero pure Orchestrion child CI Visibility requests, got %d", got)
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
			name:     "skip",
			testName: "TestOrchestrionRetryProcessSkipChild",
			assert: func(t *testing.T, result retryProcessChildResult, output []byte) {
				if result.Status != "skip" || result.Failed || !result.Skipped || result.SkipReason != "orchestrion skip sentinel" {
					t.Fatalf("unexpected orchestrion skip result: %+v\n%s", result, output)
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
				if result.Status != "fail" || !result.Failed || !result.Panic || result.ErrorType != "panic" ||
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
				if result.Status != "fail" || !result.Failed || !result.Panic || result.ErrorType != "panic" ||
					result.ErrorMessage != "orchestrion parallel subtest panic sentinel" || result.ErrorStack == "" {
					t.Fatalf("unexpected orchestrion parallel subtest panic result: %+v\n%s", result, output)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			resultPath := filepath.Join(tempDir, "result.json")
			server, requests := newOrchestrionRetryProcessChildActivityServer(t)
			cmd := exec.Command(os.Args[0], "-test.run="+orchestrionRetryProcessMetadataRunFilter, "-test.v")
			cmd.Env = append(os.Environ(),
				constants.CIVisibilityInternalRetryProcessChild+"=true",
				constants.CIVisibilityInternalRetryProcessResultPath+"="+resultPath,
				constants.CIVisibilityInternalRetryProcessTestName+"="+tt.testName,
				constants.CIVisibilityInternalRetryProcessAttempt+"=1",
				constants.CIVisibilityInternalRetryProcessReason+"="+constants.AutoTestRetriesRetryReason,
			)
			continuedPath := ""
			if tt.testName == "TestOrchestrionRetryProcessSubtestPanicChild" {
				continuedPath = filepath.Join(tempDir, "subtest-panic-continued")
				cmd.Env = append(cmd.Env, orchestrionRetryProcessSubtestPanicContinuedPathEnv+"="+continuedPath)
			}
			cmd.Env = append(cmd.Env, orchestrionRetryProcessChildActivityEnv(server.URL)...)
			output, err := cmd.CombinedOutput()
			if tt.wantExitErr == (err == nil) {
				t.Fatalf("unexpected orchestrion child exit: %v\n%s", err, output)
			}
			tt.assert(t, decodeOrchestrionRetryProcessResult(t, resultPath, output), output)
			if continuedPath != "" {
				if _, err := os.Stat(continuedPath); err == nil {
					t.Fatalf("subtest panic returned to the top-level test; continuation marker was written at %s", continuedPath)
				} else if !os.IsNotExist(err) {
					t.Fatalf("checking subtest panic continuation marker: %v", err)
				}
			}
			if got := requests.Load(); got != 0 {
				t.Fatalf("expected zero pure Orchestrion child CI Visibility requests, got %d", got)
			}
		})
	}
}

func TestOrchestrionRetryProcessNoMatchingChildModeController(t *testing.T) {
	if orchestrionRetryProcessChild() {
		t.Skip("controller runs only in the parent process")
	}

	tempDir := t.TempDir()
	resultPath := filepath.Join(tempDir, "result.json")
	server, requests := newOrchestrionRetryProcessChildActivityServer(t)
	cmd := exec.Command(os.Args[0], "-test.run="+orchestrionRetryProcessChildRunFilter, "-test.v")
	cmd.Env = append(os.Environ(),
		constants.CIVisibilityInternalRetryProcessChild+"=true",
		constants.CIVisibilityInternalRetryProcessResultPath+"="+resultPath,
		constants.CIVisibilityInternalRetryProcessTestName+"=TestOrchestrionRetryProcessMissingChild",
		constants.CIVisibilityInternalRetryProcessAttempt+"=1",
		constants.CIVisibilityInternalRetryProcessReason+"="+constants.AutoTestRetriesRetryReason,
	)
	cmd.Env = append(cmd.Env, orchestrionRetryProcessChildActivityEnv(server.URL)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("orchestrion no-matching child process failed: %v\n%s", err, output)
	}

	result := decodeOrchestrionRetryProcessResult(t, resultPath, output)
	if result.Version != 1 ||
		result.TestName != "TestOrchestrionRetryProcessMissingChild" ||
		result.Attempt != 1 ||
		result.RetryReason != constants.AutoTestRetriesRetryReason ||
		result.Status != "not_run" ||
		result.Failed ||
		result.Skipped {
		t.Fatalf("unexpected no-matching child result: %+v\n%s", result, output)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("expected zero pure Orchestrion child CI Visibility requests, got %d", got)
	}
}

func TestOrchestrionRetryProcessInvalidConfigController(t *testing.T) {
	if orchestrionRetryProcessChild() {
		t.Skip("controller runs only in the parent process")
	}

	tempDir := t.TempDir()
	resultPath := filepath.Join(tempDir, "result.json")
	server, requests := newOrchestrionRetryProcessChildActivityServer(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestOrchestrionRetryProcessSelectedChild$", "-test.v")
	cmd.Env = append(os.Environ(),
		constants.CIVisibilityInternalRetryProcessChild+"=true",
		constants.CIVisibilityInternalRetryProcessResultPath+"="+resultPath,
		constants.CIVisibilityInternalRetryProcessTestName+"=TestOrchestrionRetryProcessSelectedChild",
		orchestrionRetryProcessInvalidConfigEnv+"=true",
	)
	cmd.Env = append(cmd.Env, orchestrionRetryProcessChildActivityEnv(server.URL)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("orchestrion invalid-config child process failed: %v\n%s", err, output)
	}

	result := decodeOrchestrionRetryProcessResult(t, resultPath, output)
	if result.Version != 1 ||
		result.Status != "not_run" ||
		result.ResultError != "missing_attempt" ||
		result.Failed ||
		result.Skipped {
		t.Fatalf("unexpected invalid-config child result: %+v\n%s", result, output)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("expected zero invalid-config Orchestrion child CI Visibility requests, got %d", got)
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
