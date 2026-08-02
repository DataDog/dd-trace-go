// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package retryprocess

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

const (
	processRetryBurstBaselineRootEnv    = "PROCESS_RETRY_BURST_BASELINE_ROOT"
	processRetryBurstSampleResourcesEnv = "PROCESS_RETRY_BURST_SAMPLE_RESOURCES"
	processRetryBurstEventPath          = "/process-retry-burst/event"
	processRetryBurstMaxPackages        = 8
)

type processRetryBurstProfile struct {
	startupDelay time.Duration
	bodyDelay    time.Duration
	cpuWork      int
}

type processRetryBurstScenario struct {
	name               string
	packages           int
	packageConcurrency int
	processConcurrency int
	retries            int
	totalRetryBudget   int
	flakyRetries       bool
	efd                bool
	parallelEFD        bool
	attemptToFix       bool
	disabled           bool
	quarantined        bool
	itrForced          bool
	coverage           bool
	parentFails        bool
	childFails         bool
	expectFailure      bool
	profile            processRetryBurstProfile
}

func (s processRetryBurstScenario) expectedRetryReason() string {
	switch {
	case s.attemptToFix:
		return constants.AttemptToFixRetryReason
	case s.efd:
		return constants.EarlyFlakeDetectionRetryReason
	case s.flakyRetries:
		return constants.AutoTestRetriesRetryReason
	default:
		return ""
	}
}

func (s processRetryBurstScenario) expectedRetriesPerPackage() int {
	switch {
	case (s.disabled || s.quarantined) && !s.attemptToFix:
		return 0
	case s.attemptToFix:
		// Test Management's setting counts the initial execution, unlike the
		// EFD and FTR settings, which count only retries.
		return max(s.retries-1, 0)
	case s.efd:
		return s.retries
	case s.flakyRetries && s.parentFails && s.retries > 0:
		if s.childFails {
			return s.retries
		}
		return 1
	default:
		return 0
	}
}

func (s processRetryBurstScenario) expectedRetryTotal() int {
	perPackage := s.expectedRetriesPerPackage()
	if s.flakyRetries && !s.efd && !s.attemptToFix && s.totalRetryBudget > 0 {
		perPackage = min(perPackage, s.totalRetryBudget)
	}
	return s.packages * perPackage
}

func (s processRetryBurstScenario) testManagementEnabled() bool {
	return s.attemptToFix || s.disabled || s.quarantined
}

type processRetryBurstEvent struct {
	Package string `json:"package"`
	Kind    string `json:"kind"`
	PID     int    `json:"pid"`
	Reason  string `json:"reason,omitempty"`
}

type processRetryBurstRecordedEvent struct {
	processRetryBurstEvent
	received time.Time
}

type processRetryBurstCollector struct {
	mu       locking.Mutex
	scenario processRetryBurstScenario
	events   []processRetryBurstRecordedEvent
	requests map[string]int
}

type processRetryBurstMetrics struct {
	elapsed                  time.Duration
	firstPass                time.Duration
	drain                    time.Duration
	parentProcesses          int
	parentFinishes           int
	firstPassCompletions     int
	childProcesses           int
	retryStarts              int
	retryFinishes            int
	retriesBeforeFirstPass   int
	maximumChildren          int
	maximumChildrenByPackage map[string]int
	retryReasons             map[string]int
	peakRSSBytes             int64
	peakCPUPercent           float64
	output                   string
}

type processRetryBurstResourceMetrics struct {
	peakRSSBytes   int64
	peakCPUPercent float64
}

func newProcessRetryBurstCollector(scenario processRetryBurstScenario) *processRetryBurstCollector {
	return &processRetryBurstCollector{scenario: scenario, requests: make(map[string]int)}
}

