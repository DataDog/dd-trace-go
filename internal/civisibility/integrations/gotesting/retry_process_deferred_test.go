// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
)

type deferredProcessRetryMutablePanic struct {
	message string
}

func processRetryCoordinatorRegisteredForTesting(c *processRetryCoordinator) bool {
	processRetryCoordinatorRegistry.mu.Lock()
	defer processRetryCoordinatorRegistry.mu.Unlock()
	_, ok := processRetryCoordinatorRegistry.values[c]
	return ok
}

func (p *deferredProcessRetryMutablePanic) String() string {
	return p.message
}

func TestDeferredProcessRetryFreshObservationIsImmutablePolicyInput(t *testing.T) {
	panicStack := []byte("panic-stack")
	terminalStack := []byte("terminal-stack")
	result := retryAttemptResult{
		failed:                 true,
		duration:               3 * time.Second,
		nativeFatalRequired:    true,
		nativeFatalTraceReplay: true,
		panicData:              "panic-value",
		panicStack:             panicStack,
		terminalTrace: []retryAttemptTerminal{{
			kind:  retryAttemptTerminalBodyPanic,
			value: "terminal-value",
			stack: terminalStack,
		}},
	}

	observation := observeFreshRetryAttempt(2, result)
	panicStack[0] = 'X'
	terminalStack[0] = 'X'
	result.terminalTrace[0].value = "changed"

	require.Equal(t, 2, observation.executionIndex)
	require.True(t, observation.failed)
	require.False(t, observation.skipped)
	require.Equal(t, 3*time.Second, observation.duration)
	require.True(t, observation.stopsRetryContinuation())
	require.Equal(t, "panic-value", observation.panicMessage)
	require.Equal(t, "panic-stack", string(observation.panicStack))
	require.Equal(t, "terminal-value", observation.terminalTrace[0].value)
	require.Equal(t, "terminal-stack", string(observation.terminalTrace[0].stack))
}

func TestDeferredProcessRetryFreshObservationTerminalPolicy(t *testing.T) {
	tests := []struct {
		name   string
		result retryAttemptResult
		stop   bool
	}{
		{name: "pass"},
		{name: "ordinary failure", result: retryAttemptResult{failed: true}},
		{name: "skip", result: retryAttemptResult{skipped: true}},
		{name: "native fatal", result: retryAttemptResult{nativeFatalRequired: true}, stop: true},
		{name: "race", result: retryAttemptResult{raceDetected: true}, stop: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation := observeFreshRetryAttempt(0, test.result)
			require.Equal(t, test.stop, observation.stopsRetryContinuation())
		})
	}
}

func TestDeferredProcessRetryCoordinatorAdmissionSealLinearizes(t *testing.T) {
	coordinator := newProcessRetryCoordinatorForTesting(false)
	admission := coordinator.beginAdmission()
	require.NotNil(t, admission)

	sealed := make(chan []*deferredProcessRetryGroup, 1)
	stateChanged := coordinator.stateChange()
	go func() { sealed <- coordinator.seal() }()
	<-stateChanged
	require.Equal(t, processRetryCoordinatorSealed, coordinator.stateSnapshot())
	require.Nil(t, coordinator.beginAdmission(), "sealing must reject new admissions")

	group := &deferredProcessRetryGroup{}
	require.True(t, admission.commit(group))
	require.False(t, admission.commit(group), "an admission token must commit once")

	queue := <-sealed
	require.Equal(t, []*deferredProcessRetryGroup{group}, queue)
	require.Equal(t, uint64(1), group.id)
}

func TestDeferredProcessRetryCoordinatorAdmissionAbortIsIdempotent(t *testing.T) {
	coordinator := newProcessRetryCoordinatorForTesting(false)
	admission := coordinator.beginAdmission()
	require.NotNil(t, admission)
	require.True(t, admission.abort())
	require.False(t, admission.abort())
	require.False(t, admission.commit(&deferredProcessRetryGroup{}))
	require.Empty(t, coordinator.seal())
}

func TestDeferredProcessRetryCoordinatorTracksNativeInvocationPhases(t *testing.T) {
	coordinator := newProcessRetryCoordinatorForTesting(false)
	first := newTestIdentity("module", "suite", "TestFirst")
	second := newTestIdentity("module", "suite", "TestSecond")

	require.Equal(t, uint64(1), coordinator.observeInvocationPhase(first))
	require.Equal(t, uint64(1), coordinator.observeInvocationPhase(second))
	require.Equal(t, uint64(2), coordinator.observeInvocationPhase(first))
	require.Equal(t, uint64(2), coordinator.observeInvocationPhase(second))
	require.Zero(t, coordinator.observeInvocationPhase(newTestIdentity("module", "suite", "TestFirst/subtest")))
}

func TestDeferredProcessRetryInvocationIsCapturedBeforeExecutionMetadata(t *testing.T) {
	if !ProcessRetryContainmentSupported() {
		t.Skip("process retry containment is unavailable")
	}
	coordinator := newProcessRetryCoordinatorForTesting(false)
	counter := &atomic.Uint64{}
	fuzzGuard := &processRetryFuzzGuardSnapshot{evaluate: func() bool { return false }}
	firstOptions := &runTestWithRetryOptions{
		processRetryCoordinator:       coordinator,
		processRetryIdentity:          newTestIdentity("module", "suite", "TestFirst"),
		processRetryInvocationCounter: counter,
		processRetryFuzzGuard:         fuzzGuard,
	}
	secondOptions := &runTestWithRetryOptions{
		processRetryCoordinator:       coordinator,
		processRetryIdentity:          newTestIdentity("module", "suite", "TestSecond"),
		processRetryInvocationCounter: counter,
		processRetryFuzzGuard:         fuzzGuard,
	}

	prepareDeferredProcessRetryInvocation(&executionOptions{options: firstOptions})
	prepareDeferredProcessRetryInvocation(&executionOptions{options: secondOptions})

	require.Equal(t, uint64(1), firstOptions.processRetryPhaseID)
	require.Equal(t, uint64(1), secondOptions.processRetryPhaseID)
	require.Equal(t, uint64(1), firstOptions.processRetryInvocationOrdinal)
	require.Equal(t, uint64(2), secondOptions.processRetryInvocationOrdinal)
}

func TestDeferredProcessRetryCoordinatorDrainPublishesOneSummary(t *testing.T) {
	coordinator := newProcessRetryCoordinatorForTesting(false)
	require.True(t, registerProcessRetryCoordinator(coordinator))
	require.True(t, processRetryCoordinatorRegisteredForTesting(coordinator))

	const callers = 8
	start := make(chan struct{})
	results := make(chan processRetryCoordinatorSummary, callers)
	var ready sync.WaitGroup
	ready.Add(callers)
	for range callers {
		go func() {
			ready.Done()
			<-start
			results <- coordinator.drain(0)
		}()
	}
	ready.Wait()
	close(start)

	for range callers {
		require.Equal(t, processRetryCoordinatorSummary{}, <-results)
	}
	require.Equal(t, processRetryCoordinatorDrained, coordinator.stateSnapshot())
	require.False(t, processRetryCoordinatorRegisteredForTesting(coordinator))
}

func TestDeferredProcessRetryCoordinatorShutdownOverridesNormalDrain(t *testing.T) {
	coordinator := newProcessRetryCoordinatorForTesting(false)
	admission := coordinator.beginAdmission()
	require.NotNil(t, admission)

	drained := make(chan processRetryCoordinatorSummary, 1)
	stateChanged := coordinator.stateChange()
	go func() { drained <- coordinator.drain(0) }()
	<-stateChanged
	require.Equal(t, processRetryCoordinatorSealed, coordinator.stateSnapshot())

	coordinator.requestShutdown()
	require.True(t, admission.abort())
	summary := <-drained
	require.Equal(t, processRetryFailureExitCode, summary.exitCode)
	require.True(t, summary.packageFailed)
	require.Equal(t, processRetryCoordinatorDrained, coordinator.stateSnapshot())
	require.True(t, coordinator.awaitCompletion(time.Now()))
}

func TestDeferredProcessRetryCoordinatorAwaitCompletionRejectsExpiredDeadline(t *testing.T) {
	var nilCoordinator *processRetryCoordinator
	require.True(t, nilCoordinator.awaitCompletion(time.Time{}))
	require.False(t, newProcessRetryCoordinatorForTesting(false).awaitCompletion(time.Time{}))
}

