// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package parallel_testing_retries

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/orchestrion/runtime/built"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	gotesting "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/civisibilitytest"
)

const (
	scenarioEnv           = "PARALLEL_TESTING_RETRIES_SCENARIO"
	externalMockServerEnv = "PARALLEL_TESTING_RETRIES_EXTERNAL_MOCK_SERVER"

	moduleName = "github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/parallel_testing_retries"
	suiteName  = "parallel_testing_retries_test.go"

	parallelPanic = "testing: t.Parallel called multiple times"
)

// scenarioConfig describes one subprocess scenario and the exact CI Visibility
// lifecycle data it must emit.
type scenarioConfig struct {
	testName       string
	settings       civisibilitynet.SettingsResponseData
	env            map[string]string
	knownTests     *civisibilitynet.KnownTestsResponseData
	testManagement *civisibilitynet.TestManagementTestsResponseDataModules
	// expectedTestEvents is the exact number of test events expected after
	// finalization.
	expectedTestEvents int
	// expectedSuiteEvents is the exact number of suite-end events expected after
	// finalization.
	expectedSuiteEvents int
	// expectedModuleEvents is the exact number of module-end events expected after
	// finalization.
	expectedModuleEvents int
	// expectedSessionEvents is the exact number of session-end events expected
	// after finalization.
	expectedSessionEvents int
	// expectedTotalEvents is the exact number of CI Visibility lifecycle events
	// expected after filtering out internal HTTP spans.
	expectedTotalEvents int
	// expectedRetryEvents is the exact number of test events marked as retries.
	expectedRetryEvents int
	// expectedFailureOutput is required in subprocess output when a scenario is
	// expected to fail for a specific reason.
	expectedFailureOutput string
	// expectedResourceEvents overrides the expected number of test events for
	// resourceName when a scenario emits both parent and child test events.
	expectedResourceEvents int
	retryReason            string
	// resourceName overrides the default test resource asserted for the scenario.
	resourceName            string
	validate                func(events civisibilitytest.Events, resource string)
	expectFailure           bool
	validateFailureInParent bool
	// runBenchmark switches subprocess selection from a test run to a benchmark run.
	runBenchmark bool
}

var flakyAttempts atomic.Int32
var subtestPanicAttempts atomic.Int32

func TestMain(m *testing.M) {
	if !built.WithOrchestrion {
		panic("Orchestrion is not enabled, please run this test with orchestrion")
	}

	scenarios := scenariosByName()
	if scenarioName := os.Getenv(scenarioEnv); scenarioName != "" {
		cfg, ok := scenarios[scenarioName]
		if !ok {
			panic(fmt.Sprintf("unknown scenario %q", scenarioName))
		}
		os.Exit(runScenarioChild(m, cfg))
	}

	for _, name := range scenarioOrder() {
		cfg := scenarios[name]
		output, exitCode, err := runScenarioProcess(cfg)
		if err != nil {
			fmt.Printf("scenario %s failed to start: %v\n%s\n", name, err, output)
			os.Exit(1)
		}

		if cfg.expectFailure {
			if exitCode == 0 {
				fmt.Printf("scenario %s expected failure, exit=%d\n%s\n", name, exitCode, output)
				os.Exit(1)
			}
			if cfg.expectedFailureOutput != "" && !strings.Contains(output, cfg.expectedFailureOutput) {
				fmt.Printf("scenario %s expected output %q, exit=%d\n%s\n", name, cfg.expectedFailureOutput, exitCode, output)
				os.Exit(1)
			}
			continue
		}

		if exitCode != 0 {
			fmt.Printf("scenario %s failed with exit=%d\n%s\n", name, exitCode, output)
			os.Exit(exitCode)
		}
		if strings.Contains(output, parallelPanic) {
			fmt.Printf("scenario %s unexpectedly printed duplicate Parallel panic\n%s\n", name, output)
			os.Exit(1)
		}
	}

	os.Exit(0)
}