func (c *processRetryBurstCollector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	if r.URL.Path == processRetryBurstEventPath {
		var event processRetryBurstEvent
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&event); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if event.Package == "" || event.Kind == "" || event.PID <= 0 {
			http.Error(w, "invalid burst event", http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		c.events = append(c.events, processRetryBurstRecordedEvent{
			processRetryBurstEvent: event,
			received:               time.Now(),
		})
		c.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}

	_, _ = io.Copy(io.Discard, r.Body)
	c.mu.Lock()
	c.requests[r.URL.Path]++
	c.mu.Unlock()
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
		response.Data.ID = "process-retry-burst"
		response.Data.Type = "ci_app_libraries_settings"
		response.Data.Attributes.FlakyTestRetriesEnabled = c.scenario.flakyRetries
		response.Data.Attributes.KnownTestsEnabled = c.scenario.efd
		response.Data.Attributes.EarlyFlakeDetection.Enabled = c.scenario.efd
		response.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.FiveS = c.scenario.retries
		response.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.TenS = c.scenario.retries
		response.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.ThirtyS = c.scenario.retries
		response.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.FiveM = c.scenario.retries
		response.Data.Attributes.TestManagement.Enabled = c.scenario.attemptToFix || c.scenario.disabled || c.scenario.quarantined
		response.Data.Attributes.TestManagement.AttemptToFixRetries = c.scenario.retries
		response.Data.Attributes.ItrEnabled = c.scenario.itrForced
		response.Data.Attributes.TestsSkipping = c.scenario.itrForced
		_ = json.NewEncoder(w).Encode(&response)
	case "/api/v2/ci/libraries/tests":
		if !c.scenario.efd {
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
		response.Data.ID = "process-retry-burst"
		response.Data.Type = "ci_app_libraries_tests"
		response.Data.Attributes.Tests = make(civisibilitynet.KnownTestsResponseDataModules, processRetryBurstMaxPackages)
		for i := 0; i < processRetryBurstMaxPackages; i++ {
			module := fmt.Sprintf("github.com/DataDog/dd-trace-go/v2/burstfixture/pkg%02d", i)
			response.Data.Attributes.Tests[module] = civisibilitynet.KnownTestsResponseDataSuites{
				"burst_test.go": {"TestZFirstPassComplete"},
			}
		}
		_ = json.NewEncoder(w).Encode(&response)
	case "/api/v2/test/libraries/test-management/tests":
		if !c.scenario.attemptToFix && !c.scenario.disabled && !c.scenario.quarantined {
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
		response.Data.ID = "process-retry-burst"
		response.Data.Type = "ci_app_libraries_tests"
		response.Data.Attributes.Modules = make(map[string]civisibilitynet.TestManagementTestsResponseDataSuites, processRetryBurstMaxPackages)
		for i := 0; i < processRetryBurstMaxPackages; i++ {
			module := fmt.Sprintf("github.com/DataDog/dd-trace-go/v2/burstfixture/pkg%02d", i)
			response.Data.Attributes.Modules[module] = civisibilitynet.TestManagementTestsResponseDataSuites{
				Suites: map[string]civisibilitynet.TestManagementTestsResponseDataTests{
					"burst_test.go": {
						Tests: map[string]civisibilitynet.TestManagementTestsResponseDataTestProperties{
							"TestAFlaky": {Properties: civisibilitynet.TestManagementTestsResponseDataTestPropertiesAttributes{
								AttemptToFix: c.scenario.attemptToFix,
								Disabled:     c.scenario.disabled,
								Quarantined:  c.scenario.quarantined,
							}},
						},
					},
				},
			}
		}
		_ = json.NewEncoder(w).Encode(&response)
	case "/api/v2/ci/tests/skippable":
		if !c.scenario.itrForced {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		data := make([]map[string]any, 0, processRetryBurstMaxPackages)
		for i := 0; i < processRetryBurstMaxPackages; i++ {
			data = append(data, map[string]any{
				"id":   fmt.Sprintf("process-retry-burst-%d", i),
				"type": "test",
				"attributes": civisibilitynet.SkippableResponseDataAttributes{
					Suite: "burst_test.go",
					Name:  "TestAFlaky",
				},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"meta": map[string]any{"correlation_id": "process-retry-burst"},
			"data": data,
		})
	case "/api/v2/git/repository/search_commits":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	case "/api/v2/git/repository/packfile":
		w.WriteHeader(http.StatusAccepted)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

func (c *processRetryBurstCollector) metrics(start time.Time, elapsed time.Duration, resources processRetryBurstResourceMetrics, output string) processRetryBurstMetrics {
	c.mu.Lock()
	events := append([]processRetryBurstRecordedEvent(nil), c.events...)
	c.mu.Unlock()

	firstPassByPackage := make(map[string]struct{})
	parentPIDs := make(map[int]struct{})
	childPIDs := make(map[int]struct{})
	activeByPackage := make(map[string]int)
	maximumByPackage := make(map[string]int)
	retryReasons := make(map[string]int)
	active, maximum, retryStarts, retryFinishes, retriesBeforeFirstPass, parentFinishes := 0, 0, 0, 0, 0, 0
	var lastFirstPass time.Time
	for _, event := range events {
		switch event.Kind {
		case "parent_start":
			parentPIDs[event.PID] = struct{}{}
		case "first_pass_complete":
			firstPassByPackage[event.Package] = struct{}{}
			if event.received.After(lastFirstPass) {
				lastFirstPass = event.received
			}
		case "child_start":
			childPIDs[event.PID] = struct{}{}
			retryStarts++
			retryReasons[event.Reason]++
			if _, ok := firstPassByPackage[event.Package]; !ok {
				retriesBeforeFirstPass++
			}
			active++
			activeByPackage[event.Package]++
			maximum = max(maximum, active)
			maximumByPackage[event.Package] = max(maximumByPackage[event.Package], activeByPackage[event.Package])
		case "child_finish":
			retryFinishes++
			active--
			activeByPackage[event.Package]--
		case "parent_finish":
			parentFinishes++
		}
	}
	firstPass := time.Duration(0)
	if !lastFirstPass.IsZero() {
		firstPass = lastFirstPass.Sub(start)
	}
	return processRetryBurstMetrics{
		elapsed:                  elapsed,
		firstPass:                firstPass,
		drain:                    max(elapsed-firstPass, 0),
		parentProcesses:          len(parentPIDs),
		parentFinishes:           parentFinishes,
		firstPassCompletions:     len(firstPassByPackage),
		childProcesses:           len(childPIDs),
		retryStarts:              retryStarts,
		retryFinishes:            retryFinishes,
		retriesBeforeFirstPass:   retriesBeforeFirstPass,
		maximumChildren:          maximum,
		maximumChildrenByPackage: maximumByPackage,
		retryReasons:             retryReasons,
		peakRSSBytes:             resources.peakRSSBytes,
		peakCPUPercent:           resources.peakCPUPercent,
		output:                   output,
	}
}

func (c *processRetryBurstCollector) requestCount(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests[path]
}

func (c *processRetryBurstCollector) requestCounts() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	counts := make(map[string]int, len(c.requests))
	for path, count := range c.requests {
		counts[path] = count
	}
	return counts
}

func processRetryBurstRepositoryRoot(tb testing.TB) string {
	tb.Helper()
	dir, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	for {
		payload, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.HasPrefix(string(payload), "module github.com/DataDog/dd-trace-go/v2\n") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			tb.Fatal("dd-trace-go repository root not found")
		}
		dir = parent
	}
}

func writeProcessRetryBurstModule(tb testing.TB, root string, packageCount int) string {
	tb.Helper()
	moduleDir := tb.TempDir()
	goMod := fmt.Sprintf(`module github.com/DataDog/dd-trace-go/v2/burstfixture

go 1.25.0

require github.com/DataDog/dd-trace-go/v2 v2.0.0

replace github.com/DataDog/dd-trace-go/v2 => %s
`, filepath.ToSlash(root))
	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte(goMod), 0o600); err != nil {
		tb.Fatal(err)
	}
	harnessDir := filepath.Join(moduleDir, "internal", "harness")
	if err := os.MkdirAll(harnessDir, 0o700); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(harnessDir, "harness.go"), []byte(processRetryBurstHarnessSource), 0o600); err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < packageCount; i++ {
		name := fmt.Sprintf("pkg%02d", i)
		packageDir := filepath.Join(moduleDir, name)
		if err := os.MkdirAll(packageDir, 0o700); err != nil {
			tb.Fatal(err)
		}
		source := fmt.Sprintf(processRetryBurstPackageSource, name, name, name)
		if err := os.WriteFile(filepath.Join(packageDir, "burst_test.go"), []byte(source), 0o600); err != nil {
			tb.Fatal(err)
		}
	}
	return moduleDir
}

func processRetryBurstPackageArgs(packageCount int) []string {
	args := make([]string, 0, packageCount)
	for i := 0; i < packageCount; i++ {
		args = append(args, fmt.Sprintf("./pkg%02d", i))
	}
	return args
}

func processRetryBurstEnvironment(serverURL string, scenario processRetryBurstScenario) []string {
	eventURL := ""
	if serverURL != "" {
		eventURL = serverURL + processRetryBurstEventPath
	}
	overrides := map[string]string{
		"GOWORK":                                                               "off",
		"GOSUMDB":                                                              "off",
		"GOMAXPROCS":                                                           strconv.Itoa(runtime.GOMAXPROCS(0)),
		"PROCESS_RETRY_BURST_EVENT_URL":                                        eventURL,
		"PROCESS_RETRY_BURST_STARTUP_DELAY":                                    scenario.profile.startupDelay.String(),
		"PROCESS_RETRY_BURST_BODY_DELAY":                                       scenario.profile.bodyDelay.String(),
		"PROCESS_RETRY_BURST_CPU_WORK":                                         strconv.Itoa(scenario.profile.cpuWork),
		"PROCESS_RETRY_BURST_PARENT_FAILS":                                     strconv.FormatBool(scenario.parentFails),
		"PROCESS_RETRY_BURST_CHILD_FAILS":                                      strconv.FormatBool(scenario.childFails),
		"DD_GIT_REPOSITORY_URL":                                                "https://github.com/DataDog/dd-trace-go.git",
		"DD_GIT_COMMIT_SHA":                                                    "1234567890abcdef1234567890abcdef12345678",
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED":                                 "false",
		constants.CIVisibilityEnabledEnvironmentVariable:                       "true",
		constants.CIVisibilityAgentlessEnabledEnvironmentVariable:              "true",
		constants.CIVisibilityAgentlessURLEnvironmentVariable:                  serverURL,
		constants.CIVisibilityGitUploadEnabledEnvironmentVariable:              "false",
		constants.APIKeyEnvironmentVariable:                                    "process-retry-burst-api-key",
		constants.CIVisibilityRetryExecutionModeEnvironmentVariable:            "process",
		constants.CIVisibilityEarlyFlakeDetectionEnabledEnvironmentVariable:    strconv.FormatBool(scenario.efd),
		constants.CIVisibilityEarlyFlakeDetectionMaxRetriesEnvironmentVariable: "-1",
		constants.CIVisibilityInternalParallelEarlyFlakeDetectionEnabled:       strconv.FormatBool(scenario.parallelEFD),
	}
	if scenario.flakyRetries {
		overrides[constants.CIVisibilityFlakyRetryCountEnvironmentVariable] = strconv.Itoa(scenario.retries)
		budget := scenario.totalRetryBudget
		if budget <= 0 {
			budget = max(scenario.packages*scenario.retries, 1)
		}
		overrides[constants.CIVisibilityTotalFlakyRetryCountEnvironmentVariable] = strconv.Itoa(budget)
	}
	if scenario.attemptToFix {
		overrides[constants.CIVisibilityTestManagementAttemptToFixRetriesEnvironmentVariable] = strconv.Itoa(scenario.retries)
	}
	if serverURL == "" {
		overrides[constants.CIVisibilityEnabledEnvironmentVariable] = "false"
	}
	if scenario.processConcurrency > 0 {
		overrides[constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable] = strconv.Itoa(scenario.processConcurrency)
	}
	privatePrefix := "DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_"
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || strings.HasPrefix(key, privatePrefix) {
			continue
		}
		if _, replaced := overrides[key]; replaced ||
			key == constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable ||
			key == constants.CIVisibilityFlakyRetryCountEnvironmentVariable ||
			key == constants.CIVisibilityTotalFlakyRetryCountEnvironmentVariable ||
			key == constants.CIVisibilityTestManagementAttemptToFixRetriesEnvironmentVariable ||
			key == "GOFLAGS" || key == "GOCOVERDIR" {
			continue
		}
		env = append(env, entry)
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+overrides[key])
	}
	return env
}