func TestDeferredProcessRetryCoordinatorAbortCancelsQueuedGroupsWithoutLaunching(t *testing.T) {
	runner := func(context.Context, *deferredProcessRetryGroup, deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		t.Fatal("an abnormal testing.M unwind must not launch deferred retries")
		return processRetryAttemptResult{}
	}
	coordinator := newProcessRetryCoordinatorForTesting(false, runner)
	group := newDeferredProcessRetrySchedulerGroup("TestAbortedMRun", 1, false, false, 1, 1)
	require.True(t, coordinator.beginAdmission().commit(group))

	summary := coordinator.abort()
	require.Equal(t, processRetryFailureExitCode, summary.exitCode)
	require.True(t, summary.packageFailed)
	require.True(t, summary.deferredFailed)
	require.Equal(t, "process_shutdown", group.terminalFailureReason)
	require.True(t, group.terminalFailure)
	require.Equal(t, processRetryCoordinatorDrained, coordinator.stateSnapshot())
	require.Equal(t, testingMAbnormalExitCode, instrumentTestingMAbnormalExitCode())
}

func TestDeferredProcessRetryCoordinatorAbortCancelsActiveWorker(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	started := make(chan struct{})
	runner := func(ctx context.Context, _ *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		close(started)
		<-ctx.Done()
		attempt := deferredProcessRetryPassingAttempt(prepared.index)
		attempt.Err = ctx.Err()
		attempt.Result.Status = processRetryStatusFail
		attempt.ExitCode = processRetryFailureExitCode
		return attempt
	}
	coordinator := newProcessRetryCoordinatorForTesting(false, runner)
	group := newDeferredProcessRetrySchedulerGroup("TestActiveAbort", 1, false, false, 1, 1)
	require.True(t, coordinator.beginAdmission().commit(group))
	drained := make(chan processRetryCoordinatorSummary, 1)
	go func() { drained <- coordinator.drain(0) }()
	<-started
	aborted := make(chan processRetryCoordinatorSummary, 1)
	go func() { aborted <- coordinator.abort() }()

	drainSummary := <-drained
	abortSummary := <-aborted
	require.Equal(t, drainSummary, abortSummary)
	require.True(t, abortSummary.packageFailed)
	require.True(t, group.truncated)
	require.Equal(t, "process_shutdown", group.terminalFailureReason)
}

func TestDeferredProcessRetryCoordinatorLateShutdownPublishesFailedSummary(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	drainEntered := make(chan struct{})
	releaseDrain := make(chan struct{})
	runner := func(_ context.Context, _ *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		close(drainEntered)
		<-releaseDrain
		return deferredProcessRetryPassingAttempt(prepared.index)
	}
	coordinator := newProcessRetryCoordinatorForTesting(false, runner)
	group := newDeferredProcessRetrySchedulerGroup("TestLateShutdown", 0, false, false, 1, 1)
	require.True(t, coordinator.beginAdmission().commit(group))
	drained := make(chan processRetryCoordinatorSummary, 1)
	go func() { drained <- coordinator.drain(0) }()
	<-drainEntered
	coordinator.requestShutdown()
	close(releaseDrain)

	summary := <-drained
	require.Equal(t, processRetryFailureExitCode, summary.exitCode)
	require.True(t, summary.packageFailed)
	require.True(t, summary.deferredFailed)
}

func TestDeferredProcessRetryCoordinatorEmptyDrainPreservesNativeFailure(t *testing.T) {
	coordinator := newProcessRetryCoordinatorForTesting(false)
	summary := coordinator.drain(7)
	require.Equal(t, 7, summary.exitCode)
	require.True(t, summary.packageFailed)
	require.Zero(t, summary.queuedGroups)
}

func TestDeferredProcessRetryExitCodeMergeNeverHidesNativeFailure(t *testing.T) {
	require.Equal(t, 0, mergeDeferredProcessRetryExitCode(0, false))
	require.Equal(t, processRetryFailureExitCode, mergeDeferredProcessRetryExitCode(0, true))
	require.Equal(t, 7, mergeDeferredProcessRetryExitCode(7, false))
	require.Equal(t, 7, mergeDeferredProcessRetryExitCode(7, true))
}

func TestDeferredProcessRetryAggregatePolicy(t *testing.T) {
	tests := []struct {
		name         string
		metadata     processRetryMetadataSnapshot
		observations [][2]bool
		failed       bool
	}{
		{name: "efd failure then pass", metadata: processRetryMetadataSnapshot{isEarlyFlakeDetectionEnabled: true, isANewTest: true}, observations: [][2]bool{{true, false}, {false, false}}},
		{name: "efd all fail", metadata: processRetryMetadataSnapshot{isEarlyFlakeDetectionEnabled: true, isANewTest: true}, observations: [][2]bool{{true, false}, {true, false}}, failed: true},
		{name: "ftr final pass", metadata: processRetryMetadataSnapshot{isFlakyTestRetriesEnabled: true}, observations: [][2]bool{{true, false}, {false, false}}},
		{name: "ftr final fail", metadata: processRetryMetadataSnapshot{isFlakyTestRetriesEnabled: true}, observations: [][2]bool{{true, false}, {true, false}}, failed: true},
		{name: "a2f any failure", metadata: processRetryMetadataSnapshot{isAttemptToFix: true, shouldOrchestrateAttemptToFix: true}, observations: [][2]bool{{true, false}, {false, false}}, failed: true},
		{name: "quarantined masks failure", metadata: processRetryMetadataSnapshot{isQuarantined: true}, observations: [][2]bool{{true, false}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			group := deferredProcessRetryGroup{metadata: test.metadata}
			for _, observation := range test.observations {
				group.outcomes.observe(observation[0], observation[1])
				group.latest.failed = observation[0]
				group.latest.skipped = observation[1]
			}
			require.Equal(t, test.failed, group.packageFailed())
		})
	}
}

func TestRetryOutcomeAccumulator(t *testing.T) {
	tests := []struct {
		name              string
		observations      [][2]bool
		passed            int
		skipped           int
		failed            int
		allAttemptsPassed bool
		allRetriesFailed  bool
	}{
		{name: "empty", allAttemptsPassed: true, allRetriesFailed: true},
		{name: "pass", observations: [][2]bool{{false, false}}, passed: 1, allAttemptsPassed: true},
		{name: "failure", observations: [][2]bool{{true, false}}, failed: 1, allRetriesFailed: true},
		{name: "skip", observations: [][2]bool{{false, true}}, skipped: 1},
		{name: "mixed", observations: [][2]bool{{true, false}, {false, true}, {false, false}}, passed: 1, skipped: 1, failed: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var outcomes retryOutcomeAccumulator
			for _, observation := range test.observations {
				outcomes.observe(observation[0], observation[1])
			}
			require.Equal(t, test.passed, outcomes.passed)
			require.Equal(t, test.skipped, outcomes.skipped)
			require.Equal(t, test.failed, outcomes.failed)
			require.Equal(t, test.passed > 0, outcomes.anyPassed())
			require.Equal(t, test.failed > 0, outcomes.anyFailed())
			require.Equal(t, test.allAttemptsPassed, outcomes.allAttemptsPassed())
			require.Equal(t, test.allRetriesFailed, outcomes.allRetriesFailed())
		})
	}
}

func TestRetryExecutionIsLast(t *testing.T) {
	tests := []struct {
		name       string
		metadata   *testExecutionMetadata
		remaining  int64
		budget     int64
		last       bool
		recognized bool
	}{
		{name: "nil metadata"},
		{name: "no retry family", metadata: &testExecutionMetadata{}},
		{name: "attempt to fix last", metadata: &testExecutionMetadata{isAttemptToFix: true, shouldOrchestrateAttemptToFix: true}, remaining: 1, last: true, recognized: true},
		{name: "attempt to fix not last", metadata: &testExecutionMetadata{isAttemptToFix: true, shouldOrchestrateAttemptToFix: true}, remaining: 2, recognized: true},
		{name: "unowned attempt to fix", metadata: &testExecutionMetadata{isAttemptToFix: true}, remaining: 1},
		{name: "efd last", metadata: &testExecutionMetadata{isEarlyFlakeDetectionEnabled: true, isANewTest: true}, remaining: 1, last: true, recognized: true},
		{name: "efd not last", metadata: &testExecutionMetadata{isEarlyFlakeDetectionEnabled: true, isAModifiedTest: true}, remaining: 2, recognized: true},
		{name: "flaky retry count exhausted", metadata: &testExecutionMetadata{isFlakyTestRetriesEnabled: true}, remaining: 1, budget: 2, last: true, recognized: true},
		{name: "flaky budget exhausted", metadata: &testExecutionMetadata{isFlakyTestRetriesEnabled: true}, remaining: 3, last: true, recognized: true},
		{name: "flaky retry remains", metadata: &testExecutionMetadata{isFlakyTestRetriesEnabled: true}, remaining: 3, budget: 2, recognized: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			last, recognized := retryExecutionIsLast(test.metadata, test.remaining, test.budget)
			require.Equal(t, test.last, last)
			require.Equal(t, test.recognized, recognized)
		})
	}
}