func runScenarioChild(m *testing.M, cfg scenarioConfig) int {
	if os.Getenv(externalMockServerEnv) == "true" {
		return m.Run()
	}

	payloads, restore := startScenarioMockServer(cfg)
	defer restore()

	code := m.Run()
	resource := suiteName + "." + cfg.testName
	if cfg.expectFailure {
		return code
	}
	if code != 0 {
		return code
	}

	validateScenarioPayloads(payloads, cfg, resource)
	return 0
}

func startScenarioMockServer(cfg scenarioConfig) (*civisibilitytest.Payloads, func()) {
	opts := []civisibilitytest.MockServerOption{
		civisibilitytest.WithSettings(cfg.settings),
	}
	if cfg.knownTests != nil {
		opts = append(opts, civisibilitytest.WithKnownTests(*cfg.knownTests))
	}
	if cfg.testManagement != nil {
		opts = append(opts, civisibilitytest.WithTestManagement(*cfg.testManagement))
	}

	_, payloads, restore := civisibilitytest.StartMockServerWithOptions(opts...)
	return payloads, restore
}

func runScenarioProcess(cfg scenarioConfig) (string, int, error) {
	if cfg.validateFailureInParent {
		payloads, restore := startScenarioMockServer(cfg)
		defer restore()

		childCfg := cfg
		childCfg.env = cloneEnvMap(cfg.env)
		childCfg.env[externalMockServerEnv] = "true"
		output, exitCode, err := runScenarioProcessWithEnv(childCfg)
		if err == nil && hasPayloadExpectations(cfg) {
			validateScenarioPayloads(payloads, cfg, resourceForScenario(cfg))
		}
		return output, exitCode, err
	}
	return runScenarioProcessWithEnv(cfg)
}

func cloneEnvMap(env map[string]string) map[string]string {
	clone := make(map[string]string, len(env)+1)
	maps.Copy(clone, env)
	return clone
}

func runScenarioProcessWithEnv(cfg scenarioConfig) (string, int, error) {
	cmd := exec.Command(os.Args[0], childArgsForScenario(os.Args[1:], cfg)...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	cmd.Env = scenarioEnvForChild(os.Environ(), cfg)

	err := cmd.Run()
	if err == nil {
		return output.String(), 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return output.String(), exitErr.ExitCode(), nil
	}
	return output.String(), 0, err
}

func scenarioEnvForChild(environ []string, cfg scenarioConfig) []string {
	filtered := make([]string, 0, len(environ)+len(cfg.env)+1)
	blocked := map[string]struct{}{
		scenarioEnv:           {},
		externalMockServerEnv: {},
		constants.CIVisibilityFlakyRetryEnabledEnvironmentVariable:                 {},
		constants.CIVisibilityFlakyRetryCountEnvironmentVariable:                   {},
		constants.CIVisibilityTotalFlakyRetryCountEnvironmentVariable:              {},
		constants.CIVisibilityTestManagementEnabledEnvironmentVariable:             {},
		constants.CIVisibilityTestManagementAttemptToFixRetriesEnvironmentVariable: {},
		constants.CIVisibilityInternalParallelEarlyFlakeDetectionEnabled:           {},
	}
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := blocked[key]; ok {
			continue
		}
		filtered = append(filtered, entry)
	}
	filtered = append(filtered, scenarioEnv+"="+cfg.testName)
	for key, value := range cfg.env {
		filtered = append(filtered, key+"="+value)
	}
	return filtered
}

func childArgsForScenario(args []string, cfg scenarioConfig) []string {
	filtered := make([]string, 0, len(args)+1)
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if shouldDropTestFlag(arg) || cfg.runBenchmark && shouldDropBenchmarkFlag(arg) {
			if !strings.Contains(arg, "=") {
				skipNext = true
			}
			continue
		}
		filtered = append(filtered, arg)
	}
	if cfg.runBenchmark {
		return append(filtered, "-test.run=^$", "-test.bench=^"+cfg.testName+"$", "-test.benchtime=1x")
	}
	return append(filtered, "-test.run=^"+cfg.testName+"$")
}

