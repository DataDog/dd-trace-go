// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package retryprocess

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

var processRetryFixtureLogs = struct {
	mu      locking.Mutex
	entries []processRetryFixtureLogEntry
}{}

var processRetryFixtureRequests = struct {
	mu          locking.Mutex
	counts      map[string]int
	childCounts map[string]int
}{counts: map[string]int{}, childCounts: map[string]int{}}

var processRetryFixturePayloads = struct {
	mu     locking.Mutex
	values []string
}{}

func TestMain(m *testing.M) {
	if processRetryFixtureEnv(processRetryTransportProbeEnv) == "true" {
		if integrations.IsProcessRetryChild() {
			panic("process retry transport descendant entered child mode")
		}
		for _, key := range []string{
			constants.CIVisibilityInternalRetryProcessChild,
			constants.CIVisibilityInternalRetryProcessResultPath,
			constants.CIVisibilityInternalRetryProcessTestName,
			constants.CIVisibilityInternalRetryProcessAttempt,
			constants.CIVisibilityInternalRetryProcessReason,
		} {
			if _, inherited := os.LookupEnv(key); inherited {
				panic("process retry transport descendant inherited " + key)
			}
		}
		os.Exit(0)
	}
	if processRetryFixtureEnv(processRetryDescendantHelperEnv) == "true" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			panic(fmt.Sprintf("listen for process retry descendant liveness: %v", err))
		}
		tcpListener, ok := listener.(*net.TCPListener)
		if !ok {
			_ = listener.Close()
			panic(fmt.Sprintf("process retry descendant listener has type %T, want *net.TCPListener", listener))
		}
		if err := tcpListener.SetDeadline(time.Now().Add(processRetryDescendantHelperLifetime)); err != nil {
			_ = listener.Close()
			panic(fmt.Sprintf("set process retry descendant listener deadline: %v", err))
		}
		livenessPath := processRetryFixtureEnv(processRetryDescendantLivenessPathEnv)
		if err := os.WriteFile(livenessPath, []byte(listener.Addr().String()), 0o600); err != nil {
			_ = listener.Close()
			panic(fmt.Sprintf("publish process retry descendant liveness address: %v", err))
		}
		for {
			conn, err := listener.Accept()
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					if err := listener.Close(); err != nil {
						panic(fmt.Sprintf("close process retry descendant listener: %v", err))
					}
					os.Exit(0)
				}
				_ = listener.Close()
				panic(fmt.Sprintf("accept process retry descendant liveness connection: %v", err))
			}
			if err := conn.Close(); err != nil {
				_ = listener.Close()
				panic(fmt.Sprintf("close process retry descendant liveness connection: %v", err))
			}
		}
	}
	if processRetryFixtureChild() {
		_ = os.Setenv(constants.APIKeyEnvironmentVariable, processRetryFixtureChildAPIKeySentinel)
		if processRetryFixtureEnv(processRetryMalformedJSONFixtureEnv) == "true" {
			fmt.Println(processRetryMalformedJSONLogSentinel)
			resultPath, ok := integrations.LookupProcessRetryChildTransport(constants.CIVisibilityInternalRetryProcessResultPath)
			if !ok || strings.TrimSpace(resultPath) == "" {
				panic("process retry malformed JSON fixture is missing the private result path")
			}
			if err := os.WriteFile(resultPath, []byte("{malformed"), 0o600); err != nil {
				panic(fmt.Sprintf("process retry malformed JSON fixture failed to write result: %v", err))
			}
			os.Exit(0)
		}
		exitCode := gotesting.RunM(m)
		if processRetryFixtureEnv(processRetryProcessExitFixtureEnv) == "true" && exitCode == 0 {
			os.Exit(1)
		}
		os.Exit(exitCode)
	}
	if !processRetryFixtureScenarioEnabled() {
		os.Exit(m.Run())
	}

	server := newProcessRetryFixtureServer()
	defer server.Close()

	_ = os.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "1")
	_ = os.Setenv(constants.CIVisibilityAgentlessURLEnvironmentVariable, server.URL)
	_ = os.Setenv(constants.CIVisibilityGitUploadEnabledEnvironmentVariable, "false")
	_ = os.Setenv(constants.APIKeyEnvironmentVariable, processRetryFixtureAPIKeySentinel)
	_ = os.Setenv(processRetryFixtureCustomSecretEnv, processRetryFixtureCustomSecretSentinel)
	_ = os.Setenv(processRetryFixtureHomePathEnv, filepath.Join(processRetryFixtureHomeDir(), "process-retry-home-path-sentinel"))
	_ = os.Setenv(processRetryFixtureWorkspacePathEnv, filepath.Join(processRetryFixtureWorkingDir(), "process-retry-workspace-path-sentinel"))
	_ = os.Setenv(processRetryFixtureTempPathEnv, filepath.Join(os.TempDir(), "process-retry-temp-path-sentinel"))
	_ = os.Setenv("DD_CIVISIBILITY_LOGS_ENABLED", "true")
	_ = os.Setenv(constants.CIVisibilityFlakyRetryCountEnvironmentVariable, "1")
	retryExecutionMode := processRetryFixtureEnv(processRetryBenchmarkExecutionModeEnv)
	if retryExecutionMode == "" {
		retryExecutionMode = "process"
	}
	_ = os.Setenv(constants.CIVisibilityRetryExecutionModeEnvironmentVariable, retryExecutionMode)

	fmt.Printf("process retry fixture runtime: go=%s goos=%s goarch=%s sha=%s\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, processRetryFixtureCommitSHA())

	tracer := integrations.InitializeCIVisibilityMock()
	exitCode := gotesting.RunM(m)
	if processRetryFixtureFailureScenarioEnabled() {
		if exitCode == 0 {
			panic("expected process retry failure scenario to fail before TestMain converts it into controller success")
		}
		resourceName := processRetryFixtureFailureScenarioResource()
		assertProcessRetryFixtureFailureSpans(tracer, resourceName)
		assertProcessRetryFixtureLogsForResource(resourceName, processRetryFixtureFailureScenarioLogSentinel())
		assertProcessRetryFixtureRequests(true)
		os.Exit(0)
	}
	if exitCode == 0 && testing.CoverMode() == "" && processRetryFixtureMainAssertionsEnabled() {
		if forcedRunChildLaunchRuns.Load() > 0 {
			assertProcessRetryFixtureSpans(tracer)
			assertProcessRetryFixtureLogs()
			assertProcessRetryForcedRunSpans(tracer)
		}
	}
	if exitCode == 0 && processRetryFixtureEnv(processRetryParallelEFDEnv) == "true" {
		assertProcessRetryParallelEFDSpans(tracer)
	}
	if exitCode == 0 && processRetryFixtureEnv(processRetryAttemptToFixEnv) == "true" {
		assertProcessRetryAttemptToFixSpans(tracer)
	}
	requireLogs := processRetryFixtureEnv(processRetryBenchmarkExecutionModeEnv) == "" &&
		processRetryFixtureEnv(processRetryParallelEFDEnv) != "true"
	assertProcessRetryFixtureRequests(requireLogs)
	os.Exit(exitCode)
}