func warmProcessRetryBurstModule(tb testing.TB, moduleDir string, packageCount int) {
	tb.Helper()
	args := []string{"test", "-mod=mod", "-run=^$", "-count=1"}
	args = append(args, processRetryBurstPackageArgs(packageCount)...)
	cmd := exec.Command("go", args...)
	cmd.Dir = moduleDir
	cmd.Env = processRetryBurstEnvironment("", processRetryBurstScenario{})
	if output, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("warm process retry burst module: %v\n%s", err, output)
	}
}

func runProcessRetryBurstScenario(tb testing.TB, moduleDir string, scenario processRetryBurstScenario) processRetryBurstMetrics {
	tb.Helper()
	collector := newProcessRetryBurstCollector(scenario)
	server := httptest.NewServer(collector)
	defer server.Close()
	args := []string{
		"test", "-mod=mod", "-count=1",
		"-p=" + strconv.Itoa(scenario.packageConcurrency),
		"-timeout=5m",
	}
	if scenario.coverage {
		args = append(args, "-coverprofile="+filepath.Join(tb.TempDir(), "coverage.out"))
	}
	args = append(args, processRetryBurstPackageArgs(scenario.packages)...)
	cmd := exec.Command("go", args...)
	cmd.Dir = moduleDir
	cmd.Env = processRetryBurstEnvironment(server.URL, scenario)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	start := time.Now()
	if err := cmd.Start(); err != nil {
		tb.Fatal(err)
	}
	stopSampling := startProcessRetryBurstResourceSampler(cmd.Process.Pid)
	err := cmd.Wait()
	elapsed := time.Since(start)
	resources := stopSampling()
	metrics := collector.metrics(start, elapsed, resources, output.String())
	if err != nil && !scenario.expectFailure {
		tb.Fatalf("process retry burst scenario %s failed: %v\nmetrics: %+v\nrequests: %v\n%s", scenario.name, err, metrics, collector.requestCounts(), output.String())
	}
	if err == nil && scenario.expectFailure {
		tb.Fatalf("process retry burst scenario %s unexpectedly passed\nmetrics: %+v\nrequests: %v\n%s", scenario.name, metrics, collector.requestCounts(), output.String())
	}
	validateProcessRetryBurstMetrics(tb, collector, scenario, metrics)
	return metrics
}