func shouldDropTestFlag(arg string) bool {
	switch arg {
	case "-test.run", "-run", "-test.skip", "-skip", "-test.list", "-list":
		return true
	}
	return strings.HasPrefix(arg, "-test.run=") ||
		strings.HasPrefix(arg, "-run=") ||
		strings.HasPrefix(arg, "-test.skip=") ||
		strings.HasPrefix(arg, "-skip=") ||
		strings.HasPrefix(arg, "-test.list=") ||
		strings.HasPrefix(arg, "-list=")
}

// shouldDropBenchmarkFlag reports whether an inherited benchmark selector
// would conflict with the scenario-specific benchmark invocation.
func shouldDropBenchmarkFlag(arg string) bool {
	switch arg {
	case "-test.bench", "-bench", "-test.benchtime", "-benchtime":
		return true
	}
	return strings.HasPrefix(arg, "-test.bench=") ||
		strings.HasPrefix(arg, "-bench=") ||
		strings.HasPrefix(arg, "-test.benchtime=") ||
		strings.HasPrefix(arg, "-benchtime=")
}

// resourceForScenario returns the test resource whose event count should be
// asserted for the scenario.
func resourceForScenario(cfg scenarioConfig) string {
	if cfg.resourceName != "" {
		return cfg.resourceName
	}
	return suiteName + "." + cfg.testName
}

// hasPayloadExpectations reports whether a scenario should validate CI
// Visibility payload contents after its subprocess exits.
func hasPayloadExpectations(cfg scenarioConfig) bool {
	return cfg.expectedTotalEvents > 0 ||
		cfg.expectedTestEvents > 0 ||
		cfg.expectedSuiteEvents > 0 ||
		cfg.expectedModuleEvents > 0 ||
		cfg.expectedSessionEvents > 0
}

// testCycleEvents returns only CI Visibility lifecycle events and excludes
// internal HTTP spans emitted while the mock intake is active.
func testCycleEvents(events civisibilitytest.Events) civisibilitytest.Events {
	var result civisibilitytest.Events
	for _, event := range events {
		switch event.Type {
		case constants.SpanTypeTest, constants.SpanTypeTestSuite, constants.SpanTypeTestModule, constants.SpanTypeTestSession:
			result = append(result, event)
		}
	}
	return result
}

// validateScenarioPayloads checks that a scenario flushed at least one payload
// and exactly the CI Visibility lifecycle events expected by its configuration.
func validateScenarioPayloads(payloads *civisibilitytest.Payloads, cfg scenarioConfig, resource string) {
	payloads.CheckPayloadCountAtLeast(1)
	events := testCycleEvents(payloads.Events()).HasCount(cfg.expectedTotalEvents)
	testEvents := events.CheckEventsByType(constants.SpanTypeTest, cfg.expectedTestEvents)
	expectedResourceEvents := cfg.expectedResourceEvents
	if expectedResourceEvents == 0 {
		expectedResourceEvents = cfg.expectedTestEvents
	}
	testEvents.CheckEventsByResourceName(resource, expectedResourceEvents)
	events.CheckEventsByType(constants.SpanTypeTestSuite, cfg.expectedSuiteEvents).CheckEventsByResourceName(suiteName, cfg.expectedSuiteEvents)
	events.CheckEventsByType(constants.SpanTypeTestModule, cfg.expectedModuleEvents).CheckEventsByResourceName(moduleName, cfg.expectedModuleEvents)
	events.CheckEventsByType(constants.SpanTypeTestSession, cfg.expectedSessionEvents)
	if cfg.expectedRetryEvents > 0 || cfg.retryReason != "" {
		testEvents.CheckEventsByTagAndValue(constants.TestIsRetry, "true", cfg.expectedRetryEvents)
		if cfg.retryReason != "" {
			testEvents.CheckEventsByTagAndValue(constants.TestRetryReason, cfg.retryReason, cfg.expectedRetryEvents)
		}
	} else {
		testEvents.CheckEventsWithoutTag(constants.TestRetryReason, cfg.expectedTestEvents)
	}
	if cfg.validate != nil {
		cfg.validate(testEvents, resource)
	}
}

