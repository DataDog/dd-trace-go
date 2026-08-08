// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/coverage"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	processRetryBatchVersion      = 1
	processRetryBatchGateVersion  = 1
	processRetryBatchTestName     = "__dd_quarantined_race_batch__"
	processRetryBatchReason       = "quarantined_race"
	processRetryBatchMaxTests     = 100_000
	processRetryBatchMaxBytes     = 16 * 1024 * 1024
	processRetryBatchManifestMode = 0o600
	processRetryBatchPollInterval = 5 * time.Millisecond
)

var errProcessRetryBatchFailed = errors.New("process retry batch failed")

type processRetryBatchTestConfig struct {
	TestName             string                         `json:"test_name"`
	InvocationOrdinal    uint64                         `json:"invocation_ordinal,omitempty"`
	DisabledSubtests     []string                       `json:"disabled_subtests,omitempty"`
	QuarantinedSubtests  []string                       `json:"quarantined_subtests,omitempty"`
	AttemptToFixSubtests []string                       `json:"attempt_to_fix_subtests,omitempty"`
	ITRSubtests          []processRetrySubtestITRConfig `json:"itr_subtests,omitempty"`
}

type processRetrySubtestITRConfig struct {
	TestName                string `json:"test_name"`
	MissingLineCodeCoverage bool   `json:"missing_line_code_coverage,omitempty"`
}

type processRetryBatchConfig struct {
	Version                int                           `json:"version"`
	Tests                  []processRetryBatchTestConfig `json:"tests"`
	AttemptToFixRetries    int                           `json:"attempt_to_fix_retries,omitempty"`
	CollectPerTestCoverage bool                          `json:"collect_per_test_coverage,omitempty"`
	PreserveNativeSchedule bool                          `json:"preserve_native_schedule,omitempty"`
	ITRCoverageActive      bool                          `json:"itr_coverage_active,omitempty"`
	ImpactedTestsEnabled   bool                          `json:"impacted_tests_enabled,omitempty"`
}

type processRetryBatchInvocationState struct {
	Version          int      `json:"version"`
	WorkingDirectory string   `json:"working_directory"`
	Environment      []string `json:"environment"`
}

func processRetryBatchManifestPath(resultPath string) string {
	return filepath.Clean(resultPath) + ".batch.json"
}

func processRetryBatchCoveragePath(resultPath string) string {
	return filepath.Clean(resultPath) + ".coverage.out"
}

func processRetryBatchResultPath(resultPath string, index int) string {
	return filepath.Join(filepath.Dir(resultPath), fmt.Sprintf("batch-result-%06d.json", index))
}

func processRetryBatchGatePath(resultPath string, index int) string {
	return filepath.Join(filepath.Dir(resultPath), fmt.Sprintf("batch-gate-%06d", index))
}

func processRetryBatchSkipPath(resultPath string, index int) string {
	return processRetryBatchGatePath(resultPath, index) + ".skip"
}

func processRetryBatchParallelPath(resultPath string, index int) string {
	return filepath.Join(filepath.Dir(resultPath), fmt.Sprintf("batch-parallel-%06d", index))
}

func processRetryBatchEnumerationPath(resultPath string) string {
	return filepath.Join(filepath.Dir(resultPath), "batch-parent-enumerated")
}

func processRetryBatchFinalStatePath(resultPath string) string {
	return filepath.Clean(resultPath) + ".state"
}

func processRetryBatchChildConfig(root processRetryChildConfig, index int, test processRetryBatchTestConfig) processRetryChildConfig {
	child := processRetryChildConfig{
		ResultPath:             processRetryBatchResultPath(root.ResultPath, index),
		TestName:               test.TestName,
		Attempt:                root.Attempt,
		RetryReason:            root.RetryReason,
		MRunEpoch:              root.MRunEpoch,
		InvocationOrdinal:      test.InvocationOrdinal,
		ParentDeadlineUnixNano: root.ParentDeadlineUnixNano,
		ParentDeadlineOK:       root.ParentDeadlineOK,
		ObservedGOMAXPROCS:     root.ObservedGOMAXPROCS,
		BatchChild:             true,
		CollectPerTestCoverage: root.Batch != nil && root.Batch.CollectPerTestCoverage,
		batchTest:              &test,
	}
	if root.Batch != nil {
		child.attemptToFixRetries = root.Batch.AttemptToFixRetries
		child.itrCoverageActive = root.Batch.ITRCoverageActive
		child.impactedTestsAnalyzer = root.impactedTestsAnalyzer
	}
	if root.Batch != nil && root.Batch.PreserveNativeSchedule {
		// The native parent owns invocation identity; the child manifest is
		// registered before those ordinals exist and is matched by batch index.
		child.MRunEpoch = 0
		child.InvocationOrdinal = 0
		child.nativeGatePath = processRetryBatchGatePath(root.ResultPath, index)
		child.nativeParallelPath = processRetryBatchParallelPath(root.ResultPath, index)
		child.nativeEnumerationPath = processRetryBatchEnumerationPath(root.ResultPath)
	}
	return child
}