func processRetryFixtureScenarioEnabled() bool {
	return processRetryFixtureEnv(processRetryScenarioEnv) == "true"
}

func processRetryScenarioEnvironment(entries ...string) []string {
	result := append([]string(nil), os.Environ()...)
	result = append(result, processRetryScenarioEnv+"=true")
	return append(result, entries...)
}

func processRetryFixtureMainAssertionsEnabled() bool {
	return processRetryFixtureEnv(processRetrySelectorFixtureEnv) != "true" &&
		processRetryFixtureEnv(processRetryStartupFixtureEnv) != "true" &&
		!processRetryFixtureFailureScenarioEnabled()
}

func processRetryFixtureFailureScenarioEnabled() bool {
	return processRetryFixtureEnv(processRetryProcessExitFixtureEnv) == "true" ||
		processRetryFixtureEnv(processRetryMalformedJSONFixtureEnv) == "true" ||
		processRetryFixtureEnv(processRetryTimeoutFixtureEnv) == "true" ||
		processRetryFixtureEnv(processRetryOutputTimeoutFixtureEnv) == "true"
}

func processRetryFixtureFailureScenarioResource() string {
	switch {
	case processRetryFixtureEnv(processRetryProcessExitFixtureEnv) == "true":
		return "fixtures_test.go.TestProcessRetryProcessExitParent"
	case processRetryFixtureEnv(processRetryMalformedJSONFixtureEnv) == "true":
		return "fixtures_test.go.TestProcessRetryMalformedJSONParent"
	case processRetryFixtureEnv(processRetryTimeoutFixtureEnv) == "true":
		return "fixtures_test.go.TestProcessRetryTimeoutParent"
	case processRetryFixtureEnv(processRetryOutputTimeoutFixtureEnv) == "true":
		return "fixtures_test.go.TestProcessRetryOutputTimeoutParent"
	default:
		return ""
	}
}