func scenarioOrder() []string {
	return []string{
		"TestParallelWrapperNoRetry",
		"TestParallelEFDSequential",
		"TestParallelEFDParallel",
		"TestParallelFlakyRetry",
		"TestParallelAttemptToFix",
		"TestSubtestPanicFlakyRetry",
		"TestFailNowSingleExecution",
		"TestManualFailNowSingleExecution",
		"BenchmarkManualFailNow",
		"TestDuplicateParallel",
		"TestConcurrentDuplicateParallel",
		"TestSubtestDuplicateParallel",
	}
}

func scenariosByName() map[string]scenarioConfig {
	return map[string]scenarioConfig{
		"TestParallelWrapperNoRetry": {
			testName:              "TestParallelWrapperNoRetry",
			settings:              efdSettings(),
			knownTests:            knownTests("TestParallelWrapperNoRetry"),
			expectedTestEvents:    1,
			expectedSuiteEvents:   1,
			expectedModuleEvents:  1,
			expectedSessionEvents: 1,
			expectedTotalEvents:   4,
			expectedRetryEvents:   0,
			validate: func(events civisibilitytest.Events, _ string) {
				events.CheckEventsByTagAndValue(constants.TestStatus, constants.TestStatusPass, 1)
				events.CheckEventsByTagAndValue(constants.TestFinalStatus, constants.TestStatusPass, 1)
				events.CheckEventsWithoutTag(constants.TestIsNew, 1)
			},
		},
		"TestParallelEFDSequential": {
			testName:              "TestParallelEFDSequential",
			settings:              efdSettings(),
			knownTests:            knownTests("TestKnownBaseline"),
			expectedTestEvents:    2,
			expectedSuiteEvents:   1,
			expectedModuleEvents:  1,
			expectedSessionEvents: 1,
			expectedTotalEvents:   5,
			expectedRetryEvents:   1,
			retryReason:           constants.EarlyFlakeDetectionRetryReason,
			validate: func(events civisibilitytest.Events, _ string) {
				events.CheckEventsByTagAndValue(constants.TestStatus, constants.TestStatusPass, 2)
				events.CheckEventsByTagAndValue(constants.TestFinalStatus, constants.TestStatusPass, 1)
				events.CheckEventsByTagAndValue(constants.TestIsNew, "true", 2)
			},
		},
		"TestParallelEFDParallel": {
			testName:              "TestParallelEFDParallel",
			settings:              efdSettings(),
			env:                   map[string]string{constants.CIVisibilityInternalParallelEarlyFlakeDetectionEnabled: "true"},
			knownTests:            knownTests("TestKnownBaseline"),
			expectedTestEvents:    2,
			expectedSuiteEvents:   1,
			expectedModuleEvents:  1,
			expectedSessionEvents: 1,
			expectedTotalEvents:   5,
			expectedRetryEvents:   1,
			retryReason:           constants.EarlyFlakeDetectionRetryReason,
			validate: func(events civisibilitytest.Events, _ string) {
				events.CheckEventsByTagAndValue(constants.TestStatus, constants.TestStatusPass, 2)
				events.CheckEventsWithoutTag(constants.TestFinalStatus, 2)
				events.CheckEventsByTagAndValue(constants.TestIsNew, "true", 2)
			},
		},
		"TestParallelFlakyRetry": {
			testName: "TestParallelFlakyRetry",
			settings: flakyRetrySettings(),
			env: map[string]string{
				constants.CIVisibilityFlakyRetryEnabledEnvironmentVariable:    "true",
				constants.CIVisibilityFlakyRetryCountEnvironmentVariable:      "1",
				constants.CIVisibilityTotalFlakyRetryCountEnvironmentVariable: "1",
			},
			expectedTestEvents:    2,
			expectedSuiteEvents:   1,
			expectedModuleEvents:  1,
			expectedSessionEvents: 1,
			expectedTotalEvents:   5,
			expectedRetryEvents:   1,
			retryReason:           constants.AutoTestRetriesRetryReason,
			validate: func(events civisibilitytest.Events, _ string) {
				events.CheckEventsByTagAndValue(constants.TestStatus, constants.TestStatusFail, 1)
				events.CheckEventsByTagAndValue(constants.TestStatus, constants.TestStatusPass, 1)
				events.CheckEventsByTagAndValue(constants.TestFinalStatus, constants.TestStatusPass, 1)
			},
		},
		"TestParallelAttemptToFix": {
			testName: "TestParallelAttemptToFix",
			settings: attemptToFixSettings(),
			env: map[string]string{
				constants.CIVisibilityTestManagementAttemptToFixRetriesEnvironmentVariable: "2",
				constants.CIVisibilityTestManagementEnabledEnvironmentVariable:             "true",
			},
			testManagement:        attemptToFixTests(),
			expectedTestEvents:    2,
			expectedSuiteEvents:   1,
			expectedModuleEvents:  1,
			expectedSessionEvents: 1,
			expectedTotalEvents:   5,
			expectedRetryEvents:   1,
			retryReason:           constants.AttemptToFixRetryReason,
			validate: func(events civisibilitytest.Events, _ string) {
				events.CheckEventsByTagAndValue(constants.TestStatus, constants.TestStatusPass, 2)
				events.CheckEventsByTagAndValue(constants.TestFinalStatus, constants.TestStatusPass, 1)
				events.CheckEventsByTagAndValue(constants.TestIsAttempToFix, "true", 2)
				events.CheckEventsByTagAndValue(constants.TestAttemptToFixPassed, "true", 1)
				events.CheckEventsByTagAndValue(constants.TestHasFailedAllRetries, "true", 0)
			},
		},
		"TestSubtestPanicFlakyRetry": {
			testName: "TestSubtestPanicFlakyRetry",
			settings: flakyRetrySettings(),
			env: map[string]string{
				constants.CIVisibilityFlakyRetryEnabledEnvironmentVariable:    "true",
				constants.CIVisibilityFlakyRetryCountEnvironmentVariable:      "1",
				constants.CIVisibilityTotalFlakyRetryCountEnvironmentVariable: "1",
			},
			expectedTestEvents:     4,
			expectedSuiteEvents:    1,
			expectedModuleEvents:   1,
			expectedSessionEvents:  1,
			expectedTotalEvents:    7,
			expectedRetryEvents:    2,
			retryReason:            constants.AutoTestRetriesRetryReason,
			expectedResourceEvents: 2,
			resourceName:           suiteName + ".TestSubtestPanicFlakyRetry/child",
			validate: func(events civisibilitytest.Events, _ string) {
				events.CheckEventsByTagAndValue(constants.TestStatus, constants.TestStatusFail, 2)
				events.CheckEventsByTagAndValue(constants.TestStatus, constants.TestStatusPass, 2)
				events.CheckEventsByTagAndValue(constants.TestFinalStatus, constants.TestStatusPass, 2)
			},
		},
		"TestFailNowSingleExecution": {
			testName:                "TestFailNowSingleExecution",
			expectedTestEvents:      1,
			expectedSuiteEvents:     1,
			expectedModuleEvents:    1,
			expectedSessionEvents:   1,
			expectedTotalEvents:     4,
			expectFailure:           true,
			validateFailureInParent: true,
			validate: func(events civisibilitytest.Events, _ string) {
				events.CheckEventsByTagAndValue(constants.TestStatus, constants.TestStatusFail, 1)
				events.CheckEventsByTagAndValue(constants.TestFinalStatus, constants.TestStatusFail, 1)
			},
		},
		"TestManualFailNowSingleExecution": {
			testName:                "TestManualFailNowSingleExecution",
			expectedTestEvents:      1,
			expectedSuiteEvents:     1,
			expectedModuleEvents:    1,
			expectedSessionEvents:   1,
			expectedTotalEvents:     4,
			expectFailure:           true,
			validateFailureInParent: true,
			validate: func(events civisibilitytest.Events, _ string) {
				events.CheckEventsByTagAndValue(constants.TestStatus, constants.TestStatusFail, 1)
				events.CheckEventsByTagAndValue(constants.TestFinalStatus, constants.TestStatusFail, 1)
			},
		},
		"BenchmarkManualFailNow": {
			testName:                "BenchmarkManualFailNow",
			expectedTestEvents:      1,
			expectedSuiteEvents:     1,
			expectedModuleEvents:    1,
			expectedSessionEvents:   1,
			expectedTotalEvents:     4,
			expectFailure:           true,
			validateFailureInParent: true,
			runBenchmark:            true,
			validate: func(events civisibilitytest.Events, _ string) {
				events.CheckEventsByTagAndValue(constants.TestStatus, constants.TestStatusFail, 1)
				events.CheckEventsByTagAndValue(constants.TestType, constants.TestTypeBenchmark, 1)
			},
		},
		"TestDuplicateParallel": {
			testName: "TestDuplicateParallel",
			settings: flakyRetrySettings(),
			env: map[string]string{
				constants.CIVisibilityFlakyRetryEnabledEnvironmentVariable:    "true",
				constants.CIVisibilityFlakyRetryCountEnvironmentVariable:      "1",
				constants.CIVisibilityTotalFlakyRetryCountEnvironmentVariable: "1",
			},
			expectFailure:         true,
			expectedFailureOutput: parallelPanic,
		},
		"TestConcurrentDuplicateParallel": {
			testName: "TestConcurrentDuplicateParallel",
			settings: flakyRetrySettings(),
			env: map[string]string{
				constants.CIVisibilityFlakyRetryEnabledEnvironmentVariable:    "true",
				constants.CIVisibilityFlakyRetryCountEnvironmentVariable:      "1",
				constants.CIVisibilityTotalFlakyRetryCountEnvironmentVariable: "1",
			},
			expectFailure:         true,
			expectedFailureOutput: parallelPanic,
		},
		"TestSubtestDuplicateParallel": {
			testName: "TestSubtestDuplicateParallel",
			settings: flakyRetrySettings(),
			env: map[string]string{
				constants.CIVisibilityFlakyRetryEnabledEnvironmentVariable:    "true",
				constants.CIVisibilityFlakyRetryCountEnvironmentVariable:      "1",
				constants.CIVisibilityTotalFlakyRetryCountEnvironmentVariable: "1",
			},
			expectedTestEvents:      4,
			expectedSuiteEvents:     1,
			expectedModuleEvents:    1,
			expectedSessionEvents:   1,
			expectedTotalEvents:     7,
			expectedRetryEvents:     2,
			retryReason:             constants.AutoTestRetriesRetryReason,
			expectedResourceEvents:  2,
			resourceName:            suiteName + ".TestSubtestDuplicateParallel/child",
			expectFailure:           true,
			validateFailureInParent: true,
			validate: func(events civisibilitytest.Events, _ string) {
				events.CheckEventsByTagAndValue(constants.TestStatus, constants.TestStatusFail, 4)
				events.CheckEventsByTagAndValue(constants.TestStatus, constants.TestStatusPass, 0)
				events.CheckEventsByTagAndValue(constants.TestFinalStatus, constants.TestStatusFail, 2)
				events.CheckEventsByTagAndValue(constants.TestFinalStatus, constants.TestStatusPass, 0)
			},
		},
	}
}