func validateProcessRetryBurstMetrics(tb testing.TB, collector *processRetryBurstCollector, scenario processRetryBurstScenario, metrics processRetryBurstMetrics) {
	tb.Helper()
	wantRetries := scenario.expectedRetryTotal()
	if metrics.parentProcesses != scenario.packages {
		tb.Fatalf("parent processes = %d, want %d\n%s", metrics.parentProcesses, scenario.packages, metrics.output)
	}
	if metrics.parentFinishes != scenario.packages || metrics.firstPassCompletions != scenario.packages {
		tb.Fatalf("parent finishes/first-pass completions = %d/%d, want %d each\n%s", metrics.parentFinishes, metrics.firstPassCompletions, scenario.packages, metrics.output)
	}
	if metrics.childProcesses != wantRetries || metrics.retryStarts != wantRetries || metrics.retryFinishes != wantRetries {
		tb.Fatalf("child processes/start/finish = %d/%d/%d, want %d each\n%s", metrics.childProcesses, metrics.retryStarts, metrics.retryFinishes, wantRetries, metrics.output)
	}
	wantReason := scenario.expectedRetryReason()
	if wantRetries == 0 {
		if len(metrics.retryReasons) != 0 {
			tb.Fatalf("retry reasons = %v, want none", metrics.retryReasons)
		}
	} else if metrics.retryReasons[wantReason] != wantRetries || len(metrics.retryReasons) != 1 {
		tb.Fatalf("retry reasons = %v, want %q exactly %d times", metrics.retryReasons, wantReason, wantRetries)
	}
	settingsRequests := collector.requestCount("/api/v2/libraries/tests/services/setting")
	if settingsRequests < 1 || settingsRequests > scenario.packages {
		tb.Fatalf("settings requests = %d, want between 1 and %d: %v", settingsRequests, scenario.packages, collector.requestCounts())
	}
	validateFeatureRequests := func(path string, enabled bool) {
		tb.Helper()
		requests := collector.requestCount(path)
		if enabled && (requests < 1 || requests > scenario.packages) {
			tb.Fatalf("%s requests = %d, want between 1 and %d", path, requests, scenario.packages)
		}
		if !enabled && requests != 0 {
			tb.Fatalf("%s requests = %d, want 0", path, requests)
		}
	}
	validateFeatureRequests("/api/v2/ci/libraries/tests", scenario.efd)
	validateFeatureRequests("/api/v2/test/libraries/test-management/tests", scenario.testManagementEnabled())
	validateFeatureRequests("/api/v2/ci/tests/skippable", scenario.itrForced)
	limit := scenario.processConcurrency
	if limit == 0 {
		limit = min(max(runtime.GOMAXPROCS(0), 1), 4)
	}
	for pkg, maximum := range metrics.maximumChildrenByPackage {
		if maximum > limit {
			tb.Fatalf("maximum child concurrency for %s = %d, limit %d", pkg, maximum, limit)
		}
	}
	globalLimit := min(scenario.packages, scenario.packageConcurrency) * limit
	if metrics.maximumChildren > globalLimit {
		tb.Fatalf("maximum global child concurrency = %d, limit %d", metrics.maximumChildren, globalLimit)
	}
}

