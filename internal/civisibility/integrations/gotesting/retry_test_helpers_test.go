// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func (c *retryAttemptOutputCapture) snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.output...)
}

func newRetryAttemptRoot(original *testing.T) (*retryAttemptRoot, string) {
	group, reason := newRetryAttemptGroup(original)
	if reason != "" {
		return nil, reason
	}
	return newRetryAttemptRootInGroup(group)
}

func runFreshRetryAttempt(original *testing.T, target func(*testing.T)) (*retryAttemptRoot, retryAttemptResult, string) {
	group, reason := newRetryAttemptGroup(original)
	if reason != "" {
		return nil, retryAttemptResult{}, reason
	}
	return runFreshRetryAttemptInGroup(group, target)
}

func runFreshRetryAttemptInGroup(group *retryAttemptGroup, target func(*testing.T)) (*retryAttemptRoot, retryAttemptResult, string) {
	return runFreshRetryAttemptInGroupWithCallbacks(group, nil, target, nil)
}

func processRetryMaxConcurrencyFromEnv(defaultValue int) int {
	if defaultValue < 1 {
		defaultValue = 1
	}
	if configured, ok := processRetryConfiguredMaxConcurrencyFromEnv(); ok {
		return configured
	}
	return defaultValue
}

func processRetryDefaultMaxConcurrency() int {
	return processRetryDefaultMaxConcurrencyForCPU(runtime.GOMAXPROCS(0))
}

func disableProcessRetryLaunches() {
	processRetryLaunchGate.mu.Lock()
	processRetryLaunchGate.disabled.Store(true)
	processRetryLaunchGate.notifyLocked()
	processRetryLaunchGate.mu.Unlock()
}

func registerActiveProcessRetryChild(cmd *exec.Cmd, hooks processRetryRunnerHooks) {
	if cmd == nil {
		return
	}
	processRetryLaunchGate.mu.Lock()
	registerActiveProcessRetryChildLocked(cmd, hooks)
	processRetryLaunchGate.mu.Unlock()
}

func (l *processRetryLimiter) acquire(ctx context.Context, parentDeadlineHardCap <-chan time.Time) processRetryLimiterAcquireResult {
	return l.acquireWithShutdownLimit(
		ctx,
		parentDeadlineHardCap,
		nil,
		processRetryMaxConcurrencyFromEnv(processRetryDefaultMaxConcurrency()),
	)
}

func (l *processRetryLimiter) acquireWithShutdown(
	ctx context.Context,
	parentDeadlineHardCap <-chan time.Time,
	shutdown <-chan struct{},
) processRetryLimiterAcquireResult {
	return l.acquireWithShutdownLimit(
		ctx,
		parentDeadlineHardCap,
		shutdown,
		processRetryMaxConcurrencyFromEnv(processRetryDefaultMaxConcurrency()),
	)
}

func runProcessRetryAttempt(ctx context.Context, cfg processRetryChildConfig, parentDeadline time.Time, parentDeadlineOK bool) processRetryAttemptResult {
	return runProcessRetryAttemptWithBaseline(ctx, cfg, parentDeadline, parentDeadlineOK, captureProcessRetryLaunchBaselineForTesting())
}

func captureProcessRetryLaunchBaselineForTesting() *processRetryLaunchBaseline {
	hooks := currentProcessRetryRunnerHooks()
	startup := captureProcessRetryStartupSnapshot(hooks.workingDirectory, hooks.args, hooks.environ)
	return captureProcessRetryLaunchBaselineFromTemplate(captureProcessRetryLaunchTemplateFromStartup(startup))
}

func runProcessRetryAttemptWithBaseline(
	ctx context.Context,
	cfg processRetryChildConfig,
	parentDeadline time.Time,
	parentDeadlineOK bool,
	baseline *processRetryLaunchBaseline,
) processRetryAttemptResult {
	return runProcessRetryAttemptWithBaselineAndShutdown(ctx, cfg, parentDeadline, parentDeadlineOK, baseline, nil, nil)
}

func waitProcessRetryChild(
	ctx context.Context,
	hooks processRetryRunnerHooks,
	cmd *exec.Cmd,
	waitCh <-chan error,
	timeoutTimer processRetryTimer,
	attempt *processRetryAttemptResult,
) error {
	teardownPhase := &processRetryReapPhase{}
	containmentLost := false
	markContainmentLost := func(err error) {
		containmentLost = true
		attempt.ContainmentLost = true
		attempt.Err = errors.Join(attempt.Err, errProcessRetryContainmentLost, err)
	}
	err := waitProcessRetryChildWithTeardown(ctx, nil, hooks, cmd, waitCh, nil, timeoutTimer, attempt, teardownPhase, markContainmentLost)
	teardownPhase.finish(containmentLost || attempt.Unreaped)
	return err
}

func waitForProcessRetryReapAfterKill(hooks processRetryRunnerHooks, waitCh <-chan error, attempt *processRetryAttemptResult) error {
	reapPhase := beginProcessRetryReapPhase()
	err := waitForProcessRetryReapAfterKillWithPhase(hooks, waitCh, attempt, reapPhase)
	reapPhase.finish(attempt != nil && attempt.Unreaped)
	return err
}

func buildProcessRetryArgs(originalArgs []string, testName string, currentCPU int, childTestingTimeout time.Duration) ([]string, bool, string) {
	return buildProcessRetryArgsFromSnapshot(captureProcessRetryArgsSnapshot(originalArgs), testName, currentCPU, childTestingTimeout)
}

func (c *processRetryCoordinator) stateChange() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.changed
}

func (c *processRetryCoordinator) stateSnapshot() processRetryCoordinatorState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}
