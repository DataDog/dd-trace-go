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

type scenarioConfig struct {
	testName                string
	settings                civisibilitynet.SettingsResponseData
	env                     map[string]string
	knownTests              *civisibilitynet.KnownTestsResponseData
	testManagement          *civisibilitynet.TestManagementTestsResponseDataModules
	expectedTestEvents      int
	expectedRetryEvents     int
	retryReason             string
	validate                func(events civisibilitytest.Events, resource string)
	expectFailure           bool
	validateFailureInParent bool
}

var flakyAttempts atomic.Int32

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
			if exitCode == 0 || !strings.Contains(output, parallelPanic) {
				fmt.Printf("scenario %s expected duplicate Parallel panic, exit=%d\n%s\n", name, exitCode, output)
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

	events := payloads.Events().CheckEventsByType("test", cfg.expectedTestEvents).CheckEventsByResourceName(resource, cfg.expectedTestEvents)
	events.CheckEventsByTagAndValue(constants.TestIsRetry, "true", cfg.expectedRetryEvents)
	if cfg.retryReason != "" {
		events.CheckEventsByTagAndValue(constants.TestRetryReason, cfg.retryReason, cfg.expectedRetryEvents)
	} else {
		events.CheckEventsWithoutTag(constants.TestRetryReason, cfg.expectedTestEvents)
	}
	cfg.validate(events, resource)
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
		if err == nil && cfg.validate != nil {
			resource := suiteName + "." + cfg.testName
			cfg.validate(payloads.Events(), resource)
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
	cmd := exec.Command(os.Args[0], childArgsForScenario(os.Args[1:], cfg.testName)...)
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

func childArgsForScenario(args []string, testName string) []string {
	filtered := make([]string, 0, len(args)+1)
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if shouldDropTestFlag(arg) {
			if !strings.Contains(arg, "=") {
				skipNext = true
			}
			continue
		}
		filtered = append(filtered, arg)
	}
	return append(filtered, "-test.run=^"+testName+"$")
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

func scenarioOrder() []string {
	return []string{
		"TestParallelWrapperNoRetry",
		"TestParallelEFDSequential",
		"TestParallelEFDParallel",
		"TestParallelFlakyRetry",
		"TestParallelAttemptToFix",
		"TestDuplicateParallel",
		"TestConcurrentDuplicateParallel",
		"TestSubtestDuplicateParallel",
	}
}

func scenariosByName() map[string]scenarioConfig {
	return map[string]scenarioConfig{
		"TestParallelWrapperNoRetry": {
			testName:            "TestParallelWrapperNoRetry",
			settings:            efdSettings(),
			knownTests:          knownTests("TestParallelWrapperNoRetry"),
			expectedTestEvents:  1,
			expectedRetryEvents: 0,
			validate: func(events civisibilitytest.Events, _ string) {
				events.CheckEventsByTagAndValue(constants.TestStatus, constants.TestStatusPass, 1)
				events.CheckEventsByTagAndValue(constants.TestFinalStatus, constants.TestStatusPass, 1)
				events.CheckEventsWithoutTag(constants.TestIsNew, 1)
			},
		},
		"TestParallelEFDSequential": {
			testName:            "TestParallelEFDSequential",
			settings:            efdSettings(),
			knownTests:          knownTests("TestKnownBaseline"),
			expectedTestEvents:  2,
			expectedRetryEvents: 1,
			retryReason:         constants.EarlyFlakeDetectionRetryReason,
			validate: func(events civisibilitytest.Events, _ string) {
				events.CheckEventsByTagAndValue(constants.TestStatus, constants.TestStatusPass, 2)
				events.CheckEventsByTagAndValue(constants.TestFinalStatus, constants.TestStatusPass, 1)
				events.CheckEventsByTagAndValue(constants.TestIsNew, "true", 2)
			},
		},
		"TestParallelEFDParallel": {
			testName:            "TestParallelEFDParallel",
			settings:            efdSettings(),
			env:                 map[string]string{constants.CIVisibilityInternalParallelEarlyFlakeDetectionEnabled: "true"},
			knownTests:          knownTests("TestKnownBaseline"),
			expectedTestEvents:  2,
			expectedRetryEvents: 1,
			retryReason:         constants.EarlyFlakeDetectionRetryReason,
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
			expectedTestEvents:  2,
			expectedRetryEvents: 1,
			retryReason:         constants.AutoTestRetriesRetryReason,
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
			testManagement:      attemptToFixTests(),
			expectedTestEvents:  2,
			expectedRetryEvents: 1,
			retryReason:         constants.AttemptToFixRetryReason,
			validate: func(events civisibilitytest.Events, _ string) {
				events.CheckEventsByTagAndValue(constants.TestStatus, constants.TestStatusPass, 2)
				events.CheckEventsByTagAndValue(constants.TestFinalStatus, constants.TestStatusPass, 1)
				events.CheckEventsByTagAndValue(constants.TestIsAttempToFix, "true", 2)
				events.CheckEventsByTagAndValue(constants.TestAttemptToFixPassed, "true", 1)
				events.CheckEventsByTagAndValue(constants.TestHasFailedAllRetries, "true", 0)
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
			expectFailure: true,
		},
		"TestConcurrentDuplicateParallel": {
			testName: "TestConcurrentDuplicateParallel",
			settings: flakyRetrySettings(),
			env: map[string]string{
				constants.CIVisibilityFlakyRetryEnabledEnvironmentVariable:    "true",
				constants.CIVisibilityFlakyRetryCountEnvironmentVariable:      "1",
				constants.CIVisibilityTotalFlakyRetryCountEnvironmentVariable: "1",
			},
			expectFailure: true,
		},
		"TestSubtestDuplicateParallel": {
			testName: "TestSubtestDuplicateParallel",
			settings: flakyRetrySettings(),
			env: map[string]string{
				constants.CIVisibilityFlakyRetryEnabledEnvironmentVariable:    "true",
				constants.CIVisibilityFlakyRetryCountEnvironmentVariable:      "1",
				constants.CIVisibilityTotalFlakyRetryCountEnvironmentVariable: "1",
			},
			expectFailure:           true,
			validateFailureInParent: true,
			validate: func(events civisibilitytest.Events, _ string) {
				events.CheckEventsByType(constants.SpanTypeTestSuite, 1).CheckEventsByResourceName(suiteName, 1)
				events.CheckEventsByType(constants.SpanTypeTestModule, 1).CheckEventsByResourceName(moduleName, 1)
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