func startProcessRetryBurstResourceSampler(rootPID int) func() processRetryBurstResourceMetrics {
	if os.Getenv(processRetryBurstSampleResourcesEnv) != "true" {
		return func() processRetryBurstResourceMetrics { return processRetryBurstResourceMetrics{} }
	}
	done := make(chan struct{})
	result := make(chan processRetryBurstResourceMetrics, 1)
	go func() {
		metrics := processRetryBurstResourceMetrics{}
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			sample := sampleProcessRetryBurstResources(rootPID)
			metrics.peakRSSBytes = max(metrics.peakRSSBytes, sample.peakRSSBytes)
			metrics.peakCPUPercent = max(metrics.peakCPUPercent, sample.peakCPUPercent)
			select {
			case <-done:
				result <- metrics
				return
			case <-ticker.C:
			}
		}
	}()
	return func() processRetryBurstResourceMetrics {
		close(done)
		return <-result
	}
}

func sampleProcessRetryBurstResources(rootPID int) processRetryBurstResourceMetrics {
	output, err := exec.Command("ps", "-axo", "pid=,ppid=,rss=,%cpu=").Output()
	if err != nil {
		return processRetryBurstResourceMetrics{}
	}
	type process struct {
		pid, ppid int
		rss       int64
		cpu       float64
	}
	processes := make(map[int]process)
	children := make(map[int][]int)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 4 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		rss, rssErr := strconv.ParseInt(fields[2], 10, 64)
		cpu, cpuErr := strconv.ParseFloat(fields[3], 64)
		if pidErr != nil || ppidErr != nil || rssErr != nil || cpuErr != nil {
			continue
		}
		processes[pid] = process{pid: pid, ppid: ppid, rss: rss * 1024, cpu: cpu}
		children[ppid] = append(children[ppid], pid)
	}
	queue := []int{rootPID}
	seen := make(map[int]struct{})
	metrics := processRetryBurstResourceMetrics{}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}
		if proc, ok := processes[pid]; ok {
			metrics.peakRSSBytes += proc.rss
			metrics.peakCPUPercent += proc.cpu
		}
		queue = append(queue, children[pid]...)
	}
	return metrics
}

func TestDeferredProcessRetryMultiPackageBurst(t *testing.T) {
	if !gotesting.ProcessRetryContainmentSupported() {
		t.Skip("process retry burst fixture requires process-tree containment")
	}
	root := processRetryBurstRepositoryRoot(t)
	moduleDir := writeProcessRetryBurstModule(t, root, 2)
	warmProcessRetryBurstModule(t, moduleDir, 2)
	scenario := processRetryBurstScenario{
		name:               "regression",
		packages:           2,
		packageConcurrency: 2,
		processConcurrency: 2,
		retries:            2,
		efd:                true,
		parallelEFD:        true,
		parentFails:        true,
	}
	metrics := runProcessRetryBurstScenario(t, moduleDir, scenario)
	if metrics.retriesBeforeFirstPass != 0 {
		t.Fatalf("%d process retries started before their package completed its native first pass", metrics.retriesBeforeFirstPass)
	}
}

func TestProcessRetryMultiPackageBurstWithoutRetries(t *testing.T) {
	root := processRetryBurstRepositoryRoot(t)
	moduleDir := writeProcessRetryBurstModule(t, root, 2)
	warmProcessRetryBurstModule(t, moduleDir, 2)
	metrics := runProcessRetryBurstScenario(t, moduleDir, processRetryBurstScenario{
		name:               "without-retries",
		packages:           2,
		packageConcurrency: 2,
		efd:                true,
	})
	if metrics.childProcesses != 0 || metrics.retryStarts != 0 || metrics.retryFinishes != 0 {
		t.Fatalf("child processes/start/finish = %d/%d/%d, want 0 each", metrics.childProcesses, metrics.retryStarts, metrics.retryFinishes)
	}
}

func TestProcessRetryBurstFamilyScenarios(t *testing.T) {
	if !gotesting.ProcessRetryContainmentSupported() {
		t.Skip("process retry burst fixture requires process-tree containment")
	}
	root := processRetryBurstRepositoryRoot(t)
	moduleDir := writeProcessRetryBurstModule(t, root, 2)
	warmProcessRetryBurstModule(t, moduleDir, 2)
	cases := []processRetryBurstScenario{
		{name: "ftr-initial-pass", flakyRetries: true, retries: 3},
		{name: "ftr-fail-to-pass", flakyRetries: true, retries: 3, parentFails: true},
		{name: "ftr-persistent-failure", flakyRetries: true, retries: 2, parentFails: true, childFails: true, expectFailure: true},
		{name: "ftr-budget-limited", flakyRetries: true, retries: 3, totalRetryBudget: 1, parentFails: true, childFails: true, expectFailure: true},
		{name: "efd-sequential-pass", efd: true, retries: 2},
		{name: "efd-parallel-pass", efd: true, parallelEFD: true, retries: 2},
		{name: "efd-persistent-failure", efd: true, parallelEFD: true, retries: 2, parentFails: true, childFails: true, expectFailure: true},
		{name: "a2f-all-pass", attemptToFix: true, retries: 2},
		{name: "a2f-persistent-failure", attemptToFix: true, retries: 2, parentFails: true, childFails: true, expectFailure: true},
		{name: "a2f-precedes-efd-and-ftr", flakyRetries: true, efd: true, parallelEFD: true, attemptToFix: true, retries: 2},
		{name: "disabled", disabled: true, parentFails: true},
		{name: "quarantined", quarantined: true, parentFails: true},
		{name: "disabled-a2f", disabled: true, attemptToFix: true, retries: 2},
		{name: "quarantined-a2f", quarantined: true, attemptToFix: true, retries: 2},
		{name: "itr-forced-ftr", flakyRetries: true, itrForced: true, retries: 1, parentFails: true},
		{name: "coverage-efd", efd: true, parallelEFD: true, retries: 2, coverage: true, parentFails: true},
	}
	for _, scenario := range cases {
		scenario.packages = 2
		scenario.packageConcurrency = 2
		scenario.processConcurrency = 2
		t.Run(scenario.name, func(t *testing.T) {
			runProcessRetryBurstScenario(t, moduleDir, scenario)
		})
	}
}