func TestDeferredProcessRetryIrreversibleFirstFailurePolicy(t *testing.T) {
	tests := []struct {
		name        string
		metadata    testExecutionMetadata
		observation retryAttemptObservation
		want        bool
	}{
		{name: "a2f failure", metadata: testExecutionMetadata{isAttemptToFix: true}, observation: retryAttemptObservation{failed: true}, want: true},
		{name: "a2f pass", metadata: testExecutionMetadata{isAttemptToFix: true}},
		{name: "efd failure", metadata: testExecutionMetadata{isEarlyFlakeDetectionEnabled: true}, observation: retryAttemptObservation{failed: true}},
		{name: "ftr failure", metadata: testExecutionMetadata{isFlakyTestRetriesEnabled: true}, observation: retryAttemptObservation{failed: true}},
		{name: "disabled a2f failure", metadata: testExecutionMetadata{isAttemptToFix: true, isDisabled: true}, observation: retryAttemptObservation{failed: true}},
		{name: "quarantined a2f failure", metadata: testExecutionMetadata{isAttemptToFix: true, isQuarantined: true}, observation: retryAttemptObservation{failed: true}},
	}
	for index := range tests {
		test := &tests[index]
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, deferredProcessRetryFirstFailureIsIrreversible(&test.metadata, test.observation))
		})
	}
	require.False(t, deferredProcessRetryFirstFailureIsIrreversible(nil, retryAttemptObservation{failed: true}))
}

func TestDeferredProcessRetryCoordinatorDrainsGroupsFIFO(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	var drained []uint64
	runner := func(_ context.Context, group *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		drained = append(drained, group.id)
		return deferredProcessRetryPassingAttempt(prepared.index)
	}
	coordinator := newProcessRetryCoordinatorForTesting(false, runner)
	groups := []*deferredProcessRetryGroup{
		newDeferredProcessRetrySchedulerGroup("TestFIFO1", 0, false, false, 1, 1),
		newDeferredProcessRetrySchedulerGroup("TestFIFO2", 0, false, false, 1, 1),
		newDeferredProcessRetrySchedulerGroup("TestFIFO3", 0, false, false, 1, 1),
	}
	for _, group := range groups {
		require.True(t, coordinator.beginAdmission().commit(group))
	}
	summary := coordinator.drain(0)

	require.Equal(t, []uint64{groups[0].id, groups[1].id, groups[2].id}, drained)
	require.False(t, summary.packageFailed)
	require.Equal(t, len(groups), summary.queuedGroups)
}

func TestDeferredProcessRetryFTRReservationsNeverOversubscribe(t *testing.T) {
	const (
		credits = 3
		callers = 12
	)
	restoreBudget := setProcessRetryBudgetForTesting(credits, credits)
	defer restoreBudget()

	start := make(chan struct{})
	results := make(chan *flakyRetryBudgetReservation, callers)
	for range callers {
		go func() {
			reservation := &flakyRetryBudgetReservation{}
			<-start
			if reservation.reserve() {
				results <- reservation
				return
			}
			results <- nil
		}()
	}
	close(start)

	reserved := make([]*flakyRetryBudgetReservation, 0, credits)
	for range callers {
		if reservation := <-results; reservation != nil {
			reserved = append(reserved, reservation)
		}
	}
	require.Len(t, reserved, credits)
	require.Zero(t, flakyRetryBudgetRemaining(integrations.GetFlakyRetriesSettings()))
	for _, reservation := range reserved {
		reservation.consume()
		reservation.refund()
	}
	require.Zero(t, flakyRetryBudgetRemaining(integrations.GetFlakyRetriesSettings()), "consumed credits must not be refunded")
}

func TestDeferredProcessRetryFamilyPrecedenceAndSlowEFDFallback(t *testing.T) {
	a2f := &testExecutionMetadata{
		isAttemptToFix:                true,
		shouldOrchestrateAttemptToFix: true,
		isEarlyFlakeDetectionEnabled:  true,
		isANewTest:                    true,
		isFlakyTestRetriesEnabled:     true,
	}
	reason, ok := processRetryReasonForExecution(a2f)
	require.True(t, ok)
	require.Equal(t, constants.AttemptToFixRetryReason, reason)

	settings := integrations.GetSettings()
	flaky := integrations.GetFlakyRetriesSettings()
	oldSettings := *settings
	oldRetryCount := flaky.RetryCount
	defer func() {
		*settings = oldSettings
		flaky.RetryCount = oldRetryCount
	}()
	flaky.RetryCount = 4
	slowEFD := &testExecutionMetadata{
		isEarlyFlakeDetectionEnabled: true,
		isANewTest:                   true,
		isFlakyTestRetriesEnabled:    true,
	}
	require.Equal(t, int64(4), computeAdjustedRetryCount(slowEFD, 5*time.Minute))
	require.True(t, slowEFD.efdFellBackToFlakyRetries)
	reason, ok = processRetryReasonForExecution(slowEFD)
	require.True(t, ok)
	require.Equal(t, constants.AutoTestRetriesRetryReason, reason)
	require.False(t, willRetryAfterExecution(false, false, slowEFD, 3, 1))
	require.True(t, willRetryAfterExecution(true, false, slowEFD, 3, 1))
}

func TestDeferredProcessRetryGroupRetainsNoAttemptRuntime(t *testing.T) {
	groupType := reflect.TypeFor[deferredProcessRetryGroup]()
	for index := range groupType.NumField() {
		field := groupType.Field(index)
		require.NotEqual(t, reflect.Func, field.Type.Kind(), "queue field %s must not retain a callback", field.Name)
		require.NotEqual(t, reflect.TypeFor[*testing.T](), field.Type, "queue field %s must not retain testing.T", field.Name)
		require.NotEqual(t, reflect.TypeFor[*retryAttemptGroup](), field.Type, "queue field %s must not retain the fresh-attempt runtime", field.Name)
		require.NotEqual(t, reflect.TypeFor[*executionOptions](), field.Type, "queue field %s must not retain execution options", field.Name)
	}
}

func TestDeferredProcessRetryCoordinatorFailfastStopsLaterGroups(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	var drained []uint64
	runner := func(_ context.Context, group *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		drained = append(drained, group.id)
		attempt := deferredProcessRetryPassingAttempt(prepared.index)
		attempt.Result.Status = processRetryStatusFail
		attempt.ExitCode = processRetryFailureExitCode
		return attempt
	}
	coordinator := newProcessRetryCoordinatorForTesting(true, runner)
	first := newDeferredProcessRetrySchedulerGroup("TestFailfastFirst", 0, false, false, 1, 1)
	second := newDeferredProcessRetrySchedulerGroup("TestFailfastSecond", 0, false, false, 1, 1)
	require.True(t, coordinator.beginAdmission().commit(first))
	require.True(t, coordinator.beginAdmission().commit(second))
	summary := coordinator.drain(0)

	require.Equal(t, []uint64{first.id}, drained)
	require.True(t, summary.packageFailed)
	require.True(t, summary.deferredFailed)
	require.True(t, summary.failfast)
	require.Equal(t, "failfast", second.terminalFailureReason)
	require.False(t, second.terminalFailure, "failfast cancellation keeps the latest real observation authoritative")
}