func waitForProcessRetryBatchGate(path string, deadline time.Time, deadlineOK bool) (processRetryBatchInvocationState, bool, error) {
	for {
		if _, err := os.Stat(path); err == nil {
			state, err := readProcessRetryBatchInvocationState(path)
			return state, err == nil, err
		} else if !errors.Is(err, os.ErrNotExist) {
			return processRetryBatchInvocationState{}, false, err
		}
		if _, err := os.Stat(path + ".skip"); err == nil {
			return processRetryBatchInvocationState{}, false, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return processRetryBatchInvocationState{}, false, err
		}
		if deadlineOK && !time.Now().Before(deadline) {
			return processRetryBatchInvocationState{}, false, context.DeadlineExceeded
		}
		time.Sleep(processRetryBatchPollInterval)
	}
}

func writeProcessRetryBatchInvocationState(path string, baseline *processRetryLaunchBaseline) error {
	if baseline == nil {
		return errors.New("missing process retry invocation state")
	}
	state := processRetryBatchInvocationState{
		Version:          processRetryBatchGateVersion,
		WorkingDirectory: baseline.workingDirectory,
		Environment:      make([]string, 0, len(baseline.environment)),
	}
	for _, entry := range baseline.environment {
		key, _, ok := strings.Cut(entry, "=")
		if ok && key == "" {
			continue
		}
		state.Environment = append(state.Environment, entry)
	}
	if err := validateProcessRetryBatchInvocationState(state); err != nil {
		return err
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(payload) > processRetryBatchMaxBytes {
		return errors.New("process retry invocation state too large")
	}
	return writeProcessRetryFileAtomically(path, payload, ".process-retry-gate-*.tmp")
}

func writeCurrentProcessRetryBatchInvocationState(path string) error {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return err
	}
	return writeProcessRetryBatchInvocationState(path, &processRetryLaunchBaseline{
		workingDirectory: workingDirectory,
		environment:      sanitizeProcessRetryBaseEnv(os.Environ()),
	})
}

func readProcessRetryBatchInvocationState(path string) (processRetryBatchInvocationState, error) {
	state, err := readProcessRetryBatchJSON[processRetryBatchInvocationState](path, "process retry invocation state")
	if err != nil {
		return processRetryBatchInvocationState{}, err
	}
	if err := validateProcessRetryBatchInvocationState(state); err != nil {
		return processRetryBatchInvocationState{}, err
	}
	return state, nil
}

func validateProcessRetryBatchInvocationState(state processRetryBatchInvocationState) error {
	if state.Version != processRetryBatchGateVersion || !filepath.IsAbs(state.WorkingDirectory) {
		return errors.New("invalid process retry invocation state")
	}
	seen := make(map[string]struct{}, len(state.Environment))
	for _, entry := range state.Environment {
		key, value, ok := strings.Cut(entry, "=")
		normalizedKey := processRetryInvocationEnvironmentKey(key)
		if !ok || key == "" || strings.IndexByte(key, 0) >= 0 || strings.IndexByte(value, 0) >= 0 ||
			isProcessRetryExcludedEnvKey(key) {
			return errors.New("invalid process retry invocation environment")
		}
		if _, duplicate := seen[normalizedKey]; duplicate {
			return errors.New("duplicate process retry invocation environment")
		}
		seen[normalizedKey] = struct{}{}
	}
	return nil
}

func applyProcessRetryBatchInvocationState(state processRetryBatchInvocationState) error {
	return applyProcessRetryBatchInvocationStateWithOptions(state, false)
}

func applyProcessRetryBatchFinalState(state processRetryBatchInvocationState) error {
	return applyProcessRetryBatchInvocationStateWithOptions(state, true)
}