func efdSettings() civisibilitynet.SettingsResponseData {
	var settings civisibilitynet.SettingsResponseData
	settings.KnownTestsEnabled = true
	settings.EarlyFlakeDetection.Enabled = true
	settings.EarlyFlakeDetection.SlowTestRetries.FiveS = 1
	return settings
}

func flakyRetrySettings() civisibilitynet.SettingsResponseData {
	var settings civisibilitynet.SettingsResponseData
	settings.FlakyTestRetriesEnabled = true
	return settings
}

func attemptToFixSettings() civisibilitynet.SettingsResponseData {
	var settings civisibilitynet.SettingsResponseData
	settings.TestManagement.Enabled = true
	settings.TestManagement.AttemptToFixRetries = 2
	return settings
}

func knownTests(testNames ...string) *civisibilitynet.KnownTestsResponseData {
	return &civisibilitynet.KnownTestsResponseData{
		Tests: civisibilitynet.KnownTestsResponseDataModules{
			moduleName: civisibilitynet.KnownTestsResponseDataSuites{
				suiteName: testNames,
			},
		},
	}
}

func attemptToFixTests() *civisibilitynet.TestManagementTestsResponseDataModules {
	return &civisibilitynet.TestManagementTestsResponseDataModules{
		Modules: map[string]civisibilitynet.TestManagementTestsResponseDataSuites{
			moduleName: {
				Suites: map[string]civisibilitynet.TestManagementTestsResponseDataTests{
					suiteName: {
						Tests: map[string]civisibilitynet.TestManagementTestsResponseDataTestProperties{
							"TestParallelAttemptToFix": {
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
}

func skipUnlessScenario(t *testing.T, name string) {
	t.Helper()
	if os.Getenv(scenarioEnv) != name {
		t.Skip("scenario not selected")
	}
}

func TestParallelEFDSequential(t *testing.T) {
	skipUnlessScenario(t, "TestParallelEFDSequential")
	t.Parallel()
}

func TestParallelWrapperNoRetry(t *testing.T) {
	skipUnlessScenario(t, "TestParallelWrapperNoRetry")
	t.Parallel()
}

func TestParallelEFDParallel(t *testing.T) {
	skipUnlessScenario(t, "TestParallelEFDParallel")
	t.Parallel()
}

func TestParallelFlakyRetry(t *testing.T) {
	skipUnlessScenario(t, "TestParallelFlakyRetry")
	t.Parallel()
	if flakyAttempts.Add(1) == 1 {
		t.Fatal("fail first attempt")
	}
}

func TestParallelAttemptToFix(t *testing.T) {
	skipUnlessScenario(t, "TestParallelAttemptToFix")
	t.Parallel()
}

func TestSubtestPanicFlakyRetry(t *testing.T) {
	skipUnlessScenario(t, "TestSubtestPanicFlakyRetry")
	t.Run("child", func(t *testing.T) {
		if subtestPanicAttempts.Add(1) == 1 {
			panic("subtest panic on first attempt")
		}
	})
}

func TestFailNowSingleExecution(t *testing.T) {
	skipUnlessScenario(t, "TestFailNowSingleExecution")
	t.FailNow()
}

func TestManualFailNowSingleExecution(t *testing.T) {
	skipUnlessScenario(t, "TestManualFailNowSingleExecution")
	gotesting.GetTest(t).FailNow()
}

func TestDuplicateParallel(t *testing.T) {
	skipUnlessScenario(t, "TestDuplicateParallel")
	t.Parallel()
	t.Parallel()
}

func TestConcurrentDuplicateParallel(t *testing.T) {
	skipUnlessScenario(t, "TestConcurrentDuplicateParallel")
	t.Parallel()

	start := make(chan struct{})
	done := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			<-start
			t.Parallel()
		})
	}
	go func() {
		wg.Wait()
		close(done)
	}()
	close(start)

	select {
	case <-done:
		t.Fatal("expected duplicate Parallel calls to panic")
	case <-time.After(5 * time.Second):
		t.Fatal("duplicate Parallel calls did not panic or complete")
	}
}

func TestSubtestDuplicateParallel(t *testing.T) {
	skipUnlessScenario(t, "TestSubtestDuplicateParallel")
	t.Run("child", func(t *testing.T) {
		t.Parallel()
		t.Parallel()
	})
}

func BenchmarkManualFailNow(b *testing.B) {
	if os.Getenv(scenarioEnv) != "BenchmarkManualFailNow" {
		b.Skip("scenario not selected")
	}
	gotesting.GetBenchmark(b).FailNow()
}