func TestDeferredProcessRetryCoordinatorNativeFailfastRunsAdmittedFirstAttempts(t *testing.T) {
	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	coordinator := newProcessRetryCoordinatorForTesting(true)
	first := newDeferredQuarantinedFirstAttemptGroupForTesting("TestFirst", 1, 1)
	second := newDeferredQuarantinedFirstAttemptGroupForTesting("TestSecond", 1, 2)
	require.True(t, coordinator.beginAdmission().commit(first))
	require.True(t, coordinator.beginAdmission().commit(second))
	var batches [][]*deferredProcessRetryGroup
	coordinator.batchRunner = func(_ context.Context, groups []*deferredProcessRetryGroup) map[*deferredProcessRetryGroup]processRetryAttemptResult {
		batches = append(batches, append([]*deferredProcessRetryGroup(nil), groups...))
		return map[*deferredProcessRetryGroup]processRetryAttemptResult{
			first:  fixedProcessRetryAttempt(processRetryStatusPass, 1),
			second: fixedProcessRetryAttempt(processRetryStatusPass, 1),
		}
	}

	summary := coordinator.drain(3)

	require.Equal(t, [][]*deferredProcessRetryGroup{{first, second}}, batches)
	require.Len(t, recorder.tests, 2)
	require.Equal(t, 3, summary.exitCode)
	require.True(t, summary.failfast)
}

func TestDeferredProcessRetryFailfastRefundsUnconsumedFTRReservation(t *testing.T) {
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()
	reservation := &flakyRetryBudgetReservation{}
	require.True(t, reservation.reserve())
	require.Zero(t, flakyRetryBudgetRemaining(integrations.GetFlakyRetriesSettings()))

	group := &deferredProcessRetryGroup{reservation: reservation}
	group.cancel("failfast", false)
	require.Equal(t, int64(1), flakyRetryBudgetRemaining(integrations.GetFlakyRetriesSettings()))
	group.cancel("failfast", false)
	require.Equal(t, int64(1), flakyRetryBudgetRemaining(integrations.GetFlakyRetriesSettings()))
}

func TestDeferredProcessRetryRetiredMRunKeepsStickyFailure(t *testing.T) {
	claim := &testingMInstrumentationClaim{}
	recordTestingMDeferredDisposition(claim, processRetryCoordinatorSummary{
		exitCode:       processRetryFailureExitCode,
		packageFailed:  true,
		deferredFailed: true,
	})

	proceed, finalize := retiredTestingMDisposition(claim)
	require.True(t, proceed)
	require.Equal(t, processRetryFailureExitCode, finalize(0))
	require.Equal(t, 7, finalize(7), "a native non-zero exit remains authoritative")

	claim.failfastLatched = true
	proceed, finalize = retiredTestingMDisposition(claim)
	require.False(t, proceed)
	require.Equal(t, processRetryFailureExitCode, finalize(0))
}

func TestDeferredProcessRetryCoordinatorSelectsOneTerminalReplay(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	var drained []uint64
	runner := func(_ context.Context, group *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		drained = append(drained, group.id)
		attempt := deferredProcessRetryPassingAttempt(prepared.index)
		attempt.Result.Status = processRetryStatusFail
		attempt.ExitCode = processRetryFailureExitCode
		return attempt
	}
	coordinator := newProcessRetryCoordinatorForTesting(false, runner)
	first := newDeferredProcessRetrySchedulerGroup("TestTerminalReplayFirst", 0, false, false, 1, 1)
	first.panicPresent = true
	first.panicMessage = "boom"
	first.panicStack = "stack"
	second := newDeferredProcessRetrySchedulerGroup("TestTerminalReplaySecond", 0, false, false, 1, 1)
	require.True(t, coordinator.beginAdmission().commit(first))
	require.True(t, coordinator.beginAdmission().commit(second))
	summary := coordinator.drain(0)

	require.Equal(t, []uint64{first.id}, drained)
	require.Equal(t, "test failed and panicked after 1 retries.\nboom\nstack", summary.terminalPanic)
	require.Equal(t, "terminal_replay", second.terminalFailureReason)
}

func TestDeferredProcessRetryCancellationFinalizesTailEventOnce(t *testing.T) {
	event := newProcessRetryRecordingTestForTesting("TestDeferredTail")
	group := &deferredProcessRetryGroup{
		metadata: processRetryMetadataSnapshot{isAttemptToFix: true, shouldOrchestrateAttemptToFix: true},
		latest:   retryAttemptObservation{failed: true},
		outcomes: retryOutcomeAccumulator{failed: 1},
		tailEvent: &deferredProcessRetryEvent{
			event:  event,
			status: integrations.ResultStatusFail,
			ready:  true,
		},
	}

	group.cancel("failfast", false)
	group.cancel("failfast", false)

	require.Equal(t, 1, event.closeCount)
	require.Equal(t, constants.TestStatusFail, event.tags[constants.TestFinalStatus])
	require.Equal(t, "false", event.tags[constants.TestAttemptToFixPassed])
}

func TestDeferredProcessRetryTerminalFailureOverridesEarlierEFDPass(t *testing.T) {
	event := newProcessRetryRecordingTestForTesting("TestDeferredTerminalEFD")
	group := &deferredProcessRetryGroup{
		metadata: processRetryMetadataSnapshot{isEarlyFlakeDetectionEnabled: true, isANewTest: true},
		outcomes: retryOutcomeAccumulator{passed: 1},
		tailEvent: &deferredProcessRetryEvent{
			event:  event,
			status: integrations.ResultStatusPass,
			ready:  true,
		},
	}

	group.cancel("containment_lost", true)

	require.True(t, group.packageFailed())
	require.Equal(t, 1, event.closeCount)
	require.Equal(t, constants.TestStatusFail, event.tags[constants.TestFinalStatus])
}

func TestDeferredProcessRetryShutdownStartsCompletionWithoutWaitingForInitialEvent(t *testing.T) {
	coordinator, execMeta, event, group := newDeferredProcessRetryPendingGroupForTesting(t, retryAttemptObservation{failed: true})

	coordinator.mu.Lock()
	inFlight := coordinator.inFlight
	queued := len(coordinator.queue)
	coordinator.mu.Unlock()
	require.Equal(t, 1, inFlight)
	require.Zero(t, queued)

	coordinator.completeShutdown()
	require.Equal(t, processRetryCoordinatorShuttingDown, coordinator.stateSnapshot())
	select {
	case <-coordinator.completed:
		t.Fatal("coordinator completed before the pending admission was published")
	default:
	}
	require.Zero(t, event.closeCount)

	deferOrCloseInstrumentedTestEvent(execMeta, event, integrations.ResultStatusFail, "")
	completeDeferredProcessRetryEvent(execMeta)
	select {
	case <-coordinator.completed:
	case <-t.Context().Done():
		t.Fatal("shutdown did not complete after the initial event was published")
	}

	require.Equal(t, 1, event.closeCount)
	require.True(t, group.tailEvent.closed)
	require.Equal(t, "process_shutdown", group.terminalFailureReason)
}

func TestDeferredProcessRetryQueuedPanicUsesFrozenMessage(t *testing.T) {
	panicValue := &deferredProcessRetryMutablePanic{message: "original panic"}
	observation := observeFreshRetryAttempt(0, retryAttemptResult{
		failed:     true,
		panicData:  panicValue,
		panicStack: []byte("original stack"),
	})
	runner := func(_ context.Context, _ *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		attempt := deferredProcessRetryPassingAttempt(prepared.index)
		attempt.Result.Status = processRetryStatusFail
		attempt.ExitCode = processRetryFailureExitCode
		return attempt
	}
	coordinator, execMeta, event, group := newDeferredProcessRetryPendingGroupForTesting(t, observation, runner)

	require.Nil(t, group.latest.panicData, "the deferred queue must not retain the mutable panic value")
	require.Nil(t, group.latest.cleanupPanicData)
	require.Nil(t, group.latest.terminalTrace)
	panicValue.message = "mutated panic"
	deferOrCloseInstrumentedTestEvent(execMeta, event, integrations.ResultStatusFail, "")
	completeDeferredProcessRetryEvent(execMeta)

	summary := coordinator.drain(0)
	terminal := fmt.Sprint(summary.terminalPanic)
	require.Contains(t, terminal, "original panic")
	require.NotContains(t, terminal, "mutated panic")
	require.Contains(t, terminal, "original stack")
}

func TestDeferredProcessRetryMissingInitialEventAbortsAdmission(t *testing.T) {
	coordinator, execMeta, _, group := newDeferredProcessRetryPendingGroupForTesting(t, retryAttemptObservation{failed: true})

	completeDeferredProcessRetryEvent(execMeta)

	require.Empty(t, coordinator.seal())
	require.Nil(t, execMeta.deferredRetryEvent)
	require.Nil(t, group.tailEvent)
	require.Nil(t, group.lease)
}