func TestProcessRetryBurstMetricsUseEventOrderAndProcessIdentity(t *testing.T) {
	start := time.Unix(100, 0)
	collector := newProcessRetryBurstCollector(processRetryBurstScenario{retries: 2})
	collector.events = []processRetryBurstRecordedEvent{
		{processRetryBurstEvent: processRetryBurstEvent{Package: "pkg00", Kind: "parent_start", PID: 1}, received: start.Add(time.Millisecond)},
		{processRetryBurstEvent: processRetryBurstEvent{Package: "pkg00", Kind: "child_start", PID: 2}, received: start.Add(2 * time.Millisecond)},
		{processRetryBurstEvent: processRetryBurstEvent{Package: "pkg00", Kind: "child_start", PID: 3}, received: start.Add(3 * time.Millisecond)},
		{processRetryBurstEvent: processRetryBurstEvent{Package: "pkg00", Kind: "first_pass_complete", PID: 1}, received: start.Add(4 * time.Millisecond)},
		{processRetryBurstEvent: processRetryBurstEvent{Package: "pkg00", Kind: "child_finish", PID: 2}, received: start.Add(5 * time.Millisecond)},
		{processRetryBurstEvent: processRetryBurstEvent{Package: "pkg00", Kind: "child_finish", PID: 3}, received: start.Add(6 * time.Millisecond)},
		{processRetryBurstEvent: processRetryBurstEvent{Package: "pkg00", Kind: "parent_finish", PID: 1}, received: start.Add(7 * time.Millisecond)},
	}
	metrics := collector.metrics(start, 8*time.Millisecond, processRetryBurstResourceMetrics{}, "")
	if metrics.parentProcesses != 1 || metrics.parentFinishes != 1 || metrics.firstPassCompletions != 1 {
		t.Fatalf("parent metrics = processes:%d finishes:%d first-pass:%d, want 1 each", metrics.parentProcesses, metrics.parentFinishes, metrics.firstPassCompletions)
	}
	if metrics.childProcesses != 2 || metrics.retryStarts != 2 || metrics.retryFinishes != 2 {
		t.Fatalf("child metrics = processes:%d starts:%d finishes:%d, want 2 each", metrics.childProcesses, metrics.retryStarts, metrics.retryFinishes)
	}
	if metrics.retriesBeforeFirstPass != 2 || metrics.maximumChildren != 2 || metrics.maximumChildrenByPackage["pkg00"] != 2 {
		t.Fatalf("ordering metrics = early:%d global-max:%d package-max:%d, want 2 each", metrics.retriesBeforeFirstPass, metrics.maximumChildren, metrics.maximumChildrenByPackage["pkg00"])
	}
	if metrics.firstPass != 4*time.Millisecond || metrics.drain != 4*time.Millisecond {
		t.Fatalf("durations = first-pass:%s drain:%s, want 4ms each", metrics.firstPass, metrics.drain)
	}
}

