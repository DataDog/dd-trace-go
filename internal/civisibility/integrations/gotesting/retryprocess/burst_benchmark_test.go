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
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/mod/modfile"

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
	processRetryBurstInProcessMode      = "in_process"
	processRetryBurstProcessMode        = "process"
)

var processRetryBurstServiceSequence atomic.Uint64

type processRetryBurstProfile struct {
	startupDelay time.Duration
	bodyDelay    time.Duration
	cpuWork      int
}

type processRetryBurstScenario struct {
	name               string
	retryExecutionMode string
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
	faultyThreshold    *int
	expectedRetries    *int
	knownFlaky         bool
	profile            processRetryBurstProfile
}

func (s processRetryBurstScenario) executionMode() string {
	if s.retryExecutionMode == "" {
		return processRetryBurstProcessMode
	}
	return s.retryExecutionMode
}

func (s processRetryBurstScenario) expectedChildTotal() int {
	if s.executionMode() != processRetryBurstProcessMode {
		return 0
	}
	return s.expectedRetryTotal()
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
	if s.expectedRetries != nil {
		return *s.expectedRetries
	}
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
		var attributes civisibilitynet.SettingsResponseData
		attributes.FlakyTestRetriesEnabled = c.scenario.flakyRetries
		attributes.KnownTestsEnabled = c.scenario.efd
		attributes.EarlyFlakeDetection.Enabled = c.scenario.efd
		attributes.EarlyFlakeDetection.FaultySessionThreshold = c.scenario.faultyThreshold
		attributes.EarlyFlakeDetection.SlowTestRetries.FiveS = c.scenario.retries
		attributes.EarlyFlakeDetection.SlowTestRetries.TenS = c.scenario.retries
		attributes.EarlyFlakeDetection.SlowTestRetries.ThirtyS = c.scenario.retries
		attributes.EarlyFlakeDetection.SlowTestRetries.FiveM = c.scenario.retries
		attributes.TestManagement.Enabled = c.scenario.attemptToFix || c.scenario.disabled || c.scenario.quarantined
		attributes.TestManagement.AttemptToFixRetries = c.scenario.retries
		attributes.ItrEnabled = c.scenario.itrForced
		attributes.TestsSkipping = c.scenario.itrForced
		writeProcessRetryAPIResponse(w, "process-retry-burst", "ci_app_libraries_settings", attributes)
	case "/api/v2/ci/libraries/tests":
		if !c.scenario.efd {
			http.NotFound(w, r)
			return
		}
		attributes := civisibilitynet.KnownTestsResponseData{Tests: make(civisibilitynet.KnownTestsResponseDataModules, processRetryBurstMaxPackages)}
		for i := range processRetryBurstMaxPackages {
			module := fmt.Sprintf("github.com/DataDog/dd-trace-go/v2/burstfixture/pkg%02d", i)
			tests := []string{"TestZFirstPassComplete"}
			if c.scenario.knownFlaky {
				tests = append(tests, "TestAFlaky")
			}
			attributes.Tests[module] = civisibilitynet.KnownTestsResponseDataSuites{
				"burst_test.go": tests,
			}
		}
		writeProcessRetryAPIResponse(w, "process-retry-burst", "ci_app_libraries_tests", attributes)
	case "/api/v2/test/libraries/test-management/tests":
		if !c.scenario.attemptToFix && !c.scenario.disabled && !c.scenario.quarantined {
			http.NotFound(w, r)
			return
		}
		attributes := civisibilitynet.TestManagementTestsResponseDataModules{Modules: make(map[string]civisibilitynet.TestManagementTestsResponseDataSuites, processRetryBurstMaxPackages)}
		for i := range processRetryBurstMaxPackages {
			module := fmt.Sprintf("github.com/DataDog/dd-trace-go/v2/burstfixture/pkg%02d", i)
			attributes.Modules[module] = civisibilitynet.TestManagementTestsResponseDataSuites{
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
		writeProcessRetryAPIResponse(w, "process-retry-burst", "ci_app_libraries_tests", attributes)
	case "/api/v2/ci/tests/skippable":
		if !c.scenario.itrForced {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		data := make([]map[string]any, 0, processRetryBurstMaxPackages)
		for i := range processRetryBurstMaxPackages {
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
		case "in_process_retry_start":
			retryStarts++
			retryReasons[event.Reason]++
			if _, ok := firstPassByPackage[event.Package]; !ok {
				retriesBeforeFirstPass++
			}
		case "in_process_retry_finish":
			retryFinishes++
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
	maps.Copy(counts, c.requests)
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
		if err == nil && processRetryBurstIsRepositoryModule(payload) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			tb.Fatal("dd-trace-go repository root not found")
		}
		dir = parent
	}
}

func processRetryBurstIsRepositoryModule(payload []byte) bool {
	return modfile.ModulePath(payload) == "github.com/DataDog/dd-trace-go/v2"
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
	for i := range packageCount {
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
	for i := range packageCount {
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
		"PROCESS_RETRY_BURST_RETRY_REASON":                                     scenario.expectedRetryReason(),
		"DD_GIT_REPOSITORY_URL":                                                "https://github.com/DataDog/dd-trace-go.git",
		"DD_GIT_COMMIT_SHA":                                                    "1234567890abcdef1234567890abcdef12345678",
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED":                                 "false",
		constants.CIVisibilityEnabledEnvironmentVariable:                       "true",
		constants.CIVisibilityAgentlessEnabledEnvironmentVariable:              "true",
		constants.CIVisibilityAgentlessURLEnvironmentVariable:                  serverURL,
		constants.CIVisibilityGitUploadEnabledEnvironmentVariable:              "false",
		constants.APIKeyEnvironmentVariable:                                    "process-retry-burst-api-key",
		constants.CIVisibilityRetryExecutionModeEnvironmentVariable:            scenario.executionMode(),
		constants.CIVisibilityEarlyFlakeDetectionEnabledEnvironmentVariable:    strconv.FormatBool(scenario.efd),
		constants.CIVisibilityEarlyFlakeDetectionMaxRetriesEnvironmentVariable: "-1",
		constants.CIVisibilityInternalParallelEarlyFlakeDetectionEnabled:       strconv.FormatBool(scenario.parallelEFD),
	}
	overrides["DD_SERVICE"] = fmt.Sprintf("process-retry-burst-%d", processRetryBurstServiceSequence.Add(1))
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

func TestProcessRetryBurstEnvironmentIsolatesReadCache(t *testing.T) {
	const serverURL = "http://127.0.0.1:12345"
	serviceName := func(environment []string) string {
		for _, entry := range environment {
			if value, ok := strings.CutPrefix(entry, "DD_SERVICE="); ok {
				return value
			}
		}
		return ""
	}

	first := serviceName(processRetryBurstEnvironment(serverURL, processRetryBurstScenario{name: "first"}))
	second := serviceName(processRetryBurstEnvironment(serverURL, processRetryBurstScenario{name: "second"}))
	if first == "" || second == "" || first == second {
		t.Fatalf("isolated service names = %q/%q, want distinct non-empty values", first, second)
	}
}

func TestProcessRetryBurstEnvironmentSelectsExecutionMode(t *testing.T) {
	value := func(environment []string, key string) string {
		t.Helper()
		prefix := key + "="
		for _, entry := range environment {
			if value, ok := strings.CutPrefix(entry, prefix); ok {
				return value
			}
		}
		return ""
	}

	for _, test := range []struct {
		name     string
		scenario processRetryBurstScenario
		want     string
	}{
		{name: "default", want: processRetryBurstProcessMode},
		{name: "in-process", scenario: processRetryBurstScenario{retryExecutionMode: processRetryBurstInProcessMode}, want: processRetryBurstInProcessMode},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment := processRetryBurstEnvironment("", test.scenario)
			if got := value(environment, constants.CIVisibilityRetryExecutionModeEnvironmentVariable); got != test.want {
				t.Fatalf("retry execution mode = %q, want %q", got, test.want)
			}
		})
	}
}

func warmProcessRetryBurstModule(tb testing.TB, moduleDir string, packageCount int) {
	tb.Helper()
	args := make([]string, 0, 4+packageCount)
	args = append(args, "test", "-mod=mod", "-run=^$", "-count=1")
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
	wantChildren := scenario.expectedChildTotal()
	if metrics.parentProcesses != scenario.packages {
		tb.Fatalf("parent processes = %d, want %d\n%s", metrics.parentProcesses, scenario.packages, metrics.output)
	}
	if metrics.parentFinishes != scenario.packages || metrics.firstPassCompletions != scenario.packages {
		tb.Fatalf("parent finishes/first-pass completions = %d/%d, want %d each\n%s", metrics.parentFinishes, metrics.firstPassCompletions, scenario.packages, metrics.output)
	}
	if metrics.childProcesses != wantChildren || metrics.retryStarts != wantRetries || metrics.retryFinishes != wantRetries {
		tb.Fatalf("child processes/start/finish = %d/%d/%d, want %d/%d/%d\n%s", metrics.childProcesses, metrics.retryStarts, metrics.retryFinishes, wantChildren, wantRetries, wantRetries, metrics.output)
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
	if scenario.executionMode() != processRetryBurstProcessMode {
		if metrics.maximumChildren != 0 || len(metrics.maximumChildrenByPackage) != 0 {
			tb.Fatalf("in-process retry child concurrency = %d/%v, want none", metrics.maximumChildren, metrics.maximumChildrenByPackage)
		}
		return
	}
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
	for line := range strings.SplitSeq(string(output), "\n") {
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

func TestProcessRetryBurstRepositoryModuleRecognition(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    bool
	}{
		{name: "LF", payload: "module github.com/DataDog/dd-trace-go/v2\n\ngo 1.25.0\n", want: true},
		{name: "CRLF", payload: "module github.com/DataDog/dd-trace-go/v2\r\n\r\ngo 1.25.0\r\n", want: true},
		{name: "different module", payload: "module example.com/other\n", want: false},
		{name: "invalid", payload: "not a go.mod", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := processRetryBurstIsRepositoryModule([]byte(tt.payload)); got != tt.want {
				t.Fatalf("processRetryBurstIsRepositoryModule() = %t, want %t", got, tt.want)
			}
		})
	}
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

func TestProcessRetryEFDFaultySessionSharesBudgetAcrossPackages(t *testing.T) {
	if !gotesting.ProcessRetryContainmentSupported() {
		t.Skip("process retry burst fixture requires process-tree containment")
	}
	root := processRetryBurstRepositoryRoot(t)
	moduleDir := writeProcessRetryBurstModule(t, root, 3)
	warmProcessRetryBurstModule(t, moduleDir, 3)
	threshold := 1
	expectedRetries := 1
	for _, mode := range []string{processRetryBurstProcessMode, processRetryBurstInProcessMode} {
		t.Run(mode, func(t *testing.T) {
			metrics := runProcessRetryBurstScenario(t, moduleDir, processRetryBurstScenario{
				name:               "faulty-session-shared-budget-" + mode,
				retryExecutionMode: mode,
				packages:           3,
				packageConcurrency: 3,
				processConcurrency: 2,
				retries:            1,
				efd:                true,
				faultyThreshold:    &threshold,
				expectedRetries:    &expectedRetries,
			})
			if metrics.retryStarts != expectedRetries {
				t.Fatalf("retry starts = %d, want one globally admitted EFD retry", metrics.retryStarts)
			}
		})
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
	inProcessCases := map[string]bool{
		"ftr-persistent-failure":   true,
		"efd-parallel-pass":        true,
		"a2f-precedes-efd-and-ftr": true,
		"coverage-efd":             true,
	}
	for _, scenario := range cases {
		scenario.packages = 2
		scenario.packageConcurrency = 2
		scenario.processConcurrency = 2
		t.Run(processRetryBurstProcessMode+"/"+scenario.name, func(t *testing.T) {
			runProcessRetryBurstScenario(t, moduleDir, scenario)
		})
		if inProcessCases[scenario.name] {
			scenario.retryExecutionMode = processRetryBurstInProcessMode
			t.Run(processRetryBurstInProcessMode+"/"+scenario.name, func(t *testing.T) {
				runProcessRetryBurstScenario(t, moduleDir, scenario)
			})
		}
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

func TestProcessRetryBurstMetricsCountInProcessRetriesWithoutChildren(t *testing.T) {
	start := time.Unix(100, 0)
	collector := newProcessRetryBurstCollector(processRetryBurstScenario{retryExecutionMode: processRetryBurstInProcessMode, retries: 1})
	collector.events = []processRetryBurstRecordedEvent{
		{processRetryBurstEvent: processRetryBurstEvent{Package: "pkg00", Kind: "parent_start", PID: 1}, received: start.Add(time.Millisecond)},
		{processRetryBurstEvent: processRetryBurstEvent{Package: "pkg00", Kind: "in_process_retry_start", PID: 1, Reason: constants.EarlyFlakeDetectionRetryReason}, received: start.Add(2 * time.Millisecond)},
		{processRetryBurstEvent: processRetryBurstEvent{Package: "pkg00", Kind: "in_process_retry_finish", PID: 1}, received: start.Add(3 * time.Millisecond)},
		{processRetryBurstEvent: processRetryBurstEvent{Package: "pkg00", Kind: "first_pass_complete", PID: 1}, received: start.Add(4 * time.Millisecond)},
		{processRetryBurstEvent: processRetryBurstEvent{Package: "pkg00", Kind: "parent_finish", PID: 1}, received: start.Add(5 * time.Millisecond)},
	}
	metrics := collector.metrics(start, 6*time.Millisecond, processRetryBurstResourceMetrics{}, "")
	if metrics.childProcesses != 0 || metrics.maximumChildren != 0 {
		t.Fatalf("child processes/maximum = %d/%d, want 0/0", metrics.childProcesses, metrics.maximumChildren)
	}
	if metrics.retryStarts != 1 || metrics.retryFinishes != 1 || metrics.retriesBeforeFirstPass != 1 {
		t.Fatalf("retry starts/finishes/early = %d/%d/%d, want 1/1/1", metrics.retryStarts, metrics.retryFinishes, metrics.retriesBeforeFirstPass)
	}
	if got := metrics.retryReasons[constants.EarlyFlakeDetectionRetryReason]; got != 1 {
		t.Fatalf("EFD retry reasons = %d, want 1", got)
	}
}

func TestMedianProcessRetryBurstImprovementUsesPairedSamples(t *testing.T) {
	experiment := []processRetryBurstMetrics{{elapsed: 9 * time.Millisecond}, {elapsed: 200 * time.Millisecond}, {elapsed: 30 * time.Millisecond}}
	baseline := []processRetryBurstMetrics{{elapsed: 10 * time.Millisecond}, {elapsed: 100 * time.Millisecond}, {elapsed: 40 * time.Millisecond}}
	got := medianProcessRetryBurstImprovement(experiment, baseline, func(metrics processRetryBurstMetrics) time.Duration { return metrics.elapsed })
	if got != 10 {
		t.Fatalf("paired median improvement = %v, want 10", got)
	}
	if got := medianProcessRetryBurstImprovement(experiment[:2], baseline, func(metrics processRetryBurstMetrics) time.Duration { return metrics.elapsed }); got != 0 {
		t.Fatalf("mismatched sample improvement = %v, want 0", got)
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
	profiles := processRetryBurstProfiles()
	faultyThreshold := 1
	zeroRetries := 0
	oneIdentityRetries := 10
	familyCases := retryFamilyBenchmarkScenarios(profiles, "/all-pass")
	cases := make([]processRetryBurstScenario, 0, 14+len(familyCases))
	cases = append(cases, []processRetryBurstScenario{
		{name: "packages=8/retries=0", packages: 8, packageConcurrency: 8, efd: true},
		{name: "faulty-session/all-known", packages: 8, packageConcurrency: 8, retries: 10, efd: true, knownFlaky: true, faultyThreshold: &faultyThreshold, expectedRetries: &zeroRetries},
		{name: "faulty-session/below-limit", packages: 1, packageConcurrency: 1, retries: 10, efd: true, faultyThreshold: &faultyThreshold},
		{name: "faulty-session/crossing", packages: 8, packageConcurrency: 8, retries: 10, efd: true, parallelEFD: true, faultyThreshold: &faultyThreshold, expectedRetries: &oneIdentityRetries},
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
	}...)
	cases = append(cases, familyCases...)
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
				b.ReportMetric(medianProcessRetryBurstImprovement(samples["experiment"], baselineSamples, func(metrics processRetryBurstMetrics) time.Duration { return metrics.elapsed }), "run-improvement-percent")
				b.ReportMetric(medianProcessRetryBurstImprovement(samples["experiment"], baselineSamples, func(metrics processRetryBurstMetrics) time.Duration { return metrics.firstPass }), "first-pass-improvement-percent")
			}
		})
	}
}

func BenchmarkRetryExecutionModeMatrix(b *testing.B) {
	if !gotesting.ProcessRetryContainmentSupported() {
		b.Skip("retry execution mode benchmark requires process-tree containment")
	}
	root := processRetryBurstRepositoryRoot(b)
	moduleDir := writeProcessRetryBurstModule(b, root, processRetryBurstMaxPackages)
	warmProcessRetryBurstModule(b, moduleDir, processRetryBurstMaxPackages)
	for _, scenario := range retryExecutionModeBenchmarkScenarios(processRetryBurstProfiles()) {
		b.Run(scenario.name, func(b *testing.B) {
			samples := map[string][]processRetryBurstMetrics{
				processRetryBurstInProcessMode: nil,
				processRetryBurstProcessMode:   nil,
			}
			b.ResetTimer()
			for i := range b.N {
				order := []string{processRetryBurstInProcessMode, processRetryBurstProcessMode}
				if i%2 != 0 {
					order[0], order[1] = order[1], order[0]
				}
				for _, mode := range order {
					modeScenario := scenario
					modeScenario.retryExecutionMode = mode
					samples[mode] = append(samples[mode], runProcessRetryBurstScenario(b, moduleDir, modeScenario))
				}
			}
			b.StopTimer()
			inProcessSamples := samples[processRetryBurstInProcessMode]
			processSamples := samples[processRetryBurstProcessMode]
			reportProcessRetryBurstSamples(b, "in-process", inProcessSamples)
			reportProcessRetryBurstSamples(b, "process", processSamples)
			b.ReportMetric(medianProcessRetryBurstImprovement(processSamples, inProcessSamples, func(metrics processRetryBurstMetrics) time.Duration { return metrics.elapsed }), "process-total-improvement-percent")
			b.ReportMetric(medianProcessRetryBurstImprovement(processSamples, inProcessSamples, func(metrics processRetryBurstMetrics) time.Duration { return metrics.firstPass }), "process-first-pass-improvement-percent")
		})
	}
}

func processRetryBurstProfiles() map[string]processRetryBurstProfile {
	return map[string]processRetryBurstProfile{
		"startup": {startupDelay: 250 * time.Millisecond},
		"body":    {bodyDelay: 250 * time.Millisecond},
		"cpu":     {cpuWork: 250_000_000},
	}
}

func retryExecutionModeBenchmarkScenarios(profiles map[string]processRetryBurstProfile) []processRetryBurstScenario {
	head := []processRetryBurstScenario{
		{name: "no-retries/ci-visibility", packages: 8, packageConcurrency: 8},
		{name: "no-retries/efd-enabled", packages: 8, packageConcurrency: 8, efd: true},
	}
	tail := []processRetryBurstScenario{
		{name: "efd-retries/2", packages: 8, packageConcurrency: 8, retries: 2, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "efd-retries/5", packages: 8, packageConcurrency: 8, retries: 5, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "efd-retries/10", packages: 8, packageConcurrency: 8, retries: 10, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "package-scale/1", packages: 1, packageConcurrency: 1, retries: 5, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "package-scale/2", packages: 2, packageConcurrency: 2, retries: 5, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "package-scale/4", packages: 4, packageConcurrency: 4, retries: 5, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "package-scale/8", packages: 8, packageConcurrency: 8, retries: 5, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "go-test-p/1", packages: 8, packageConcurrency: 1, retries: 5, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "go-test-p/2", packages: 8, packageConcurrency: 2, retries: 5, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "go-test-p/4", packages: 8, packageConcurrency: 4, retries: 5, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "go-test-p/8", packages: 8, packageConcurrency: 8, retries: 5, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "process-concurrency/1", packages: 8, packageConcurrency: 8, processConcurrency: 1, retries: 5, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "process-concurrency/2", packages: 8, packageConcurrency: 8, processConcurrency: 2, retries: 5, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "process-concurrency/default", packages: 8, packageConcurrency: 8, retries: 5, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "profile/startup", packages: 8, packageConcurrency: 8, retries: 5, efd: true, parallelEFD: true, parentFails: true, profile: profiles["startup"]},
		{name: "profile/body", packages: 8, packageConcurrency: 8, retries: 5, efd: true, parallelEFD: true, parentFails: true, profile: profiles["body"]},
		{name: "profile/cpu", packages: 8, packageConcurrency: 8, retries: 5, efd: true, parallelEFD: true, parentFails: true, profile: profiles["cpu"]},
	}
	familyScenarios := retryFamilyBenchmarkScenarios(profiles, "")
	scenarios := make([]processRetryBurstScenario, 0, len(head)+len(familyScenarios)+len(tail))
	scenarios = append(scenarios, head...)
	scenarios = append(scenarios, familyScenarios...)
	return append(scenarios, tail...)
}

func retryFamilyBenchmarkScenarios(profiles map[string]processRetryBurstProfile, managedA2FSuffix string) []processRetryBurstScenario {
	return []processRetryBurstScenario{
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
		{name: "families/a2f+disabled" + managedA2FSuffix, packages: 8, packageConcurrency: 8, retries: 3, attemptToFix: true, disabled: true, profile: profiles["startup"]},
		{name: "families/a2f+quarantined" + managedA2FSuffix, packages: 8, packageConcurrency: 8, retries: 3, attemptToFix: true, quarantined: true, profile: profiles["startup"]},
		{name: "families/test-management/disabled", packages: 8, packageConcurrency: 8, disabled: true, parentFails: true},
		{name: "families/test-management/quarantined", packages: 8, packageConcurrency: 8, quarantined: true, parentFails: true},
		{name: "families/itr-forced/ftr-fail-to-pass", packages: 8, packageConcurrency: 8, retries: 1, flakyRetries: true, itrForced: true, parentFails: true, profile: profiles["startup"]},
		{name: "families/coverage/efd-parallel", packages: 8, packageConcurrency: 8, retries: 2, efd: true, parallelEFD: true, coverage: true, parentFails: true, profile: profiles["startup"]},
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
	slices.Sort(values)
	return values[len(values)/2]
}

func medianProcessRetryBurstImprovement(experiment, baseline []processRetryBurstMetrics, value func(processRetryBurstMetrics) time.Duration) float64 {
	if len(experiment) != len(baseline) || len(experiment) == 0 {
		return 0
	}
	improvements := make([]float64, len(experiment))
	for i := range experiment {
		improvements[i] = processRetryBurstImprovement(value(experiment[i]), value(baseline[i]))
	}
	slices.Sort(improvements)
	return improvements[len(improvements)/2]
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
var testRuns atomic.Int32

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

func RunFlaky(t *testing.T, pkg string) {
	attempt := testRuns.Add(1)
	child := integrations.IsProcessRetryChild()
	retry := child || attempt > 1
	if retry && !child {
		reason := os.Getenv("PROCESS_RETRY_BURST_RETRY_REASON")
		post(pkg, "in_process_retry_start", reason)
		defer post(pkg, "in_process_retry_finish", "")
	}
	runWork()
	if retry && os.Getenv("PROCESS_RETRY_BURST_CHILD_FAILS") == "true" {
		t.Fail()
	}
	if !retry && os.Getenv("PROCESS_RETRY_BURST_PARENT_FAILS") == "true" {
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
func TestAFlaky(t *testing.T) { harness.RunFlaky(t, packageName) }

func TestZFirstPassComplete(t *testing.T) { harness.FirstPassComplete(packageName) }

func TestMain(m *testing.M) { harness.RunM(m, %q) }
`