func applyProcessRetryBatchInvocationStateWithOptions(state processRetryBatchInvocationState, preserveExcludedEnvironment bool) error {
	if err := validateProcessRetryBatchInvocationState(state); err != nil {
		return err
	}
	if err := os.Chdir(state.WorkingDirectory); err != nil {
		return err
	}
	desired := make(map[string]struct{}, len(state.Environment))
	for _, entry := range state.Environment {
		key, _, _ := strings.Cut(entry, "=")
		desired[processRetryInvocationEnvironmentKey(key)] = struct{}{}
	}
	preserved := make(map[string]struct{})
	if preserveExcludedEnvironment {
		for _, entry := range os.Environ() {
			key, value, ok := strings.Cut(entry, "=")
			if ok && (isProcessRetryExcludedEnvKey(key) || isProcessRetryParentOnlyEnv(key, value)) {
				preserved[processRetryInvocationEnvironmentKey(key)] = struct{}{}
			}
		}
	}
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		if _, ok := preserved[processRetryInvocationEnvironmentKey(key)]; ok {
			continue
		}
		if _, ok := desired[processRetryInvocationEnvironmentKey(key)]; !ok {
			if err := os.Unsetenv(key); err != nil {
				return err
			}
		}
	}
	for _, entry := range state.Environment {
		key, value, _ := strings.Cut(entry, "=")
		if _, ok := preserved[processRetryInvocationEnvironmentKey(key)]; ok {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return nil
}

func processRetryInvocationEnvironmentKey(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}

func writeProcessRetryBatchConfig(path string, cfg *processRetryBatchConfig) error {
	if err := validateProcessRetryBatchConfig(cfg); err != nil {
		return err
	}
	payload, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if len(payload) > processRetryBatchMaxBytes {
		return errors.New("process retry batch manifest too large")
	}
	return os.WriteFile(path, payload, processRetryBatchManifestMode)
}

func readProcessRetryBatchConfig(path string) (*processRetryBatchConfig, error) {
	cfg, err := readProcessRetryBatchJSON[processRetryBatchConfig](path, "process retry batch manifest")
	if err != nil {
		return nil, err
	}
	if err := validateProcessRetryBatchConfig(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func readProcessRetryBatchJSON[T any](path, kind string) (T, error) {
	var value T
	file, err := os.Open(path)
	if err != nil {
		return value, err
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, processRetryBatchMaxBytes+1))
	if err != nil {
		return value, err
	}
	if len(payload) > processRetryBatchMaxBytes {
		return value, fmt.Errorf("%s too large", kind)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return value, fmt.Errorf("%s has trailing data", kind)
	}
	return value, nil
}

func validateProcessRetryBatchConfig(cfg *processRetryBatchConfig) error {
	if cfg == nil || cfg.Version != processRetryBatchVersion || len(cfg.Tests) == 0 || len(cfg.Tests) > processRetryBatchMaxTests ||
		cfg.AttemptToFixRetries < 0 || cfg.AttemptToFixRetries > processRetryBatchMaxTests {
		return errors.New("invalid process retry batch manifest")
	}
	seen := make(map[string]struct{}, len(cfg.Tests))
	for _, test := range cfg.Tests {
		if strings.TrimSpace(test.TestName) == "" {
			return errors.New("invalid process retry batch test")
		}
		if _, duplicate := seen[test.TestName]; duplicate {
			return errors.New("duplicate process retry batch test")
		}
		seen[test.TestName] = struct{}{}
		seenDirectives := make(map[string]struct{}, len(test.DisabledSubtests)+len(test.AttemptToFixSubtests))
		for _, names := range [][]string{test.DisabledSubtests, test.AttemptToFixSubtests} {
			for _, name := range names {
				if !strings.HasPrefix(name, test.TestName+"/") {
					return errors.New("invalid process retry managed subtest")
				}
				if _, duplicate := seenDirectives[name]; duplicate {
					return errors.New("duplicate process retry managed subtest")
				}
				seenDirectives[name] = struct{}{}
			}
		}
		seenQuarantined := make(map[string]struct{}, len(test.QuarantinedSubtests))
		for _, name := range test.QuarantinedSubtests {
			if !strings.HasPrefix(name, test.TestName+"/") {
				return errors.New("invalid process retry quarantined subtest")
			}
			if _, duplicate := seenQuarantined[name]; duplicate {
				return errors.New("duplicate process retry quarantined subtest")
			}
			seenQuarantined[name] = struct{}{}
		}
		seenITR := make(map[string]struct{}, len(test.ITRSubtests))
		for _, subtest := range test.ITRSubtests {
			if !strings.HasPrefix(subtest.TestName, test.TestName+"/") {
				return errors.New("invalid process retry ITR subtest")
			}
			if _, duplicate := seenITR[subtest.TestName]; duplicate {
				return errors.New("duplicate process retry ITR subtest")
			}
			seenITR[subtest.TestName] = struct{}{}
		}
	}
	return nil
}

func processRetryTestManagementSubtests(identity testIdentity, modules *net.TestManagementTestsResponseDataModules) (disabled, quarantined, attemptToFix []string) {
	if modules == nil {
		return nil, nil, nil
	}
	module, ok := modules.Modules[identity.ModuleName]
	if !ok {
		return nil, nil, nil
	}
	suite, ok := module.Suites[identity.SuiteName]
	if !ok {
		return nil, nil, nil
	}
	prefix := identity.FullName + "/"
	for name, test := range suite.Tests {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if test.Properties.AttemptToFix {
			attemptToFix = append(attemptToFix, name)
		} else if test.Properties.Disabled {
			disabled = append(disabled, name)
		}
		if test.Properties.Quarantined {
			quarantined = append(quarantined, name)
		}
	}
	slices.Sort(disabled)
	slices.Sort(quarantined)
	slices.Sort(attemptToFix)
	return disabled, quarantined, attemptToFix
}

func processRetryAttemptToFixRetries() int {
	settings := integrations.GetSettings()
	if settings == nil || !settings.SubtestFeaturesEnabled {
		return 0
	}
	return max(settings.TestManagement.AttemptToFixRetries, 0)
}

func processRetryITRSubtests(identity testIdentity, state *itrState) []processRetrySubtestITRConfig {
	if state == nil || !state.testsSkippingEnabled() || state.response == nil {
		return nil
	}
	suite := state.response.Skippables[identity.SuiteName]
	prefix := identity.FullName + "/"
	configs := make([]processRetrySubtestITRConfig, 0)
	for name, candidates := range suite {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		config := processRetrySubtestITRConfig{TestName: name}
		matched := false
		for _, candidate := range candidates {
			if strings.TrimSpace(candidate.Parameters) != "" {
				continue
			}
			matched = true
			config.MissingLineCodeCoverage = config.MissingLineCodeCoverage || candidate.MissingLineCodeCoverage
		}
		if !matched {
			continue
		}
		configs = append(configs, config)
	}
	slices.SortFunc(configs, func(a, b processRetrySubtestITRConfig) int {
		return strings.Compare(a.TestName, b.TestName)
	})
	return configs
}

type deferredProcessRetryBatchRunner func(context.Context, []*deferredProcessRetryGroup) map[*deferredProcessRetryGroup]processRetryAttemptResult

type deferredProcessRetryBatchOnceRunner func(
	context.Context,
	[]*deferredProcessRetryGroup,
) (map[*deferredProcessRetryGroup]processRetryAttemptResult, map[*deferredProcessRetryGroup]processRetryAttemptResult)

type nativeScheduledProcessRetryBatch struct {
	rootCfg            processRetryChildConfig
	batch              *processRetryBatchConfig
	resultRoot         string
	ctx                context.Context
	done               chan struct{}
	cancel             context.CancelFunc
	attempt            processRetryAttemptResult
	signal             func() error
	processSlotRelease <-chan processRetryLimiterRelease
}

func (c *processRetryCoordinator) registerNativeScheduledTest(identity testIdentity) {
	if c == nil || identity.FullName == "" || len(identity.Segments) != 1 {
		return
	}
	var testManagementData *net.TestManagementTestsResponseDataModules
	if settings := integrations.GetSettings(); settings != nil && settings.SubtestFeaturesEnabled {
		testManagementData = integrations.GetTestManagementTestsData()
	}
	disabled, quarantined, attemptToFix := processRetryTestManagementSubtests(identity, testManagementData)
	spec := processRetryBatchTestConfig{
		TestName:             identity.FullName,
		DisabledSubtests:     disabled,
		QuarantinedSubtests:  quarantined,
		AttemptToFixSubtests: attemptToFix,
		ITRSubtests:          processRetryITRSubtests(identity, currentITRState()),
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != processRetryCoordinatorAccepting {
		return
	}
	if c.nativeTestIndex == nil {
		c.nativeTestIndex = make(map[string]int)
	}
	if _, exists := c.nativeTestIndex[identity.FullName]; exists {
		return
	}
	c.nativeTestIndex[identity.FullName] = len(c.nativeTests)
	c.nativeTests = append(c.nativeTests, spec)
}

func (c *processRetryCoordinator) startNativeScheduledBatch(group *deferredProcessRetryGroup) (*nativeScheduledProcessRetryBatch, error) {
	if c == nil || group == nil {
		return nil, errors.New("missing native process retry batch")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	registeredIndex, found := c.nativeTestIndex[group.identity.FullName]
	if !found || registeredIndex >= len(c.nativeTests) {
		return nil, errors.New("native process retry test was not registered")
	}
	if batch := c.nativeBatches[group.invocationOrdinal]; batch != nil {
		return batch, nil
	}
	tempDir, err := os.MkdirTemp("", "dd-process-retry-native-*")
	if err != nil {
		return nil, err
	}
	batchConfig := &processRetryBatchConfig{
		Version:                processRetryBatchVersion,
		Tests:                  []processRetryBatchTestConfig{c.nativeTests[registeredIndex]},
		AttemptToFixRetries:    processRetryAttemptToFixRetries(),
		CollectPerTestCoverage: coverage.CanCollectPerTestCoverage(),
		PreserveNativeSchedule: true,
	}
	if state := currentITRState(); state != nil {
		batchConfig.ITRCoverageActive = state.coverageActive
		batchConfig.ImpactedTestsEnabled = state.settings != nil && state.settings.ImpactedTestsEnabled
	}
	rootCfg := processRetryChildConfig{
		TestName:          processRetryBatchTestName,
		Attempt:           1,
		RetryReason:       processRetryBatchReason,
		MRunEpoch:         group.mRunEpoch,
		InvocationOrdinal: group.invocationOrdinal,
		Batch:             batchConfig,
		tempDir:           tempDir,
	}
	ctx, cancel := context.WithCancel(context.Background())
	resultRoot := filepath.Join(tempDir, "result.json")
	processSlotRelease := make(chan processRetryLimiterRelease, 1)
	rootCfg.processSlotRelease = processSlotRelease
	batch := &nativeScheduledProcessRetryBatch{
		rootCfg:            rootCfg,
		batch:              batchConfig,
		resultRoot:         resultRoot,
		ctx:                ctx,
		done:               make(chan struct{}),
		cancel:             cancel,
		processSlotRelease: processSlotRelease,
		signal: sync.OnceValue(func() error {
			return writeCurrentProcessRetryBatchInvocationState(processRetryBatchEnumerationPath(resultRoot))
		}),
	}
	if c.nativeBatches == nil {
		c.nativeBatches = make(map[uint64]*nativeScheduledProcessRetryBatch)
	}
	c.nativeBatches[group.invocationOrdinal] = batch
	go func() {
		defer close(batch.done)
		defer cancel()
		batch.attempt = runProcessRetryAttemptWithBaselineAndShutdown(
			ctx,
			rootCfg,
			group.parentDeadline,
			group.parentDeadlineOK,
			group.launchBaseline,
			group.shutdown(),
		)
	}()
	return batch, nil
}

func (c *processRetryCoordinator) waitNativeScheduledFirstAttempt(group *deferredProcessRetryGroup, t *testing.T) processRetryAttemptResult {
	batch, err := c.startNativeScheduledBatch(group)
	if err != nil {
		return failedNativeScheduledAttempt(err)
	}
	index := 0
	if err := writeProcessRetryBatchInvocationState(processRetryBatchGatePath(batch.resultRoot, index), group.launchBaseline); err != nil {
		return failedNativeScheduledAttempt(err)
	}
	expectedRoot := batch.rootCfg
	expectedRoot.ResultPath = batch.resultRoot
	expectedRoot.ParentDeadlineOK = group.parentDeadlineOK
	expected := processRetryBatchChildConfig(expectedRoot, index, batch.batch.Tests[index])
	parallelPath := processRetryBatchParallelPath(batch.resultRoot, index)
	parallel := false
	readAttempt := func() (processRetryAttemptResult, error) {
		result, timingOK, readErr := readProcessRetryResult(expected.ResultPath, expected)
		if readErr != nil {
			return processRetryAttemptResult{}, readErr
		}
		if result.Status != processRetryStatusNotRun && !result.RootParallel {
			state, stateErr := readProcessRetryBatchInvocationState(processRetryBatchFinalStatePath(expected.ResultPath))
			if stateErr == nil {
				stateErr = applyProcessRetryBatchFinalState(state)
			}
			if stateErr != nil {
				return failedNativeScheduledAttempt(stateErr), nil
			}
		}
		attempt := completedProcessRetryAttempt(result)
		if timingOK {
			attempt.StartTime = time.Unix(0, result.StartUnixNano)
			attempt.FinishTime = time.Unix(0, result.FinishUnixNano)
		}
		return attempt, nil
	}
	ticker := time.NewTicker(processRetryBatchPollInterval)
	defer ticker.Stop()
	for {
		if !parallel {
			if attempt, readErr := readAttempt(); readErr == nil {
				// Wait for the child M.Run coverage report before the parent test can finish.
				<-batch.done
				return attempt
			}
		}
		if !parallel {
			if _, statErr := os.Stat(parallelPath); statErr == nil {
				parallel = true
				// Mirror mutations made before t.Parallel before the parent continues enumeration.
				state, stateErr := readProcessRetryBatchInvocationState(parallelPath)
				if stateErr == nil {
					stateErr = applyProcessRetryBatchFinalState(state)
				}
				if stateErr != nil {
					return failedNativeScheduledAttempt(stateErr)
				}
				// The child is paused inside t.Parallel while both native schedulers
				// enumerate. Reacquire admission before opening the bridge so the test
				// body remains subject to the configured process concurrency limit.
				(<-batch.processSlotRelease)()
				if err := transitionNativeScheduledTestToParallel(t); err != nil {
					return failedNativeScheduledAttempt(err)
				}
				limiterResult := acquireNativeScheduledProcessSlot(batch, group)
				if limiterResult.Cause != processRetryLimiterAcquired {
					if errors.Is(limiterResult.Err, context.Canceled) {
						<-batch.done
						return nativeScheduledBatchAttempt(batch)
					}
					select {
					case <-batch.done:
						return nativeScheduledBatchAttempt(batch)
					default:
						return failedNativeScheduledAttempt(limiterResult.Err)
					}
				}
				defer limiterResult.Release()
				if err := batch.signal(); err != nil {
					return failedNativeScheduledAttempt(err)
				}
			}
		}
		select {
		case <-batch.done:
			if attempt, readErr := readAttempt(); readErr == nil {
				return attempt
			} else if errors.Is(readErr, errProcessRetryResultMissing) && deferredProcessRetryGlobalStopReason(batch.attempt) == "" {
				now := time.Now()
				return processRetryAttemptResult{Err: readErr, ExitCode: processRetryExitCodeUnset, StartTime: now, FinishTime: now}
			}
			return nativeScheduledBatchAttempt(batch)
		case <-ticker.C:
		case <-c.shutdown:
			now := time.Now()
			return processRetryAttemptResult{SetupFailure: true, Err: errProcessRetryShutdown, ExitCode: processRetryExitCodeUnset, StartTime: now, FinishTime: now}
		}
	}
}

func nativeScheduledBatchAttempt(batch *nativeScheduledProcessRetryBatch) processRetryAttemptResult {
	attempt := batch.attempt
	attempt.Cleanup = nil
	return attempt
}

func acquireNativeScheduledProcessSlot(batch *nativeScheduledProcessRetryBatch, group *deferredProcessRetryGroup) processRetryLimiterAcquireResult {
	return getProcessRetryLimiter().acquireWithShutdownLimit(
		batch.ctx,
		nil,
		group.shutdown(),
		int(processRetryParallelMaxConcurrencyForBaseline(group.launchBaseline)),
	)
}

func transitionNativeScheduledTestToParallel(t *testing.T) error {
	// Per-test coverage temporarily replaces the shared parent barrier while
	// serial tests run. Capture it before releasing enumeration so the parallel
	// transition cannot race with that replacement.
	layout, reason := getRetryAttemptLayout()
	if reason != "" {
		return errors.New(reason)
	}
	base := commonBaseForTest(t, layout)
	parentBase := pointerWord(base, layout.common.parent)
	if base == nil || parentBase == nil {
		return errors.New("testing_t_parent_layout_unsupported")
	}
	barrier := *fieldPtr[chan bool](parentBase, layout.common.barrier)
	if barrier == nil {
		return errors.New("testing_t_parent_barrier_unavailable")
	}
	group := retryAttemptGroup{original: t, layout: layout, originalParentBarrier: barrier}
	group.transitionOriginalToParallel()
	testingTestStateWaitParallel(getTestState(t))
	initializeRetryAttemptStart(base, layout.common.start.unsafeField)
	return nil
}

func failedNativeScheduledAttempt(err error) processRetryAttemptResult {
	now := time.Now()
	return processRetryAttemptResult{SetupFailure: true, Err: err, ExitCode: processRetryExitCodeUnset, StartTime: now, FinishTime: now}
}

func (c *processRetryCoordinator) nativeScheduledBatchResult(invocationOrdinal uint64) (processRetryAttemptResult, bool) {
	if c == nil {
		return processRetryAttemptResult{}, false
	}
	c.mu.Lock()
	batch := c.nativeBatches[invocationOrdinal]
	c.mu.Unlock()
	if batch == nil {
		return processRetryAttemptResult{}, false
	}
	skipErr := skipUninvokedNativeScheduledTests(batch)
	if skipErr != nil {
		batch.cancel()
	}
	<-batch.done
	mergePendingProcessRetryTestLog(&batch.attempt)
	if batch.attempt.OutputTail != "" && !batch.attempt.outputStreamed {
		_, _ = io.WriteString(os.Stdout, batch.attempt.OutputTail)
	}
	if err := coverage.MergeProcessCoverageProfile(processRetryBatchCoveragePath(batch.resultRoot)); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Debug("civisibility: failed to merge isolated first-attempt coverage: %s", err.Error())
	}
	if skipErr != nil {
		batch.attempt.SetupFailure = true
		batch.attempt.Err = errors.Join(batch.attempt.Err, skipErr)
	}
	return batch.attempt, true
}

func skipUninvokedNativeScheduledTests(batch *nativeScheduledProcessRetryBatch) error {
	if batch == nil || batch.batch == nil {
		return nil
	}
	// Filters and -failfast can leave registered tests uninvoked; release their
	// child gates without executing their bodies before waiting for the batch.
	for index := range batch.batch.Tests {
		if _, err := os.Stat(processRetryBatchGatePath(batch.resultRoot, index)); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.WriteFile(processRetryBatchSkipPath(batch.resultRoot, index), nil, processRetryBatchManifestMode); err != nil {
			return err
		}
	}
	return nil
}

func (c *processRetryCoordinator) cleanupNativeScheduledBatch(invocationOrdinal uint64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	batch := c.nativeBatches[invocationOrdinal]
	delete(c.nativeBatches, invocationOrdinal)
	c.mu.Unlock()
	if batch == nil {
		return
	}
	batch.cancel()
	if batch.attempt.Cleanup != nil {
		batch.attempt.Cleanup()
	}
}

func runDeferredQuarantinedProcessRetryBatch(
	ctx context.Context,
	groups []*deferredProcessRetryGroup,
) map[*deferredProcessRetryGroup]processRetryAttemptResult {
	return runDeferredQuarantinedProcessRetryBatchWithRunner(ctx, groups, runDeferredQuarantinedProcessRetryBatchOnce)
}

func runDeferredNativeScheduledProcessRetryBatch(
	ctx context.Context,
	groups []*deferredProcessRetryGroup,
) map[*deferredProcessRetryGroup]processRetryAttemptResult {
	results := make(map[*deferredProcessRetryGroup]processRetryAttemptResult, len(groups))
	for _, group := range groups {
		maps.Copy(results, runDeferredQuarantinedProcessRetryBatchWithRunner(
			ctx,
			[]*deferredProcessRetryGroup{group},
			runDeferredNativeScheduledProcessRetryBatchOnce,
		))
	}
	return results
}

func runDeferredQuarantinedProcessRetryBatchWithRunner(
	ctx context.Context,
	groups []*deferredProcessRetryGroup,
	runOnce deferredProcessRetryBatchOnceRunner,
) map[*deferredProcessRetryGroup]processRetryAttemptResult {
	results := make(map[*deferredProcessRetryGroup]processRetryAttemptResult, len(groups))
	pending := append([]*deferredProcessRetryGroup(nil), groups...)
	for len(pending) > 0 {
		completed, missing := runOnce(ctx, pending)
		maps.Copy(results, completed)
		if len(missing) == 0 {
			break
		}
		if ctx.Err() != nil {
			maps.Copy(results, missing)
			break
		}
		globalStop := false
		for _, attempt := range missing {
			if deferredProcessRetryGlobalStopReason(attempt) != "" {
				globalStop = true
				break
			}
		}
		if globalStop {
			maps.Copy(results, missing)
			break
		}
		if len(completed) > 0 {
			next := pending[:0]
			for _, group := range pending {
				if _, stillMissing := missing[group]; stillMissing {
					next = append(next, group)
				}
			}
			pending = next
			continue
		}
		for _, group := range pending {
			oneCompleted, oneMissing := runOnce(ctx, []*deferredProcessRetryGroup{group})
			if result, ok := oneCompleted[group]; ok {
				results[group] = result
				continue
			}
			if result, ok := oneMissing[group]; ok {
				results[group] = result
			}
		}
		break
	}
	return results
}

func runDeferredQuarantinedProcessRetryBatchOnce(
	ctx context.Context,
	groups []*deferredProcessRetryGroup,
) (map[*deferredProcessRetryGroup]processRetryAttemptResult, map[*deferredProcessRetryGroup]processRetryAttemptResult) {
	return runDeferredProcessRetryBatchOnce(ctx, groups, false)
}

func runDeferredNativeScheduledProcessRetryBatchOnce(
	ctx context.Context,
	groups []*deferredProcessRetryGroup,
) (map[*deferredProcessRetryGroup]processRetryAttemptResult, map[*deferredProcessRetryGroup]processRetryAttemptResult) {
	return runDeferredProcessRetryBatchOnce(ctx, groups, true)
}

func runDeferredProcessRetryBatchOnce(
	ctx context.Context,
	groups []*deferredProcessRetryGroup,
	preserveNativeSchedule bool,
) (map[*deferredProcessRetryGroup]processRetryAttemptResult, map[*deferredProcessRetryGroup]processRetryAttemptResult) {
	completed := make(map[*deferredProcessRetryGroup]processRetryAttemptResult, len(groups))
	missing := make(map[*deferredProcessRetryGroup]processRetryAttemptResult, len(groups))
	if len(groups) == 0 {
		return completed, missing
	}
	fail := func(err error) map[*deferredProcessRetryGroup]processRetryAttemptResult {
		attempt := failedNativeScheduledAttempt(err)
		for _, group := range groups {
			missing[group] = attempt
		}
		return missing
	}
	if preserveNativeSchedule && len(groups) != 1 {
		return completed, fail(errors.New("native process retry batch must contain one test"))
	}
	first := groups[0]
	batch := &processRetryBatchConfig{
		Version:                processRetryBatchVersion,
		Tests:                  make([]processRetryBatchTestConfig, 0, len(groups)),
		AttemptToFixRetries:    processRetryAttemptToFixRetries(),
		CollectPerTestCoverage: coverage.CanCollectPerTestCoverage(),
		PreserveNativeSchedule: preserveNativeSchedule,
	}
	if preserveNativeSchedule {
		if state := currentITRState(); state != nil {
			batch.ITRCoverageActive = state.coverageActive
			batch.ImpactedTestsEnabled = state.settings != nil && state.settings.ImpactedTestsEnabled
		}
	}
	var testManagementData *net.TestManagementTestsResponseDataModules
	if settings := integrations.GetSettings(); settings != nil && settings.SubtestFeaturesEnabled {
		testManagementData = integrations.GetTestManagementTestsData()
	}
	for _, group := range groups {
		disabled, quarantined, attemptToFix := processRetryTestManagementSubtests(group.identity, testManagementData)
		spec := processRetryBatchTestConfig{
			TestName:             group.identity.FullName,
			InvocationOrdinal:    group.invocationOrdinal,
			DisabledSubtests:     disabled,
			QuarantinedSubtests:  quarantined,
			AttemptToFixSubtests: attemptToFix,
		}
		if preserveNativeSchedule {
			spec.ITRSubtests = processRetryITRSubtests(group.identity, currentITRState())
		}
		batch.Tests = append(batch.Tests, spec)
	}
	parentDeadline, parentDeadlineOK := earliestDeferredProcessRetryDeadline(groups)
	rootCfg := processRetryChildConfig{
		TestName:          processRetryBatchTestName,
		Attempt:           1,
		RetryReason:       processRetryBatchReason,
		MRunEpoch:         first.mRunEpoch,
		InvocationOrdinal: first.invocationOrdinal,
		Batch:             batch,
	}
	if preserveNativeSchedule {
		tempDir, err := os.MkdirTemp("", "dd-process-retry-native-*")
		if err != nil {
			return completed, fail(err)
		}
		defer func() { _ = os.RemoveAll(tempDir) }()
		rootCfg.tempDir = tempDir
		resultRoot := filepath.Join(tempDir, "result.json")
		for index, group := range groups {
			if err := writeProcessRetryBatchInvocationState(processRetryBatchGatePath(resultRoot, index), group.launchBaseline); err != nil {
				return completed, fail(err)
			}
		}
		if err := writeCurrentProcessRetryBatchInvocationState(processRetryBatchEnumerationPath(resultRoot)); err != nil {
			return completed, fail(err)
		}
	}
	processAttempt := runProcessRetryAttemptWithBaselineAndShutdown(
		ctx,
		rootCfg,
		parentDeadline,
		parentDeadlineOK,
		first.launchBaseline,
		first.shutdown(),
	)
	if processAttempt.OutputTail != "" && !processAttempt.outputStreamed {
		_, _ = io.WriteString(os.Stdout, processAttempt.OutputTail)
	}
	mergePendingProcessRetryTestLog(&processAttempt)
	if processAttempt.TempDir == "" {
		for _, group := range groups {
			attempt := processAttempt
			attempt.Cleanup = nil
			missing[group] = attempt
		}
		if processAttempt.Cleanup != nil {
			processAttempt.Cleanup()
		}
		return completed, missing
	}
	resultRoot := filepath.Join(processAttempt.TempDir, "result.json")
	if err := coverage.MergeProcessCoverageProfile(processRetryBatchCoveragePath(resultRoot)); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Debug("civisibility: failed to merge isolated first-attempt coverage: %s", err.Error())
	}
	expectedRoot := rootCfg
	expectedRoot.ResultPath = resultRoot
	expectedRoot.ParentDeadlineOK = parentDeadlineOK
	for index, group := range groups {
		expected := processRetryBatchChildConfig(expectedRoot, index, batch.Tests[index])
		result, timingOK, err := readProcessRetryResult(expected.ResultPath, expected)
		if err != nil {
			attempt := processAttempt
			attempt.Cleanup = nil
			missing[group] = attempt
			continue
		}
		attempt := completedProcessRetryAttempt(result)
		if timingOK {
			attempt.StartTime = time.Unix(0, result.StartUnixNano)
			attempt.FinishTime = time.Unix(0, result.FinishUnixNano)
		} else {
			attempt.StartTime = processAttempt.StartTime
			attempt.FinishTime = processAttempt.FinishTime
		}
		completed[group] = attempt
	}
	preserveProcessRetryBatchFailure(processAttempt, groups, completed)
	if processAttempt.Cleanup != nil {
		processAttempt.Cleanup()
	}
	return completed, missing
}

func preserveProcessRetryBatchFailure(
	processAttempt processRetryAttemptResult,
	groups []*deferredProcessRetryGroup,
	completed map[*deferredProcessRetryGroup]processRetryAttemptResult,
) {
	if errors.Is(processAttempt.Err, errProcessRetryTestLogMerge) {
		for _, group := range groups {
			attempt, ok := completed[group]
			if !ok {
				continue
			}
			attempt.SetupFailure = true
			attempt.Err = errors.Join(attempt.Err, processAttempt.Err)
			completed[group] = attempt
			return
		}
	}
	if !processAttempt.ExitStatusObserved || processAttempt.ExitCode == 0 {
		return
	}
	for _, attempt := range completed {
		if effectiveProcessRetryStatus(attempt, false).Failed {
			return
		}
	}
	for _, group := range groups {
		attempt, ok := completed[group]
		if !ok {
			continue
		}
		attempt.ExitCode = processAttempt.ExitCode
		attempt.ExitStatusObserved = true
		attempt.Err = errors.Join(processAttempt.Err, errProcessRetryBatchFailed)
		completed[group] = attempt
		return
	}
}

func earliestDeferredProcessRetryDeadline(groups []*deferredProcessRetryGroup) (time.Time, bool) {
	var earliest time.Time
	found := false
	for _, group := range groups {
		if group == nil || !group.parentDeadlineOK {
			continue
		}
		if !found || group.parentDeadline.Before(earliest) {
			earliest = group.parentDeadline
			found = true
		}
	}
	return earliest, found
}
