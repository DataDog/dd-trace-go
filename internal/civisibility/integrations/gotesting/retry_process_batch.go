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
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/coverage"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	processRetryBatchVersion      = 1
	processRetryBatchTestName     = "__dd_quarantined_race_batch__"
	processRetryBatchReason       = "quarantined_race"
	processRetryBatchMaxTests     = 100_000
	processRetryBatchMaxBytes     = 16 * 1024 * 1024
	processRetryBatchManifestMode = 0o600
)

type processRetryBatchTestConfig struct {
	TestName          string `json:"test_name"`
	InvocationOrdinal uint64 `json:"invocation_ordinal,omitempty"`
}

type processRetryBatchConfig struct {
	Version                int                           `json:"version"`
	Tests                  []processRetryBatchTestConfig `json:"tests"`
	CollectPerTestCoverage bool                          `json:"collect_per_test_coverage,omitempty"`
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

func processRetryBatchChildConfig(root processRetryChildConfig, index int, test processRetryBatchTestConfig) processRetryChildConfig {
	return processRetryChildConfig{
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

type deferredProcessRetryBatchRunner func(context.Context, []*deferredProcessRetryGroup) map[*deferredProcessRetryGroup]processRetryAttemptResult

type deferredProcessRetryBatchOnceRunner func(
	context.Context,
	[]*deferredProcessRetryGroup,
) (map[*deferredProcessRetryGroup]processRetryAttemptResult, map[*deferredProcessRetryGroup]processRetryAttemptResult)

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
	for _, group := range groups {
		batch.Tests = append(batch.Tests, processRetryBatchTestConfig{
			TestName:          group.identity.FullName,
			InvocationOrdinal: group.invocationOrdinal,
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
	replayDeferredProcessRetryBatchOutput(os.Stdout, processAttempt)
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
	if processAttempt.Cleanup != nil {
		processAttempt.Cleanup()
	}
	return completed, missing
}

func replayDeferredProcessRetryBatchOutput(writer io.Writer, attempt processRetryAttemptResult) {
	if writer != nil && attempt.OutputTail != "" {
		_, _ = io.WriteString(writer, attempt.OutputTail)
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