func TestDeferredProcessRetryLeaseFailureAbortsAdmission(t *testing.T) {
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	restoreSupport := setProcessRetrySupportHooksForTesting(t, processRetrySupportHooks{
		childCleanupSupported:      func() bool { return true },
		testingMWorkloadsSupported: func() bool { return true },
	})
	defer restoreSupport()

	oldRegistered := processRetryActiveChildren.closeActionRegistered.Load()
	processRetryActiveChildren.closeActionRegistered.Store(false)
	t.Cleanup(func() { processRetryActiveChildren.closeActionRegistered.Store(oldRegistered) })

	identity := newTestIdentity("module", "suite", "TestDeferredLeaseFailure")
	execMeta := &testExecutionMetadata{
		identity:                  identity,
		isFlakyTestRetriesEnabled: true,
		test:                      newProcessRetryRecordingTestForTesting(identity.FullName),
		retryAttemptFinalizer:     func(retryAttemptResult) {},
	}
	coordinator := newProcessRetryCoordinatorForTesting(false)
	execOpts := &executionOptions{
		options: &runTestWithRetryOptions{
			t:                       t,
			testInfo:                &commonInfo{moduleName: identity.ModuleName, suiteName: identity.SuiteName, testName: identity.FullName, identity: identity},
			processRetryCoordinator: coordinator,
			processRetryIdentity:    identity,
			processRetryFuzzGuard:   &processRetryFuzzGuardSnapshot{evaluate: func() bool { return false }},
		},
		executionMetadata:          execMeta,
		retryCount:                 1,
		lastObservation:            retryAttemptObservation{failed: true},
		processRetryLaunchBaseline: &processRetryLaunchBaseline{argsSnapshot: processRetryArgsSnapshot{captured: true, ok: true}, currentCPU: 1, maxConcurrency: 1, maxConcurrencySet: true},
	}

	require.False(t, enqueueDeferredProcessRetryGroup(execOpts))
	coordinator.mu.Lock()
	inFlight := coordinator.inFlight
	queued := len(coordinator.queue)
	coordinator.mu.Unlock()
	require.Zero(t, inFlight)
	require.Zero(t, queued)
	require.Nil(t, execMeta.deferredRetryEvent)
}

func TestDeferredProcessRetryInitialPanicControlsTerminalReplay(t *testing.T) {
	panicValue := &struct{ message string }{message: "initial panic"}
	tests := []struct {
		name          string
		retryFailed   bool
		terminalPanic bool
	}{
		{name: "ordinary retry failure", retryFailed: true, terminalPanic: true},
		{name: "retry pass", retryFailed: false, terminalPanic: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := func(_ context.Context, _ *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
				attempt := deferredProcessRetryPassingAttempt(prepared.index)
				if test.retryFailed {
					attempt.Result.Status = processRetryStatusFail
					attempt.ExitCode = processRetryFailureExitCode
				}
				return attempt
			}
			coordinator, execMeta, event, _ := newDeferredProcessRetryPendingGroupForTesting(t, retryAttemptObservation{
				failed:     true,
				panicData:  panicValue,
				panicStack: []byte("initial stack"),
			}, runner)
			deferOrCloseInstrumentedTestEvent(execMeta, event, integrations.ResultStatusFail, "")
			completeDeferredProcessRetryEvent(execMeta)

			summary := coordinator.drain(0)
			if test.terminalPanic {
				require.Contains(t, fmt.Sprint(summary.terminalPanic), "initial panic")
				require.Contains(t, fmt.Sprint(summary.terminalPanic), "initial stack")
				require.Equal(t, processRetryFailureExitCode, summary.exitCode)
			} else {
				require.Nil(t, summary.terminalPanic)
				require.Zero(t, summary.exitCode)
			}
		})
	}
}

func TestDeferredProcessRetryChildEventRemainsTailUntilAggregateFinalization(t *testing.T) {
	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	identity := newTestIdentity("module", "suite", "TestDeferredChildTail")
	execMeta := &testExecutionMetadata{
		identity:                      identity,
		isARetry:                      true,
		isAttemptToFix:                true,
		shouldOrchestrateAttemptToFix: true,
		allAttemptsPassed:             true,
		remainingRetries:              0,
	}
	now := time.Now()
	effective, tail := deferProcessRetryTestEventWithAdmission(&commonInfo{
		moduleName: identity.ModuleName,
		suiteName:  identity.SuiteName,
		testName:   identity.FullName,
		identity:   identity,
	}, execMeta, processRetryAttemptResult{
		Result:     processRetryResult{Status: processRetryStatusPass},
		ExitCode:   0,
		StartTime:  now,
		FinishTime: now.Add(time.Millisecond),
	}, func(processRetryEffectiveStatus) {
		execMeta.retryContinuationDecided = true
		execMeta.retryContinuationAdmitted = false
	})

	require.False(t, effective.Failed)
	require.NotNil(t, tail)
	require.Len(t, recorder.tests, 1)
	require.Zero(t, recorder.tests[0].closeCount)
	group := &deferredProcessRetryGroup{
		metadata:  processRetryMetadataSnapshot{isAttemptToFix: true, shouldOrchestrateAttemptToFix: true},
		latest:    retryAttemptObservation{},
		outcomes:  retryOutcomeAccumulator{passed: 1},
		tailEvent: tail,
	}
	group.finish()

	require.Equal(t, 1, recorder.tests[0].closeCount)
	require.Equal(t, constants.TestStatusPass, recorder.tests[0].tags[constants.TestFinalStatus])
	require.Equal(t, "true", recorder.tests[0].tags[constants.TestAttemptToFixPassed])
}

func TestDeferredProcessRetrySchedulerSharesWorkersRoundRobin(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	type startedAttempt struct {
		groupID uint64
		index   int
		release chan struct{}
	}
	started := make(chan startedAttempt, 4)
	runner := func(_ context.Context, group *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		release := make(chan struct{})
		started <- startedAttempt{groupID: group.id, index: prepared.index, release: release}
		<-release
		return deferredProcessRetryPassingAttempt(prepared.index)
	}
	coordinator := newProcessRetryCoordinatorForTesting(false, runner)
	groups := []*deferredProcessRetryGroup{
		newDeferredProcessRetrySchedulerGroup("TestRoundRobinA", 1, true, true, 2, 2),
		newDeferredProcessRetrySchedulerGroup("TestRoundRobinB", 1, true, true, 2, 2),
	}
	for _, group := range groups {
		require.True(t, coordinator.beginAdmission().commit(group))
	}
	drained := make(chan processRetryCoordinatorSummary, 1)
	go func() { drained <- coordinator.drain(0) }()

	first := <-started
	second := <-started
	require.ElementsMatch(t, []uint64{groups[0].id, groups[1].id}, []uint64{first.groupID, second.groupID})
	require.Equal(t, 1, first.index)
	require.Equal(t, 1, second.index)
	first.release <- struct{}{}
	third := <-started
	require.Equal(t, groups[0].id, third.groupID, "a group may receive its second turn only after every ready group received its first")
	require.Equal(t, 2, third.index)
	second.release <- struct{}{}
	fourth := <-started
	require.Equal(t, groups[1].id, fourth.groupID)
	require.Equal(t, 2, fourth.index)
	third.release <- struct{}{}
	fourth.release <- struct{}{}

	summary := <-drained
	require.False(t, summary.packageFailed)
	require.Equal(t, 2, summary.queuedGroups)
}

func TestDeferredProcessRetrySchedulerBatchesSerialBeforeParallelWithinEachPhase(t *testing.T) {
	parallelA := newDeferredProcessRetrySchedulerGroup("TestParallelA", 0, false, true, 2, 2)
	serial := newDeferredProcessRetrySchedulerGroup("TestSerial", 0, false, false, 2, 2)
	parallelB := newDeferredProcessRetrySchedulerGroup("TestParallelB", 0, false, true, 2, 2)
	nextPhase := newDeferredProcessRetrySchedulerGroup("TestNextPhase", 0, false, false, 2, 2)
	nextPhase.phaseID = 2

	batches := deferredProcessRetryScheduleBatches([]*deferredProcessRetryGroup{parallelA, serial, parallelB, nextPhase})
	require.Len(t, batches, 3)
	require.Equal(t, []*deferredProcessRetryGroup{serial}, batches[0])
	require.Equal(t, []*deferredProcessRetryGroup{parallelA, parallelB}, batches[1])
	require.Equal(t, []*deferredProcessRetryGroup{nextPhase}, batches[2])
}