func BenchmarkProcessRetryMultiPackageBurst(b *testing.B) {
	if !gotesting.ProcessRetryContainmentSupported() {
		b.Skip("process retry burst benchmark requires process-tree containment")
	}
	root := processRetryBurstRepositoryRoot(b)
	targetRoots := map[string]string{"experiment": root}
	if baseline := strings.TrimSpace(os.Getenv(processRetryBurstBaselineRootEnv)); baseline != "" {
		targetRoots["baseline"] = baseline
	}
	profiles := map[string]processRetryBurstProfile{
		"startup": {startupDelay: 250 * time.Millisecond},
		"body":    {bodyDelay: 250 * time.Millisecond},
		"cpu":     {cpuWork: 250_000_000},
	}
	cases := []processRetryBurstScenario{
		{name: "packages=8/retries=0", packages: 8, packageConcurrency: 8, efd: true},
		{name: "scale/packages=1", packages: 1, packageConcurrency: 1, retries: 10, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "scale/packages=2", packages: 2, packageConcurrency: 2, retries: 10, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "scale/packages=4", packages: 4, packageConcurrency: 4, retries: 10, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "scale/packages=8", packages: 8, packageConcurrency: 8, retries: 10, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "packages=8/go-test-p=1", packages: 8, packageConcurrency: 1, retries: 10, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "packages=8/go-test-p=2", packages: 8, packageConcurrency: 2, retries: 10, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "packages=8/go-test-p=4", packages: 8, packageConcurrency: 4, retries: 10, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "packages=8/process-concurrency=1", packages: 8, packageConcurrency: 8, processConcurrency: 1, retries: 10, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "packages=8/process-concurrency=2", packages: 8, packageConcurrency: 8, processConcurrency: 2, retries: 10, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "packages=8/retries=2", packages: 8, packageConcurrency: 8, retries: 2, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "packages=8/retries=5", packages: 8, packageConcurrency: 8, retries: 5, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "packages=8/profile=body", packages: 8, packageConcurrency: 8, retries: 10, efd: true, parallelEFD: true, parentFails: true, profile: profiles["body"]},
		{name: "packages=8/profile=cpu", packages: 8, packageConcurrency: 8, retries: 10, efd: true, parallelEFD: true, parentFails: true, profile: profiles["cpu"]},
		{name: "families/ftr/initial-pass", packages: 8, packageConcurrency: 8, retries: 5, flakyRetries: true},
		{name: "families/ftr/fail-to-pass", packages: 8, packageConcurrency: 8, retries: 5, flakyRetries: true, parentFails: true, profile: profiles["startup"]},
		{name: "families/ftr/persistent-failure", packages: 8, packageConcurrency: 8, retries: 5, flakyRetries: true, parentFails: true, childFails: true, expectFailure: true, profile: profiles["startup"]},
		{name: "families/ftr/budget-limited=2", packages: 8, packageConcurrency: 8, retries: 5, totalRetryBudget: 2, flakyRetries: true, parentFails: true, childFails: true, expectFailure: true, profile: profiles["startup"]},
		{name: "families/efd/sequential-pass", packages: 8, packageConcurrency: 8, retries: 10, efd: true, profile: profiles["startup"]},
		{name: "families/efd/parallel-pass", packages: 8, packageConcurrency: 8, retries: 10, efd: true, parallelEFD: true, profile: profiles["startup"]},
		{name: "families/efd/parallel-persistent-failure", packages: 8, packageConcurrency: 8, retries: 10, efd: true, parallelEFD: true, parentFails: true, childFails: true, expectFailure: true, profile: profiles["startup"]},
		{name: "families/a2f/all-pass", packages: 8, packageConcurrency: 8, retries: 3, attemptToFix: true, profile: profiles["startup"]},
		{name: "families/a2f/persistent-failure", packages: 8, packageConcurrency: 8, retries: 3, attemptToFix: true, parentFails: true, childFails: true, expectFailure: true, profile: profiles["startup"]},
		{name: "families/a2f+efd+ftr/precedence", packages: 8, packageConcurrency: 8, retries: 3, flakyRetries: true, efd: true, parallelEFD: true, attemptToFix: true, profile: profiles["startup"]},
		{name: "families/a2f+disabled/all-pass", packages: 8, packageConcurrency: 8, retries: 3, attemptToFix: true, disabled: true, profile: profiles["startup"]},
		{name: "families/a2f+quarantined/all-pass", packages: 8, packageConcurrency: 8, retries: 3, attemptToFix: true, quarantined: true, profile: profiles["startup"]},
		{name: "families/test-management/disabled", packages: 8, packageConcurrency: 8, disabled: true, parentFails: true},
		{name: "families/test-management/quarantined", packages: 8, packageConcurrency: 8, quarantined: true, parentFails: true},
		{name: "families/itr-forced/ftr-fail-to-pass", packages: 8, packageConcurrency: 8, retries: 1, flakyRetries: true, itrForced: true, parentFails: true, profile: profiles["startup"]},
		{name: "families/coverage/efd-parallel", packages: 8, packageConcurrency: 8, retries: 2, efd: true, parallelEFD: true, coverage: true, parentFails: true, profile: profiles["startup"]},
	}
	moduleDirs := make(map[string]string, len(targetRoots))
	for _, name := range []string{"experiment", "baseline"} {
		targetRoot, ok := targetRoots[name]
		if !ok {
			continue
		}
		moduleDirs[name] = writeProcessRetryBurstModule(b, targetRoot, processRetryBurstMaxPackages)
		warmProcessRetryBurstModule(b, moduleDirs[name], processRetryBurstMaxPackages)
	}
	for _, scenario := range cases {
		b.Run(scenario.name, func(b *testing.B) {
			samples := make(map[string][]processRetryBurstMetrics, len(moduleDirs))
			b.ResetTimer()
			for i := range b.N {
				order := []string{"experiment", "baseline"}
				if i%2 != 0 {
					order[0], order[1] = order[1], order[0]
				}
				for _, name := range order {
					moduleDir, ok := moduleDirs[name]
					if !ok {
						continue
					}
					samples[name] = append(samples[name], runProcessRetryBurstScenario(b, moduleDir, scenario))
				}
			}
			b.StopTimer()
			reportProcessRetryBurstSamples(b, "experiment", samples["experiment"])
			if baselineSamples := samples["baseline"]; len(baselineSamples) > 0 {
				reportProcessRetryBurstSamples(b, "baseline", baselineSamples)
				experimentRun := medianProcessRetryBurstDuration(samples["experiment"], func(metrics processRetryBurstMetrics) time.Duration { return metrics.elapsed })
				baselineRun := medianProcessRetryBurstDuration(baselineSamples, func(metrics processRetryBurstMetrics) time.Duration { return metrics.elapsed })
				experimentFirstPass := medianProcessRetryBurstDuration(samples["experiment"], func(metrics processRetryBurstMetrics) time.Duration { return metrics.firstPass })
				baselineFirstPass := medianProcessRetryBurstDuration(baselineSamples, func(metrics processRetryBurstMetrics) time.Duration { return metrics.firstPass })
				b.ReportMetric(processRetryBurstImprovement(experimentRun, baselineRun), "run-improvement-percent")
				b.ReportMetric(processRetryBurstImprovement(experimentFirstPass, baselineFirstPass), "first-pass-improvement-percent")
			}
		})
	}
}