func processRetryFixtureFailureKind() string {
	switch {
	case processRetryFixtureEnv(processRetryProcessExitFixtureEnv) == "true":
		return "process_exit"
	case processRetryFixtureEnv(processRetryMalformedJSONFixtureEnv) == "true":
		return "missing_or_not_run"
	case processRetryFixtureEnv(processRetryTimeoutFixtureEnv) == "true",
		processRetryFixtureEnv(processRetryOutputTimeoutFixtureEnv) == "true":
		return "timeout"
	default:
		return ""
	}
}

func processRetryFixtureFailureScenarioLogSentinel() string {
	switch {
	case processRetryFixtureEnv(processRetryProcessExitFixtureEnv) == "true":
		return processRetryProcessExitLogSentinel
	case processRetryFixtureEnv(processRetryMalformedJSONFixtureEnv) == "true":
		return processRetryMalformedJSONLogSentinel
	case processRetryFixtureEnv(processRetryTimeoutFixtureEnv) == "true":
		return processRetryTimeoutLogSentinel
	case processRetryFixtureEnv(processRetryOutputTimeoutFixtureEnv) == "true":
		return processRetryOutputTimeoutLogSentinel
	default:
		return ""
	}
}

func newProcessRetryFixtureServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordProcessRetryFixtureRequest(r)
		switch r.URL.Path {
		case "/api/v2/libraries/tests/services/setting":
			w.Header().Set("Content-Type", "application/json")
			response := struct {
				Data struct {
					ID         string                               `json:"id"`
					Type       string                               `json:"type"`
					Attributes civisibilitynet.SettingsResponseData `json:"attributes"`
				} `json:"data"`
			}{}
			response.Data.ID = "process-retry-fixture"
			response.Data.Type = "ci_app_libraries_settings"
			response.Data.Attributes.FlakyTestRetriesEnabled = true
			response.Data.Attributes.ItrEnabled = true
			response.Data.Attributes.TestsSkipping = true
			if processRetryFixtureEnv(processRetryParallelEFDEnv) == "true" {
				response.Data.Attributes.KnownTestsEnabled = true
				response.Data.Attributes.EarlyFlakeDetection.Enabled = true
				response.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.FiveS = 2
			}
			if processRetryFixtureEnv(processRetryAttemptToFixEnv) == "true" {
				response.Data.Attributes.TestManagement.Enabled = true
				response.Data.Attributes.TestManagement.AttemptToFixRetries = 3
			}
			_ = json.NewEncoder(w).Encode(&response)
		case "/api/v2/ci/libraries/tests":
			if processRetryFixtureEnv(processRetryParallelEFDEnv) != "true" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			response := struct {
				Data struct {
					ID         string                                 `json:"id"`
					Type       string                                 `json:"type"`
					Attributes civisibilitynet.KnownTestsResponseData `json:"attributes"`
				} `json:"data"`
			}{}
			response.Data.ID = "process-retry-fixture"
			response.Data.Type = "ci_app_libraries_tests"
			response.Data.Attributes.Tests = civisibilitynet.KnownTestsResponseDataModules{
				"known-module": civisibilitynet.KnownTestsResponseDataSuites{},
			}
			_ = json.NewEncoder(w).Encode(&response)
		case "/api/v2/test/libraries/test-management/tests":
			if processRetryFixtureEnv(processRetryAttemptToFixEnv) != "true" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			response := struct {
				Data struct {
					ID         string                                                 `json:"id"`
					Type       string                                                 `json:"type"`
					Attributes civisibilitynet.TestManagementTestsResponseDataModules `json:"attributes"`
				} `json:"data"`
			}{}
			response.Data.ID = "process-retry-fixture"
			response.Data.Type = "ci_app_libraries_tests"
			response.Data.Attributes.Modules = map[string]civisibilitynet.TestManagementTestsResponseDataSuites{
				"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/retryprocess": {
					Suites: map[string]civisibilitynet.TestManagementTestsResponseDataTests{
						"fixtures_test.go": {
							Tests: map[string]civisibilitynet.TestManagementTestsResponseDataTestProperties{
								"TestProcessRetryAttemptToFixParent": {
									Properties: civisibilitynet.TestManagementTestsResponseDataTestPropertiesAttributes{AttemptToFix: true},
								},
							},
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(&response)
		case "/api/v2/ci/tests/skippable":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"meta": map[string]any{"correlation_id": "process-retry-fixture"},
				"data": []map[string]any{{
					"id":   "process-retry-forced-run",
					"type": "test",
					"attributes": civisibilitynet.SkippableResponseDataAttributes{
						Suite: "fixtures_test.go",
						Name:  "TestProcessRetryITRForcedRun",
					},
				}},
			})
		case "/api/v2/git/repository/search_commits":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{}"))
		case "/api/v2/git/repository/packfile":
			w.WriteHeader(http.StatusAccepted)
		case "/api/v2/logs":
			reader, err := gzip.NewReader(r.Body)
			if err == nil {
				var entries []processRetryFixtureLogEntry
				if decodeErr := json.NewDecoder(reader).Decode(&entries); decodeErr == nil {
					processRetryFixtureLogs.mu.Lock()
					processRetryFixtureLogs.entries = append(processRetryFixtureLogs.entries, entries...)
					processRetryFixtureLogs.mu.Unlock()
					recordProcessRetryFixturePayloadsFromLogs(entries)
				} else {
					_, _ = io.Copy(io.Discard, reader)
				}
				_ = reader.Close()
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
}

func recordProcessRetryFixturePayloadsFromLogs(entries []processRetryFixtureLogEntry) {
	processRetryFixturePayloads.mu.Lock()
	defer processRetryFixturePayloads.mu.Unlock()
	for _, entry := range entries {
		processRetryFixturePayloads.values = append(processRetryFixturePayloads.values, entry.Message, entry.TestName)
	}
}

func recordProcessRetryFixtureRequest(r *http.Request) {
	processRetryFixtureRequests.mu.Lock()
	defer processRetryFixtureRequests.mu.Unlock()
	processRetryFixtureRequests.counts[r.URL.Path]++
	if r.Header.Get("dd-api-key") == processRetryFixtureChildAPIKeySentinel {
		processRetryFixtureRequests.childCounts[r.URL.Path]++
	}
}

func assertProcessRetryFixtureLogs() {
	processRetryFixtureLogs.mu.Lock()
	defer processRetryFixtureLogs.mu.Unlock()
	if len(processRetryFixtureLogs.entries) == 0 {
		panic("expected process retry fixture to send CI Visibility test logs")
	}
	for _, entry := range processRetryFixtureLogs.entries {
		assertProcessRetryFixtureNoForbiddenSentinel(entry.Message)
		assertProcessRetryFixtureNoForbiddenSentinel(entry.TestName)
		if entry.TestName == "TestProcessRetryITRForcedRun" &&
			strings.Contains(entry.Message, processRetryChildLogSentinel) {
			assertProcessRetryFixturePayloads()
			return
		}
	}
	panic(fmt.Sprintf("expected process retry child output sentinel %q in CI Visibility logs, got %d log entries", processRetryChildLogSentinel, len(processRetryFixtureLogs.entries)))
}

func assertProcessRetryFixtureLogsForResource(resourceName, sentinel string) {
	if resourceName == "" || sentinel == "" {
		panic("missing process retry failure log assertion input")
	}
	testName := strings.TrimPrefix(resourceName, "fixtures_test.go.")
	processRetryFixtureLogs.mu.Lock()
	defer processRetryFixtureLogs.mu.Unlock()
	if len(processRetryFixtureLogs.entries) == 0 {
		panic(fmt.Sprintf("expected process retry fixture to send CI Visibility test logs for %s", resourceName))
	}
	for _, entry := range processRetryFixtureLogs.entries {
		assertProcessRetryFixtureNoForbiddenSentinel(entry.Message)
		assertProcessRetryFixtureNoForbiddenSentinel(entry.TestName)
		if entry.TestName == testName && strings.Contains(entry.Message, sentinel) {
			assertProcessRetryFixturePayloads()
			return
		}
	}
	panic(fmt.Sprintf("expected process retry child output sentinel %q for %s in CI Visibility logs, got %d log entries", sentinel, resourceName, len(processRetryFixtureLogs.entries)))
}

func assertProcessRetryFixturePayloads() {
	processRetryFixturePayloads.mu.Lock()
	defer processRetryFixturePayloads.mu.Unlock()
	for _, payload := range processRetryFixturePayloads.values {
		assertProcessRetryFixtureNoForbiddenSentinel(payload)
	}
}

func assertProcessRetryFixtureNoForbiddenSentinel(value string) {
	for _, sentinel := range processRetryFixtureForbiddenSentinels() {
		if sentinel != "" && strings.Contains(value, sentinel) {
			panic(fmt.Sprintf("process retry fixture payload leaked forbidden sentinel %q", sentinel))
		}
	}
}

func assertProcessRetryFixtureRequests(requireLogs bool) {
	processRetryFixtureRequests.mu.Lock()
	defer processRetryFixtureRequests.mu.Unlock()
	for path, count := range processRetryFixtureRequests.childCounts {
		if count != 0 {
			panic(fmt.Sprintf("expected zero child-owned CI Visibility requests to %s, got %d", path, count))
		}
	}
	if got := processRetryFixtureRequests.counts["/api/v2/libraries/tests/services/setting"]; got != 1 {
		panic(fmt.Sprintf("expected only the parent process to request settings once, got %d requests", got))
	}
	if got := processRetryFixtureRequests.counts["/api/v2/ci/tests/skippable"]; got != 1 {
		panic(fmt.Sprintf("expected only the parent process to request skippable tests once, got %d requests", got))
	}
	wantKnownTestsRequests := 0
	if processRetryFixtureEnv(processRetryParallelEFDEnv) == "true" {
		wantKnownTestsRequests = 1
	}
	if got := processRetryFixtureRequests.counts["/api/v2/ci/libraries/tests"]; got != wantKnownTestsRequests {
		panic(fmt.Sprintf("expected %d parent known-tests requests, got %d", wantKnownTestsRequests, got))
	}
	wantTestManagementRequests := 0
	if processRetryFixtureEnv(processRetryAttemptToFixEnv) == "true" {
		wantTestManagementRequests = 1
	}
	if got := processRetryFixtureRequests.counts["/api/v2/test/libraries/test-management/tests"]; got != wantTestManagementRequests {
		panic(fmt.Sprintf("expected %d parent test-management requests, got %d", wantTestManagementRequests, got))
	}
	if got := processRetryFixtureRequests.counts["/api/v2/git/repository/packfile"]; got != 0 {
		panic(fmt.Sprintf("expected no git packfile uploads while git upload is disabled, got %d requests", got))
	}
	if got := processRetryFixtureRequests.counts["/api/v2/logs"]; requireLogs && got < 1 {
		panic(fmt.Sprintf("expected the parent process to upload test logs, got %d requests", got))
	}
	for path, count := range processRetryFixtureRequests.counts {
		switch path {
		case "/api/v2/libraries/tests/services/setting", "/api/v2/ci/tests/skippable":
			if count < 1 {
				panic(fmt.Sprintf("expected at least one request to %s", path))
			}
		case "/api/v2/ci/libraries/tests":
			if count != wantKnownTestsRequests {
				panic(fmt.Sprintf("expected %d requests to %s, got %d", wantKnownTestsRequests, path, count))
			}
		case "/api/v2/test/libraries/test-management/tests":
			if count != wantTestManagementRequests {
				panic(fmt.Sprintf("expected %d requests to %s, got %d", wantTestManagementRequests, path, count))
			}
		case "/api/v2/logs":
			if requireLogs && count < 1 {
				panic(fmt.Sprintf("expected at least one request to %s", path))
			}
		default:
			panic(fmt.Sprintf("unexpected CI Visibility request path %s (%d requests)", path, count))
		}
	}
}

func TestProcessRetryFixtureRequestAssertionRejectsUnknownPath(t *testing.T) {
	processRetryFixtureRequests.mu.Lock()
	oldCounts := processRetryFixtureRequests.counts
	oldChildCounts := processRetryFixtureRequests.childCounts
	processRetryFixtureRequests.counts = map[string]int{
		"/api/v2/libraries/tests/services/setting": 1,
		"/api/v2/ci/tests/skippable":               1,
		"/api/v2/logs":                             1,
		"/unexpected-child-request":                1,
	}
	processRetryFixtureRequests.childCounts = map[string]int{}
	processRetryFixtureRequests.mu.Unlock()
	t.Cleanup(func() {
		processRetryFixtureRequests.mu.Lock()
		processRetryFixtureRequests.counts = oldCounts
		processRetryFixtureRequests.childCounts = oldChildCounts
		processRetryFixtureRequests.mu.Unlock()
	})
	defer func() {
		if recover() == nil {
			t.Fatal("strict request assertion accepted an unexpected child request")
		}
	}()
	assertProcessRetryFixtureRequests(true)
}

func TestProcessRetryFixtureRequestAssertionRejectsChildOwnedRequest(t *testing.T) {
	processRetryFixtureRequests.mu.Lock()
	oldCounts := processRetryFixtureRequests.counts
	oldChildCounts := processRetryFixtureRequests.childCounts
	processRetryFixtureRequests.counts = map[string]int{
		"/api/v2/libraries/tests/services/setting": 1,
		"/api/v2/ci/tests/skippable":               1,
		"/api/v2/logs":                             1,
	}
	processRetryFixtureRequests.childCounts = map[string]int{"/api/v2/logs": 1}
	processRetryFixtureRequests.mu.Unlock()
	t.Cleanup(func() {
		processRetryFixtureRequests.mu.Lock()
		processRetryFixtureRequests.counts = oldCounts
		processRetryFixtureRequests.childCounts = oldChildCounts
		processRetryFixtureRequests.mu.Unlock()
	})
	defer func() {
		if recover() == nil {
			t.Fatal("strict request assertion accepted a child-owned request")
		}
	}()
	assertProcessRetryFixtureRequests(true)
}

func assertProcessRetryFixtureSpans(tracer mocktracer.Tracer) {
	spans := tracer.FinishedSpans()
	var fixtureSpans []*mocktracer.Span
	for _, span := range spans {
		if span.Tag(ext.ResourceName) == "fixtures_test.go.TestProcessRetryITRForcedRun" {
			fixtureSpans = append(fixtureSpans, span)
		}
	}
	if len(fixtureSpans) != 2 {
		panic(fmt.Sprintf("expected exactly 2 process retry fixture test spans, got %d", len(fixtureSpans)))
	}
	var processRetrySpans int
	for _, span := range fixtureSpans {
		if span.Tag(constants.TestRetryExecutionMode) == "process" {
			processRetrySpans++
			if span.Tag(constants.TestIsRetry) != "true" {
				panic(fmt.Sprintf("expected process retry span to have %s=true", constants.TestIsRetry))
			}
			if span.Tag(constants.TestRetryReason) != constants.AutoTestRetriesRetryReason {
				panic(fmt.Sprintf("expected process retry span to have retry reason %s", constants.AutoTestRetriesRetryReason))
			}
		}
	}
	if processRetrySpans != 1 {
		panic(fmt.Sprintf("expected exactly 1 process retry span, got %d", processRetrySpans))
	}
}

func assertProcessRetryForcedRunSpans(tracer mocktracer.Tracer) {
	const resourceName = "fixtures_test.go.TestProcessRetryITRForcedRun"
	var fixtureSpans []*mocktracer.Span
	for _, span := range tracer.FinishedSpans() {
		if span.Tag(ext.ResourceName) == resourceName {
			fixtureSpans = append(fixtureSpans, span)
		}
	}
	if len(fixtureSpans) != 2 {
		panic(fmt.Sprintf("expected exactly 2 forced-run process retry spans, got %d", len(fixtureSpans)))
	}
	processRetrySpans := 0
	for _, span := range fixtureSpans {
		if span.Tag(constants.TestForcedToRun) != "true" {
			panic(fmt.Sprintf("expected %s=true on forced-run span", constants.TestForcedToRun))
		}
		if span.Tag(constants.TestRetryExecutionMode) == "process" {
			processRetrySpans++
		}
	}
	if processRetrySpans != 1 {
		panic(fmt.Sprintf("expected exactly 1 forced-run process retry span, got %d", processRetrySpans))
	}
}

func assertProcessRetryParallelEFDSpans(tracer mocktracer.Tracer) {
	const resourceName = "fixtures_test.go.TestProcessRetryParallelEFDParent"
	var fixtureSpans []*mocktracer.Span
	for _, span := range tracer.FinishedSpans() {
		if span.Tag(ext.ResourceName) == resourceName {
			fixtureSpans = append(fixtureSpans, span)
		}
	}
	if len(fixtureSpans) != 3 {
		panic(fmt.Sprintf("expected one parent and two parallel EFD retry spans, got %d", len(fixtureSpans)))
	}
	processRetrySpans := 0
	for _, span := range fixtureSpans {
		if span.Tag(constants.TestRetryExecutionMode) != "process" {
			continue
		}
		processRetrySpans++
		if span.Tag(constants.TestIsRetry) != "true" {
			panic(fmt.Sprintf("expected parallel EFD process retry span to have %s=true", constants.TestIsRetry))
		}
		if span.Tag(constants.TestRetryReason) != constants.EarlyFlakeDetectionRetryReason {
			panic(fmt.Sprintf("expected parallel EFD process retry reason %s", constants.EarlyFlakeDetectionRetryReason))
		}
		if span.Tag(constants.TestFinalStatus) != nil {
			panic("parallel EFD retry span unexpectedly has test.final_status")
		}
	}
	if processRetrySpans != 2 {
		panic(fmt.Sprintf("expected exactly 2 parallel EFD process retry spans, got %d", processRetrySpans))
	}
}

func assertProcessRetryAttemptToFixSpans(tracer mocktracer.Tracer) {
	const resourceName = "fixtures_test.go.TestProcessRetryAttemptToFixParent"
	var fixtureSpans []*mocktracer.Span
	for _, span := range tracer.FinishedSpans() {
		if span.Tag(ext.ResourceName) == resourceName {
			fixtureSpans = append(fixtureSpans, span)
		}
	}
	if len(fixtureSpans) != 3 {
		panic(fmt.Sprintf("expected one parent and two attempt-to-fix retry spans, got %d", len(fixtureSpans)))
	}
	processRetrySpans := 0
	passedTags := 0
	for _, span := range fixtureSpans {
		if span.Tag(constants.TestIsAttempToFix) != "true" {
			panic(fmt.Sprintf("expected attempt-to-fix span to have %s=true", constants.TestIsAttempToFix))
		}
		if span.Tag(constants.TestAttemptToFixPassed) == "true" {
			passedTags++
		}
		if span.Tag(constants.TestRetryExecutionMode) != "process" {
			continue
		}
		processRetrySpans++
		if span.Tag(constants.TestIsRetry) != "true" {
			panic(fmt.Sprintf("expected attempt-to-fix process retry span to have %s=true", constants.TestIsRetry))
		}
		if span.Tag(constants.TestRetryReason) != constants.AttemptToFixRetryReason {
			panic(fmt.Sprintf("expected attempt-to-fix retry reason %s", constants.AttemptToFixRetryReason))
		}
	}
	if processRetrySpans != 2 {
		panic(fmt.Sprintf("expected exactly 2 attempt-to-fix process retry spans, got %d", processRetrySpans))
	}
	if passedTags != 1 {
		panic(fmt.Sprintf("expected exactly one %s=true tag, got %d", constants.TestAttemptToFixPassed, passedTags))
	}
}

func assertProcessRetryFixtureFailureSpans(tracer mocktracer.Tracer, resourceName string) {
	if resourceName == "" {
		panic("missing process retry failure resource name")
	}
	spans := tracer.FinishedSpans()
	var fixtureSpans []*mocktracer.Span
	for _, span := range spans {
		if span.Tag(ext.ResourceName) == resourceName {
			fixtureSpans = append(fixtureSpans, span)
		}
	}
	if len(fixtureSpans) != 2 {
		panic(fmt.Sprintf("expected exactly 2 spans for %s, got %d", resourceName, len(fixtureSpans)))
	}
	var processRetrySpans int
	failureKind := processRetryFixtureFailureKind()
	if failureKind == "" {
		panic("missing process retry failure kind")
	}
	for _, span := range fixtureSpans {
		if span.Tag(constants.TestRetryExecutionMode) == "process" {
			processRetrySpans++
			if span.Tag(constants.TestStatus) != constants.TestStatusFail {
				panic(fmt.Sprintf("expected failed process retry span for %s to have test.status=fail", resourceName))
			}
			if span.Tag(constants.TestIsRetry) != "true" {
				panic(fmt.Sprintf("expected failed process retry span for %s to have test.is_retry=true", resourceName))
			}
			if span.Tag(ext.ErrorType) != failureKind {
				panic(fmt.Sprintf("expected failed process retry span for %s to have error.type=%s", resourceName, failureKind))
			}
			if span.Tag(ext.ErrorMsg) != "process retry failed: "+failureKind {
				panic(fmt.Sprintf("expected failed process retry span for %s to have static error.message", resourceName))
			}
		}
	}
	if processRetrySpans != 1 {
		panic(fmt.Sprintf("expected exactly 1 process retry span for %s, got %d", resourceName, processRetrySpans))
	}
}

type processRetryFixtureLogEntry struct {
	Message  string `json:"message"`
	TestName string `json:"test.name"`
}

const (
	processRetryFixtureAPIKeySentinel       = "process-retry-api-key-secret-sentinel"
	processRetryFixtureChildAPIKeySentinel  = "process-retry-child-api-key-secret-sentinel"
	processRetryFixtureCustomSecretEnv      = "PROCESS_RETRY_CUSTOM_SECRET_SENTINEL"
	processRetryFixtureCustomSecretSentinel = "process-retry-custom-secret-sentinel"
	processRetryFixtureHomePathEnv          = "PROCESS_RETRY_HOME_PATH_SENTINEL"
	processRetryFixtureWorkspacePathEnv     = "PROCESS_RETRY_WORKSPACE_PATH_SENTINEL"
	processRetryFixtureTempPathEnv          = "PROCESS_RETRY_TEMP_PATH_SENTINEL"
)

func processRetryFixtureForbiddenSentinels() []string {
	return []string{
		processRetryFixtureAPIKeySentinel,
		processRetryFixtureChildAPIKeySentinel,
		processRetryFixtureCustomSecretSentinel,
		processRetryFixtureEnv(processRetryFixtureHomePathEnv),
		processRetryFixtureEnv(processRetryFixtureWorkspacePathEnv),
		processRetryFixtureEnv(processRetryFixtureTempPathEnv),
	}
}

func processRetryFixtureHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return os.TempDir()
	}
	return home
}

func processRetryFixtureWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		return os.TempDir()
	}
	return wd
}