func TestDeferredProcessRetrySchedulerHonorsNativeParallelGroupLimit(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	type startedGroup struct {
		id      uint64
		release chan struct{}
	}
	started := make(chan startedGroup, 3)
	runner := func(_ context.Context, group *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		release := make(chan struct{})
		started <- startedGroup{id: group.id, release: release}
		<-release
		return deferredProcessRetryPassingAttempt(prepared.index)
	}
	coordinator := newProcessRetryCoordinatorForTesting(false, runner)
	groups := []*deferredProcessRetryGroup{
		newDeferredProcessRetrySchedulerGroup("TestNativeLimitA", 0, false, true, 2, 3),
		newDeferredProcessRetrySchedulerGroup("TestNativeLimitB", 0, false, true, 2, 3),
		newDeferredProcessRetrySchedulerGroup("TestNativeLimitC", 0, false, true, 2, 3),
	}
	require.Equal(t, 2, deferredProcessRetryMaxActiveGroups(groups), "the scheduler must use the stricter native parallel limit")
	for _, group := range groups {
		require.True(t, coordinator.beginAdmission().commit(group))
	}
	drained := make(chan processRetryCoordinatorSummary, 1)
	go func() { drained <- coordinator.drain(0) }()

	first := <-started
	second := <-started
	require.ElementsMatch(t, []uint64{groups[0].id, groups[1].id}, []uint64{first.id, second.id})
	first.release <- struct{}{}
	third := <-started
	require.Equal(t, groups[2].id, third.id, "the third root-parallel group must wait for a native scheduler slot")
	second.release <- struct{}{}
	third.release <- struct{}{}
	require.False(t, (<-drained).packageFailed)
}

func TestDeferredProcessRetrySchedulerAppliesParallelResultsInExecutionOrder(t *testing.T) {
	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	type startedAttempt struct {
		index   int
		release chan struct{}
	}
	started := make(chan startedAttempt, 3)
	runner := func(_ context.Context, _ *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		release := make(chan struct{})
		started <- startedAttempt{index: prepared.index, release: release}
		<-release
		attempt := deferredProcessRetryPassingAttempt(prepared.index)
		if prepared.index == 2 {
			attempt.Result.Status = processRetryStatusFail
			attempt.ExitCode = processRetryFailureExitCode
		}
		return attempt
	}
	coordinator := newProcessRetryCoordinatorForTesting(false, runner)
	group := newDeferredProcessRetrySchedulerGroup("TestOrderedResults", 2, true, false, 1, 3)
	require.True(t, coordinator.beginAdmission().commit(group))
	drained := make(chan processRetryCoordinatorSummary, 1)
	go func() { drained <- coordinator.drain(0) }()

	releases := make(map[int]chan struct{}, 3)
	for range 3 {
		attempt := <-started
		releases[attempt.index] = attempt.release
	}
	releases[3] <- struct{}{}
	releases[1] <- struct{}{}
	releases[2] <- struct{}{}
	require.False(t, (<-drained).packageFailed, "EFD succeeds when any execution passes")
	require.Len(t, recorder.tests, 3)
	require.Equal(t, []processRetryStatus{processRetryStatusPass, processRetryStatusFail, processRetryStatusPass}, []processRetryStatus{
		recorder.tests[0].status,
		recorder.tests[1].status,
		recorder.tests[2].status,
	})
}

func TestDeferredProcessRetryLateSetupFailureIsOneConsumedProcessAttempt(t *testing.T) {
	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	starts := 0
	runner := func(_ context.Context, _ *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		starts++
		now := time.Unix(0, int64(prepared.index))
		return processRetryAttemptResult{
			SetupFailure: true,
			Err:          errors.New("late setup failure"),
			ExitCode:     processRetryExitCodeUnset,
			StartTime:    now,
			FinishTime:   now.Add(time.Nanosecond),
		}
	}
	coordinator := newProcessRetryCoordinatorForTesting(false, runner)
	group := newDeferredProcessRetrySchedulerGroup("TestLateSetupFailure", 1, false, false, 1, 1)
	require.True(t, coordinator.beginAdmission().commit(group))

	summary := coordinator.drain(0)
	require.Equal(t, 1, starts, "a deferred setup failure must not be replayed without a live native testing.T")
	require.True(t, summary.packageFailed)
	require.True(t, summary.deferredFailed)
	require.Len(t, recorder.tests, 1, "the admitted continuation must produce exactly one retry event")
	require.Equal(t, processRetryStatusFail, recorder.tests[0].status)
}

func TestDeferredProcessRetrySchedulerFailfastStartsNoNewAttempts(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	type startedAttempt struct {
		groupID uint64
		release chan struct{}
	}
	started := make(chan startedAttempt, 4)
	var first *deferredProcessRetryGroup
	runner := func(_ context.Context, group *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		release := make(chan struct{})
		started <- startedAttempt{groupID: group.id, release: release}
		<-release
		attempt := deferredProcessRetryPassingAttempt(prepared.index)
		if group == first {
			attempt.Result.Status = processRetryStatusFail
			attempt.ExitCode = processRetryFailureExitCode
		}
		return attempt
	}
	coordinator := newProcessRetryCoordinatorForTesting(true, runner)
	first = newDeferredProcessRetrySchedulerGroup("TestFailfastA", 1, false, true, 2, 2)
	second := newDeferredProcessRetrySchedulerGroup("TestFailfastB", 1, false, true, 2, 2)
	for _, group := range []*deferredProcessRetryGroup{first, second} {
		group.metadata.isAttemptToFix = true
		group.metadata.shouldOrchestrateAttemptToFix = true
		require.True(t, coordinator.beginAdmission().commit(group))
	}
	drained := make(chan processRetryCoordinatorSummary, 1)
	go func() { drained <- coordinator.drain(0) }()

	startedByID := make(map[uint64]startedAttempt, 2)
	for range 2 {
		attempt := <-started
		startedByID[attempt.groupID] = attempt
	}
	startedByID[first.id].release <- struct{}{}
	startedByID[second.id].release <- struct{}{}
	summary := <-drained
	require.True(t, summary.packageFailed)
	require.True(t, summary.failfast)
	require.Len(t, started, 0, "failfast must not dispatch a second attempt for either active group")
	require.True(t, second.truncated)
}

func TestDeferredProcessRetrySchedulerContainmentLossStopsLaterGroups(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	var starts int
	var first *deferredProcessRetryGroup
	runner := func(_ context.Context, group *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		starts++
		if group != first {
			t.Fatal("containment loss must withdraw later groups before they reach the runner")
		}
		attempt := deferredProcessRetryPassingAttempt(prepared.index)
		attempt.Result.Status = processRetryStatusFail
		attempt.ExitCode = processRetryFailureExitCode
		attempt.ContainmentLost = true
		attempt.Err = errProcessRetryContainmentLost
		return attempt
	}
	coordinator := newProcessRetryCoordinatorForTesting(false, runner)
	first = newDeferredProcessRetrySchedulerGroup("TestContainmentLossA", 0, false, false, 1, 1)
	second := newDeferredProcessRetrySchedulerGroup("TestContainmentLossB", 0, false, false, 1, 1)
	for _, group := range []*deferredProcessRetryGroup{first, second} {
		require.True(t, coordinator.beginAdmission().commit(group))
	}

	summary := coordinator.drain(0)
	require.True(t, summary.packageFailed)
	require.True(t, summary.deferredFailed)
	require.Equal(t, 1, starts)
	require.Equal(t, "containment_lost", second.terminalFailureReason)
	require.True(t, second.terminalFailure)
}