func reportProcessRetryBurstSamples(b *testing.B, prefix string, samples []processRetryBurstMetrics) {
	b.Helper()
	if len(samples) == 0 {
		return
	}
	run := medianProcessRetryBurstDuration(samples, func(metrics processRetryBurstMetrics) time.Duration { return metrics.elapsed })
	firstPass := medianProcessRetryBurstDuration(samples, func(metrics processRetryBurstMetrics) time.Duration { return metrics.firstPass })
	drain := medianProcessRetryBurstDuration(samples, func(metrics processRetryBurstMetrics) time.Duration { return metrics.drain })
	var retriesBeforeFirstPass, maximumChildren int
	var peakRSSBytes int64
	var peakCPUPercent float64
	for _, metrics := range samples {
		retriesBeforeFirstPass += metrics.retriesBeforeFirstPass
		maximumChildren = max(maximumChildren, metrics.maximumChildren)
		peakRSSBytes = max(peakRSSBytes, metrics.peakRSSBytes)
		peakCPUPercent = max(peakCPUPercent, metrics.peakCPUPercent)
	}
	b.ReportMetric(float64(run)/float64(time.Millisecond), prefix+"-run-ms/op")
	b.ReportMetric(float64(firstPass)/float64(time.Millisecond), prefix+"-first-pass-ms/op")
	b.ReportMetric(float64(drain)/float64(time.Millisecond), prefix+"-drain-ms/op")
	b.ReportMetric(float64(retriesBeforeFirstPass)/float64(len(samples)), prefix+"-early-retries/op")
	b.ReportMetric(float64(maximumChildren), prefix+"-max-children")
	if peakRSSBytes > 0 {
		b.ReportMetric(float64(peakRSSBytes)/(1024*1024), prefix+"-peak-rss-MiB")
		b.ReportMetric(peakCPUPercent, prefix+"-peak-cpu-percent")
	}
}

func medianProcessRetryBurstDuration(samples []processRetryBurstMetrics, value func(processRetryBurstMetrics) time.Duration) time.Duration {
	values := make([]time.Duration, len(samples))
	for i, sample := range samples {
		values[i] = value(sample)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values[len(values)/2]
}

func processRetryBurstImprovement(experiment, baseline time.Duration) float64 {
	if baseline <= 0 {
		return 0
	}
	return 100 * float64(baseline-experiment) / float64(baseline)
}

const processRetryBurstHarnessSource = `package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
)

var cpuSink atomic.Uint64

type event struct {
	Package string ` + "`json:\"package\"`" + `
	Kind string ` + "`json:\"kind\"`" + `
	PID int ` + "`json:\"pid\"`" + `
	Reason string ` + "`json:\"reason,omitempty\"`" + `
}

func post(pkg, kind, reason string) {
	url := os.Getenv("PROCESS_RETRY_BURST_EVENT_URL")
	if url == "" {
		return
	}
	payload, err := json.Marshal(event{Package: pkg, Kind: kind, PID: os.Getpid(), Reason: reason})
	if err != nil {
		panic(err)
	}
	response, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		panic(fmt.Sprintf("event %s returned %s", kind, response.Status))
	}
}

func duration(name string) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return 0
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		panic(err)
	}
	return value
}

func ChildStarted(pkg string) {
	if !integrations.IsProcessRetryChild() {
		return
	}
	reason, _ := integrations.LookupProcessRetryChildTransport(constants.CIVisibilityInternalRetryProcessReason)
	post(pkg, "child_start", reason)
	time.Sleep(duration("PROCESS_RETRY_BURST_STARTUP_DELAY"))
}

func runWork() {
	time.Sleep(duration("PROCESS_RETRY_BURST_BODY_DELAY"))
	iterations, err := strconv.Atoi(os.Getenv("PROCESS_RETRY_BURST_CPU_WORK"))
	if err != nil {
		panic(err)
	}
	value := uint64(1469598103934665603)
	for i := 0; i < iterations; i++ {
		value ^= uint64(i + 1)
		value *= 1099511628211
	}
	cpuSink.Store(value)
}

func RunFlaky(t *testing.T) {
	runWork()
	child := integrations.IsProcessRetryChild()
	if child && os.Getenv("PROCESS_RETRY_BURST_CHILD_FAILS") == "true" {
		t.Fail()
	}
	if !child && os.Getenv("PROCESS_RETRY_BURST_PARENT_FAILS") == "true" {
		t.Fail()
	}
}

func FirstPassComplete(pkg string) {
	if integrations.IsProcessRetryChild() {
		panic("retry child executed the first-pass sentinel")
	}
	post(pkg, "first_pass_complete", "")
}

func RunM(m *testing.M, pkg string) {
	child := integrations.IsProcessRetryChild()
	if !child {
		post(pkg, "parent_start", "")
	}
	exitCode := gotesting.RunM(m)
	if child {
		post(pkg, "child_finish", "")
	} else {
		post(pkg, "parent_finish", "")
	}
	os.Exit(exitCode)
}
`

const processRetryBurstPackageSource = `package %s

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/burstfixture/internal/harness"
)

const packageName = %q

func init() { harness.ChildStarted(packageName) }

//dd:test.unskippable
func TestAFlaky(t *testing.T) { harness.RunFlaky(t) }

func TestZFirstPassComplete(t *testing.T) { harness.FirstPassComplete(packageName) }

func TestMain(m *testing.M) { harness.RunM(m, %q) }
`
