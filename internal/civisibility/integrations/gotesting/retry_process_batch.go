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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/coverage"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	processRetryBatchVersion      = 1
	processRetryBatchTestName     = "__dd_quarantined_race_batch__"
	processRetryBatchReason       = "quarantined_race"
	processRetryBatchMaxTests     = 100_000
	processRetryBatchMaxBytes     = 16 * 1024 * 1024
	processRetryBatchManifestMode = 0o600
	processRetryBatchPollInterval = 5 * time.Millisecond
)

var errProcessRetryBatchFailed = errors.New("process retry batch failed")

type processRetryBatchTestConfig struct {
	TestName          string   `json:"test_name"`
	InvocationOrdinal uint64   `json:"invocation_ordinal,omitempty"`
	DisabledSubtests  []string `json:"disabled_subtests,omitempty"`
}

type processRetryBatchConfig struct {
	Version                int                           `json:"version"`
	Tests                  []processRetryBatchTestConfig `json:"tests"`
	CollectPerTestCoverage bool                          `json:"collect_per_test_coverage,omitempty"`
	PreserveNativeSchedule bool                          `json:"preserve_native_schedule,omitempty"`
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

func processRetryBatchParallelPath(resultPath string, index int) string {
	return filepath.Join(filepath.Dir(resultPath), fmt.Sprintf("batch-parallel-%06d", index))
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
	if root.Batch != nil && root.Batch.PreserveNativeSchedule {
		// The native parent owns invocation identity; the child manifest is
		// registered before those ordinals exist and is matched by batch index.
		child.MRunEpoch = 0
		child.InvocationOrdinal = 0
		child.nativeGatePath = processRetryBatchGatePath(root.ResultPath, index)
		child.nativeParallelPath = processRetryBatchParallelPath(root.ResultPath, index)
	}
	return child
}

func waitForProcessRetryBatchGate(path string, deadline time.Time, deadlineOK bool) error {
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if deadlineOK && !time.Now().Before(deadline) {
			return context.DeadlineExceeded
		}
		time.Sleep(processRetryBatchPollInterval)
	}
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
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, processRetryBatchMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > processRetryBatchMaxBytes {
		return nil, errors.New("process retry batch manifest too large")
	}
	var cfg processRetryBatchConfig
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("process retry batch manifest has trailing data")
	}
	if err := validateProcessRetryBatchConfig(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func validateProcessRetryBatchConfig(cfg *processRetryBatchConfig) error {
	if cfg == nil || cfg.Version != processRetryBatchVersion || len(cfg.Tests) == 0 || len(cfg.Tests) > processRetryBatchMaxTests {
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
	}
	return nil
}

func disabledProcessRetrySubtests(identity testIdentity, modules *net.TestManagementTestsResponseDataModules) []string {
	if modules == nil {
		return nil
	}
	module, ok := modules.Modules[identity.ModuleName]
	if !ok {
		return nil
	}
	suite, ok := module.Suites[identity.SuiteName]
	if !ok {
		return nil
	}
	prefix := identity.FullName + "/"
	var disabled []string
	for name, test := range suite.Tests {
		if strings.HasPrefix(name, prefix) && test.Properties.Disabled && !test.Properties.AttemptToFix {
			disabled = append(disabled, name)
		}
	}
	slices.Sort(disabled)
	return disabled
}

type deferredProcessRetryBatchRunner func(context.Context, []*deferredProcessRetryGroup) map[*deferredProcessRetryGroup]processRetryAttemptResult

type deferredProcessRetryBatchOnceRunner func(
	context.Context,
	[]*deferredProcessRetryGroup,
) (map[*deferredProcessRetryGroup]processRetryAttemptResult, map[*deferredProcessRetryGroup]processRetryAttemptResult)

type nativeScheduledProcessRetryBatch struct {
	rootCfg    processRetryChildConfig
	batch      *processRetryBatchConfig
	resultRoot string
	done       chan struct{}
	cancel     context.CancelFunc
	attempt    processRetryAttemptResult
}

func (c *processRetryCoordinator) registerNativeScheduledTest(identity testIdentity) {
	if c == nil || identity.FullName == "" || len(identity.Segments) != 1 {
		return
	}
	var testManagementData *net.TestManagementTestsResponseDataModules
	if settings := integrations.GetSettings(); settings != nil && settings.SubtestFeaturesEnabled {
		testManagementData = integrations.GetTestManagementTestsData()
	}
	spec := processRetryBatchTestConfig{
		TestName:         identity.FullName,
		DisabledSubtests: disabledProcessRetrySubtests(identity, testManagementData),
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

func (c *processRetryCoordinator) startNativeScheduledBatch(group *deferredProcessRetryGroup) (*nativeScheduledProcessRetryBatch, int, error) {
	if c == nil || group == nil {
		return nil, 0, errors.New("missing native process retry batch")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	index, found := c.nativeTestIndex[group.identity.FullName]
	if !found || len(c.nativeTests) == 0 {
		return nil, 0, errors.New("native process retry test was not registered")
	}
	if batch := c.nativeBatches[group.phaseID]; batch != nil {
		return batch, index, nil
	}
	tempDir, err := os.MkdirTemp("", "dd-process-retry-native-*")
	if err != nil {
		return nil, 0, err
	}
	batchConfig := &processRetryBatchConfig{
		Version:                processRetryBatchVersion,
		Tests:                  append([]processRetryBatchTestConfig(nil), c.nativeTests...),
		CollectPerTestCoverage: coverage.CanCollectPerTestCoverage(),
		PreserveNativeSchedule: true,
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
	batch := &nativeScheduledProcessRetryBatch{
		rootCfg:    rootCfg,
		batch:      batchConfig,
		resultRoot: filepath.Join(tempDir, "result.json"),
		done:       make(chan struct{}),
		cancel:     cancel,
	}
	if c.nativeBatches == nil {
		c.nativeBatches = make(map[uint64]*nativeScheduledProcessRetryBatch)
	}
	c.nativeBatches[group.phaseID] = batch
	go func() {
		batch.attempt = runProcessRetryAttemptWithBaselineAndShutdown(
			ctx,
			rootCfg,
			group.parentDeadline,
			group.parentDeadlineOK,
			group.launchBaseline,
			group.shutdown(),
		)
		close(batch.done)
	}()
	return batch, index, nil
}

func (c *processRetryCoordinator) waitNativeScheduledFirstAttempt(group *deferredProcessRetryGroup, t *testing.T) processRetryAttemptResult {
	batch, index, err := c.startNativeScheduledBatch(group)
	if err != nil {
		now := time.Now()
		return processRetryAttemptResult{SetupFailure: true, Err: err, ExitCode: processRetryExitCodeUnset, StartTime: now, FinishTime: now}
	}
	if err := os.WriteFile(processRetryBatchGatePath(batch.resultRoot, index), nil, processRetryBatchManifestMode); err != nil {
		now := time.Now()
		return processRetryAttemptResult{SetupFailure: true, Err: err, ExitCode: processRetryExitCodeUnset, StartTime: now, FinishTime: now}
	}
	expectedRoot := batch.rootCfg
	expectedRoot.ResultPath = batch.resultRoot
	expectedRoot.ParentDeadlineOK = group.parentDeadlineOK
	expected := processRetryBatchChildConfig(expectedRoot, index, batch.batch.Tests[index])
	parallelPath := processRetryBatchParallelPath(batch.resultRoot, index)
	parallel := false
	readAttempt := func() (processRetryAttemptResult, bool) {
		result, timingOK, readErr := readProcessRetryResult(expected.ResultPath, expected)
		if readErr != nil {
			return processRetryAttemptResult{}, false
		}
		attempt := completedProcessRetryAttempt(result)
		if timingOK {
			attempt.StartTime = time.Unix(0, result.StartUnixNano)
			attempt.FinishTime = time.Unix(0, result.FinishUnixNano)
		}
		return attempt, true
	}
	ticker := time.NewTicker(processRetryBatchPollInterval)
	defer ticker.Stop()
	for {
		if attempt, ok := readAttempt(); ok {
			return attempt
		}
		if !parallel {
			if _, statErr := os.Stat(parallelPath); statErr == nil {
				parallel = true
				t.Parallel()
			}
		}
		select {
		case <-batch.done:
			if attempt, ok := readAttempt(); ok {
				return attempt
			}
			attempt := batch.attempt
			attempt.Cleanup = nil
			return attempt
		case <-ticker.C:
		case <-c.shutdown:
			now := time.Now()
			return processRetryAttemptResult{SetupFailure: true, Err: errProcessRetryShutdown, ExitCode: processRetryExitCodeUnset, StartTime: now, FinishTime: now}
		}
	}
}

func (c *processRetryCoordinator) nativeScheduledBatchResult(phaseID uint64) (processRetryAttemptResult, bool) {
	if c == nil {
		return processRetryAttemptResult{}, false
	}
	c.mu.Lock()
	batch := c.nativeBatches[phaseID]
	c.mu.Unlock()
	if batch == nil {
		return processRetryAttemptResult{}, false
	}
	<-batch.done
	if batch.attempt.OutputTail != "" {
		_, _ = io.WriteString(os.Stdout, batch.attempt.OutputTail)
	}
	if err := coverage.MergeProcessCoverageProfile(processRetryBatchCoveragePath(batch.resultRoot)); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Debug("civisibility: failed to merge isolated first-attempt coverage: %s", err.Error())
	}
	return batch.attempt, true
}

func (c *processRetryCoordinator) cleanupNativeScheduledBatch(phaseID uint64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	batch := c.nativeBatches[phaseID]
	delete(c.nativeBatches, phaseID)
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
	completed := make(map[*deferredProcessRetryGroup]processRetryAttemptResult, len(groups))
	missing := make(map[*deferredProcessRetryGroup]processRetryAttemptResult, len(groups))
	if len(groups) == 0 {
		return completed, missing
	}
	first := groups[0]
	batch := &processRetryBatchConfig{
		Version:                processRetryBatchVersion,
		Tests:                  make([]processRetryBatchTestConfig, 0, len(groups)),
		CollectPerTestCoverage: coverage.CanCollectPerTestCoverage(),
	}
	var testManagementData *net.TestManagementTestsResponseDataModules
	if settings := integrations.GetSettings(); settings != nil && settings.SubtestFeaturesEnabled {
		testManagementData = integrations.GetTestManagementTestsData()
	}
	for _, group := range groups {
		batch.Tests = append(batch.Tests, processRetryBatchTestConfig{
			TestName:          group.identity.FullName,
			InvocationOrdinal: group.invocationOrdinal,
			DisabledSubtests:  disabledProcessRetrySubtests(group.identity, testManagementData),
		})
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
	processAttempt := runProcessRetryAttemptWithBaselineAndShutdown(
		ctx,
		rootCfg,
		parentDeadline,
		parentDeadlineOK,
		first.launchBaseline,
		first.shutdown(),
	)
	if processAttempt.OutputTail != "" {
		_, _ = io.WriteString(os.Stdout, processAttempt.OutputTail)
	}
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