func TestDeferredProcessRetryGlobalStopCountsCanceledLaterOrdinaryGroup(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*processRetryMetadataSnapshot)
	}{
		{name: "disabled", configure: func(metadata *processRetryMetadataSnapshot) { metadata.isDisabled = true }},
		{name: "quarantined", configure: func(metadata *processRetryMetadataSnapshot) { metadata.isQuarantined = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
			defer restoreSession()
			var starts int
			var first *deferredProcessRetryGroup
			runner := func(_ context.Context, group *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
				starts++
				require.Same(t, first, group)
				attempt := deferredProcessRetryPassingAttempt(prepared.index)
				attempt.Result.Status = processRetryStatusFail
				attempt.ExitCode = processRetryFailureExitCode
				attempt.ContainmentLost = true
				attempt.Err = errProcessRetryContainmentLost
				return attempt
			}
			coordinator := newProcessRetryCoordinatorForTesting(false, runner)
			first = newDeferredProcessRetrySchedulerGroup("TestMaskedContainmentLoss", 0, false, false, 1, 1)
			tt.configure(&first.metadata)
			second := newDeferredProcessRetrySchedulerGroup("TestCanceledOrdinaryFTR", 0, false, false, 1, 1)
			second.metadata.isEarlyFlakeDetectionEnabled = false
			second.metadata.isANewTest = false
			for _, group := range []*deferredProcessRetryGroup{first, second} {
				require.True(t, coordinator.beginAdmission().commit(group))
			}

			summary := coordinator.drain(0)

			require.Equal(t, 1, starts)
			require.False(t, first.packageFailed(), "the leading directive masks its own failure")
			require.True(t, second.packageFailed(), "canceling the ordinary FTR group must fail the package")
			require.True(t, summary.deferredFailed)
			require.True(t, summary.packageFailed)
			require.Equal(t, processRetryFailureExitCode, summary.exitCode)
		})
	}
}

func TestDeferredProcessRetryGlobalStopReasons(t *testing.T) {
	tests := []struct {
		name    string
		attempt processRetryAttemptResult
		want    string
	}{
		{name: "ordinary failure", attempt: processRetryAttemptResult{Err: errors.New("ordinary")}},
		{name: "unreaped", attempt: processRetryAttemptResult{Unreaped: true}, want: "process_unreaped"},
		{name: "containment", attempt: processRetryAttemptResult{ContainmentLost: true}, want: "containment_lost"},
		{name: "launch disabled", attempt: processRetryAttemptResult{Err: errProcessRetryLaunchDisabled}, want: "launch_disabled"},
		{name: "shutdown", attempt: processRetryAttemptResult{Err: errProcessRetryShutdown}, want: "process_shutdown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, deferredProcessRetryGlobalStopReason(test.attempt))
		})
	}
}

func BenchmarkDeferredProcessRetryQueueAdmission(b *testing.B) {
	for _, groupCount := range []int{1, 10, 100} {
		b.Run(fmt.Sprintf("groups=%d", groupCount), func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(float64(groupCount), "groups/op")
			for range b.N {
				coordinator := newProcessRetryCoordinatorForTesting(false)
				for index := range groupCount {
					group := &deferredProcessRetryGroup{invocationOrdinal: uint64(index + 1)}
					if !coordinator.beginAdmission().commit(group) {
						b.Fatal("queue admission failed")
					}
				}
			}
		})
	}
}

func TestDeferredProcessRetryBatchesFirstAttemptsByPhase(t *testing.T) {
	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()

	a := newDeferredQuarantinedFirstAttemptGroupForTesting("TestA", 1, 1)
	b := newDeferredQuarantinedFirstAttemptGroupForTesting("TestB", 1, 2)
	c := newDeferredQuarantinedFirstAttemptGroupForTesting("TestC", 2, 3)
	ordinary := &deferredProcessRetryGroup{invocationOrdinal: 4}
	coordinator := newProcessRetryCoordinatorForTesting(false)
	var batches [][]*deferredProcessRetryGroup
	coordinator.batchRunner = func(_ context.Context, groups []*deferredProcessRetryGroup) map[*deferredProcessRetryGroup]processRetryAttemptResult {
		batches = append(batches, append([]*deferredProcessRetryGroup(nil), groups...))
		results := make(map[*deferredProcessRetryGroup]processRetryAttemptResult, len(groups))
		for index, group := range groups {
			results[group] = fixedProcessRetryAttempt(processRetryStatusPass, int64(index+1))
		}
		return results
	}

	remaining, batchFailed := coordinator.drainDeferredFirstAttempts([]*deferredProcessRetryGroup{a, ordinary, b, c})

	require.False(t, batchFailed)
	require.Equal(t, [][]*deferredProcessRetryGroup{{a, b}, {c}}, batches)
	require.Equal(t, []*deferredProcessRetryGroup{ordinary}, remaining)
	require.Len(t, recorder.tests, 3)
	for _, event := range recorder.tests {
		require.Equal(t, processRetryStatusPass, event.status)
		require.Equal(t, 1, event.closeCount)
		require.Equal(t, constants.TestStatusSkip, event.tags[constants.TestFinalStatus])
	}
}

func TestDeferredProcessRetryUsesNativeScheduledFirstAttemptResult(t *testing.T) {
	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	group := newDeferredQuarantinedFirstAttemptGroupForTesting("TestA", 1, 1)
	result := fixedProcessRetryAttempt(processRetryStatusPass, 1)
	group.firstAttemptResult = &result
	coordinator := newProcessRetryCoordinatorForTesting(false)
	coordinator.batchRunner = func(context.Context, []*deferredProcessRetryGroup) map[*deferredProcessRetryGroup]processRetryAttemptResult {
		t.Fatal("native-scheduled first attempt was executed twice")
		return nil
	}

	remaining, batchFailed := coordinator.drainDeferredFirstAttempts([]*deferredProcessRetryGroup{group})

	require.False(t, batchFailed)
	require.Empty(t, remaining)
	require.Len(t, recorder.tests, 1)
	require.Equal(t, processRetryStatusPass, recorder.tests[0].status)
}

func TestDeferredProcessRetryFirstAttemptDrainReturnsOriginalQueueWhenUnused(t *testing.T) {
	coordinator := newProcessRetryCoordinatorForTesting(false)
	queue := []*deferredProcessRetryGroup{{invocationOrdinal: 1}, {invocationOrdinal: 2}}
	coordinator.batchRunner = func(context.Context, []*deferredProcessRetryGroup) map[*deferredProcessRetryGroup]processRetryAttemptResult {
		t.Fatal("batch runner called without a deferred first attempt")
		return nil
	}

	got, batchFailed := coordinator.drainDeferredFirstAttempts(queue)

	require.False(t, batchFailed)
	require.Equal(t, queue, got)
	require.Same(t, &queue[0], &got[0])
}

func TestDeferredProcessRetryFirstAttemptConsumesInitialRetrySlot(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	settings := integrations.GetSettings()
	oldSettings := *settings
	defer func() { *settings = oldSettings }()
	settings.TestManagement.AttemptToFixRetries = 10

	group := newDeferredQuarantinedFirstAttemptGroupForTesting("TestAttemptToFix", 1, 1)
	group.metadata.isAttemptToFix = true
	group.metadata.shouldOrchestrateAttemptToFix = true

	require.True(t, group.applyDeferredFirstAttempt(fixedProcessRetryAttempt(processRetryStatusPass, 1)))
	require.Equal(t, int64(9), group.retryCount)
}

func TestDeferredProcessRetryAttemptToFixContinuesAfterEveryRace(t *testing.T) {
	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	settings := integrations.GetSettings()
	oldSettings := *settings
	defer func() { *settings = oldSettings }()
	settings.TestManagement.AttemptToFixRetries = 3

	group := newDeferredQuarantinedFirstAttemptGroupForTesting("TestAttemptToFixRace", 1, 1)
	group.metadata.isAttemptToFix = true
	group.metadata.shouldOrchestrateAttemptToFix = true
	raceAttempt := fixedProcessRetryAttempt(processRetryStatusFail, 1)
	raceAttempt.Result.Failed = true
	raceAttempt.Result.RaceDetected = true
	require.True(t, group.applyDeferredFirstAttempt(raceAttempt))

	for index, wantContinue := range []bool{true, false} {
		prepared, ok, _ := group.prepareAttempt(true)
		require.True(t, ok)
		attempt := raceAttempt
		attempt.StartTime = time.Unix(0, int64(index+2))
		attempt.FinishTime = attempt.StartTime.Add(time.Nanosecond)
		require.Equal(t, wantContinue, group.applyCompletedAttempt(deferredProcessRetryCompletedAttempt{
			prepared: prepared,
			result:   attempt,
		}, true, false))
	}
	require.Zero(t, group.retryCount)
	group.finish()
	require.Len(t, recorder.tests, 3)
}

func TestDeferredProcessRetryAttemptToFixRaceTerminalPolicy(t *testing.T) {
	raceAttempt := fixedProcessRetryAttempt(processRetryStatusFail, 1)
	raceAttempt.Result.Failed = true
	raceAttempt.Result.RaceDetected = true
	ownedAttemptToFix := &processRetryMetadataSnapshot{isAttemptToFix: true, shouldOrchestrateAttemptToFix: true}
	unownedAttemptToFix := &processRetryMetadataSnapshot{isAttemptToFix: true}

	require.False(t, deferredProcessRetryAttemptTerminal(ownedAttemptToFix, raceAttempt))
	require.True(t, deferredProcessRetryAttemptTerminal(unownedAttemptToFix, raceAttempt))
	require.True(t, deferredProcessRetryAttemptTerminal(nil, raceAttempt))

	infrastructureFailure := raceAttempt
	infrastructureFailure.ContainmentLost = true
	require.True(t, deferredProcessRetryAttemptTerminal(ownedAttemptToFix, infrastructureFailure))
}

func TestDeferredProcessRetryFirstAttemptReservesFTRBudget(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()
	group := newDeferredQuarantinedFirstAttemptGroupForTesting("TestFTR", 1, 1)
	group.metadata.isFlakyTestRetriesEnabled = true
	attempt := fixedProcessRetryAttempt(processRetryStatusFail, 1)
	attempt.Result.Failed = true

	require.True(t, group.applyDeferredFirstAttempt(attempt))
	require.NotNil(t, group.reservation)
	require.True(t, group.reservation.reserved())
	require.Zero(t, flakyRetryBudgetRemaining(integrations.GetFlakyRetriesSettings()))
}

func TestDeferredProcessRetryFirstAttemptAdmitsNewEFDTest(t *testing.T) {
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	settings := integrations.GetSettings()
	oldSettings := *settings
	defer func() { *settings = oldSettings }()
	settings.EarlyFlakeDetection.SlowTestRetries.FiveS = 2
	guard := &recordingEFDFaultySessionGuard{}
	group := newDeferredQuarantinedFirstAttemptGroupForTesting("TestEFD", 1, 1)
	group.metadata.isEarlyFlakeDetectionEnabled = true
	group.metadata.isANewTest = true
	group.efdFaultySessionGuard = guard
	attempt := fixedProcessRetryAttempt(processRetryStatusFail, 1)
	attempt.Result.Failed = true

	require.True(t, group.applyDeferredFirstAttempt(attempt))
	require.Equal(t, 1, guard.admitCalls)
	require.Zero(t, guard.retryCalls)
}

func TestDeferredProcessRetryFirstAttemptDoesNotMarkAllRetriesFailed(t *testing.T) {
	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	group := newDeferredQuarantinedFirstAttemptGroupForTesting("TestQuarantined", 1, 1)
	attempt := fixedProcessRetryAttempt(processRetryStatusFail, 1)
	attempt.Result.Failed = true

	require.False(t, group.applyDeferredFirstAttempt(attempt))
	group.finish()
	require.Len(t, recorder.tests, 1)
	_, tagged := recorder.tests[0].tags[constants.TestHasFailedAllRetries]
	require.False(t, tagged)
}

func newDeferredQuarantinedFirstAttemptGroupForTesting(name string, phaseID, ordinal uint64) *deferredProcessRetryGroup {
	identity := newTestIdentity("module", "suite", name)
	return &deferredProcessRetryGroup{
		identity:             *identity,
		testInfo:             commonInfo{moduleName: identity.ModuleName, suiteName: identity.SuiteName, testName: identity.FullName, identity: identity},
		metadata:             processRetryMetadataSnapshot{identity: identity, isQuarantined: true, hasAdditionalFeatureWrapper: true},
		phaseID:              phaseID,
		invocationOrdinal:    ordinal,
		deferredFirstAttempt: true,
	}
}

func newDeferredProcessRetrySchedulerGroup(
	name string,
	retryCount int64,
	parallelEFD, rootParallel bool,
	nativeMaxParallel, processMaxParallel int,
) *deferredProcessRetryGroup {
	identity := newTestIdentity("module", "suite", name)
	firstEvent := newProcessRetryRecordingTestForTesting(identity.FullName)
	group := &deferredProcessRetryGroup{
		identity:          *identity,
		testInfo:          commonInfo{moduleName: identity.ModuleName, suiteName: identity.SuiteName, testName: identity.FullName, identity: identity},
		metadata:          processRetryMetadataSnapshot{identity: identity, isEarlyFlakeDetectionEnabled: true, isANewTest: true, hasAdditionalFeatureWrapper: true},
		launchBaseline:    &processRetryLaunchBaseline{currentCPU: processMaxParallel, maxConcurrency: processMaxParallel, maxConcurrencySet: true},
		retryCount:        retryCount,
		phaseID:           1,
		latest:            retryAttemptObservation{failed: true, rootParallel: rootParallel},
		parallelEFD:       parallelEFD,
		rootParallel:      rootParallel,
		nativeMaxParallel: nativeMaxParallel,
		tailEvent: &deferredProcessRetryEvent{
			event:  firstEvent,
			status: integrations.ResultStatusFail,
			failed: true,
			ready:  true,
		},
	}
	group.outcomes.observe(true, false)
	return group
}

func newDeferredProcessRetryPendingGroupForTesting(
	t *testing.T,
	observation retryAttemptObservation,
	runners ...deferredProcessRetryAttemptRunner,
) (*processRetryCoordinator, *testExecutionMetadata, *processRetryRecordingTest, *deferredProcessRetryGroup) {
	t.Helper()
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	t.Cleanup(restoreSession)
	if !ProcessRetryContainmentSupported() {
		t.Skip("process retry containment is unavailable")
	}
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	t.Cleanup(restoreLaunchGate)
	require.True(t, registerProcessRetryShutdownAction())
	restoreSupport := setProcessRetrySupportHooksForTesting(t, processRetrySupportHooks{
		childCleanupSupported:      func() bool { return true },
		testingMWorkloadsSupported: func() bool { return true },
	})
	t.Cleanup(restoreSupport)

	identity := newTestIdentity("module", "suite", "TestDeferredPending")
	event := newProcessRetryRecordingTestForTesting(identity.FullName)
	execMeta := &testExecutionMetadata{
		identity:                  identity,
		isFlakyTestRetriesEnabled: true,
		test:                      event,
	}
	execMeta.retryAttemptFinalizer = func(retryAttemptResult) {}
	coordinator := newProcessRetryCoordinatorForTesting(false, runners...)
	options := &runTestWithRetryOptions{
		t:                       t,
		testInfo:                &commonInfo{moduleName: identity.ModuleName, suiteName: identity.SuiteName, testName: identity.FullName, identity: identity},
		processRetryCoordinator: coordinator,
		processRetryIdentity:    identity,
		processRetryFuzzGuard:   &processRetryFuzzGuardSnapshot{evaluate: func() bool { return false }},
	}
	execOpts := &executionOptions{
		options:                     options,
		executionMetadata:           execMeta,
		executionIndex:              0,
		retryCount:                  0,
		lastObservation:             observation,
		flakyRetryBudgetReservation: &flakyRetryBudgetReservation{},
		processRetryLaunchBaseline:  &processRetryLaunchBaseline{argsSnapshot: processRetryArgsSnapshot{captured: true, ok: true}, currentCPU: 1, maxConcurrency: 1, maxConcurrencySet: true},
	}
	require.True(t, enqueueDeferredProcessRetryGroup(execOpts))
	require.NotNil(t, execMeta.deferredRetryEvent)
	require.NotNil(t, execMeta.deferredRetryEvent.group)
	return coordinator, execMeta, event, execMeta.deferredRetryEvent.group
}

func deferredProcessRetryPassingAttempt(index int) processRetryAttemptResult {
	start := time.Unix(0, int64(index))
	return processRetryAttemptResult{
		Result:     processRetryResult{Status: processRetryStatusPass},
		ExitCode:   0,
		StartTime:  start,
		FinishTime: start.Add(time.Nanosecond),
	}
}

func newProcessRetryCoordinatorForTesting(failfastEnabled bool, runners ...deferredProcessRetryAttemptRunner) *processRetryCoordinator {
	runner := runDeferredProcessRetryAttempt
	if len(runners) > 0 {
		runner = runners[0]
	}
	return newProcessRetryCoordinator(func() bool { return failfastEnabled }, runner)
}
