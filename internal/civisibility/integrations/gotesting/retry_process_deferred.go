// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

// retryAttemptObservation is the immutable policy input produced when one
// fresh attempt finishes. It deliberately excludes testing.T state and output
// buffers so later policy code cannot retain attempt-owned mutable data.
type retryAttemptObservation struct {
	executionIndex         int
	failed                 bool
	skipped                bool
	duration               time.Duration
	raceDetected           bool
	nativeFatalRequired    bool
	nativeFatalTraceReplay bool
	panicData              any
	panicMessage           string
	panicStack             []byte
	cleanupPanicData       any
	terminalTrace          []retryAttemptTerminal
	rootParallel           bool
}

func observeFreshRetryAttempt(executionIndex int, result retryAttemptResult) retryAttemptObservation {
	observation := retryAttemptObservation{
		executionIndex:         executionIndex,
		failed:                 result.failed,
		skipped:                result.skipped,
		duration:               result.duration,
		raceDetected:           result.raceDetected,
		nativeFatalRequired:    result.nativeFatalRequired,
		nativeFatalTraceReplay: result.nativeFatalTraceReplay,
		panicData:              result.panicData,
		panicStack:             append([]byte(nil), result.panicStack...),
		cleanupPanicData:       result.cleanupPanicData,
		terminalTrace:          cloneRetryAttemptTerminalTrace(result.terminalTrace),
		rootParallel:           result.parallelLeaseHeld,
	}
	if result.panicData != nil {
		observation.panicMessage = fmt.Sprint(result.panicData)
	}
	return observation
}

func (o retryAttemptObservation) stopsRetryContinuation() bool {
	return o.nativeFatalRequired || o.raceDetected
}

type processRetryCoordinatorState uint8

const (
	processRetryCoordinatorAccepting processRetryCoordinatorState = iota
	processRetryCoordinatorSealed
	processRetryCoordinatorDraining
	processRetryCoordinatorShuttingDown
	processRetryCoordinatorDrained
)

type deferredProcessRetryGroup struct {
	id                    uint64
	identity              testIdentity
	testInfo              commonInfo
	metadata              processRetryMetadataSnapshot
	launchBaseline        *processRetryLaunchBaseline
	lease                 *processRetryGroupLease
	parentDeadline        time.Time
	parentDeadlineOK      bool
	mRunEpoch             uint64
	phaseID               uint64
	invocationOrdinal     uint64
	executionIndex        int
	retryCount            int64
	reservation           *flakyRetryBudgetReservation
	latest                retryAttemptObservation
	outcomes              retryOutcomeAccumulator
	module                integrations.TestModule
	suite                 integrations.TestSuite
	terminalFailure       bool
	terminalFailureReason string
	truncated             bool
	tailEvent             *deferredProcessRetryEvent
	panicPresent          bool
	panicMessage          string
	panicStack            string
	parallelEFD           bool
	rootParallel          bool
	nativeMaxParallel     int
	efdFaultySessionGuard earlyFlakeDetectionFaultySession
	deferredFirstAttempt  bool
	firstAttemptResult    *processRetryAttemptResult
}

type deferredProcessRetryEvent struct {
	event      integrations.Test
	status     integrations.TestResultStatus
	skipReason string
	finishTime time.Time
	failed     bool
	ready      bool
	closed     bool
	admission  *processRetryAdmission
	group      *deferredProcessRetryGroup
}

type processRetryCoordinatorSummary struct {
	exitCode       int
	packageFailed  bool
	deferredFailed bool
	queuedGroups   int
	failfast       bool
	terminalPanic  any
}

type processRetryCoordinator struct {
	mu               locking.Mutex
	state            processRetryCoordinatorState
	nextID           uint64
	inFlight         int
	queue            []*deferredProcessRetryGroup
	changed          chan struct{}
	shutdown         chan struct{}
	shutdownSignaled bool
	completed        chan struct{}
	completionOwner  atomic.Uint32
	summary          processRetryCoordinatorSummary
	invocationPhase  uint64
	phaseByTestName  map[string]uint64
	failfastEnabled  func() bool
	attemptRunner    deferredProcessRetryAttemptRunner
	batchRunner      deferredProcessRetryBatchRunner
	nativeRunner     deferredProcessRetryBatchRunner
	nativeTests      []processRetryBatchTestConfig
	nativeTestIndex  map[string]int
	nativeBatches    map[uint64]*nativeScheduledProcessRetryBatch
}

type deferredProcessRetryPreparedAttempt struct {
	index       int
	retryReason string
	execMeta    *testExecutionMetadata
}

type deferredProcessRetryCompletedAttempt struct {
	prepared deferredProcessRetryPreparedAttempt
	result   processRetryAttemptResult
}

type deferredProcessRetryScheduledTask struct {
	groupIndex int
	ctx        context.Context
	prepared   deferredProcessRetryPreparedAttempt
}

type deferredProcessRetryScheduledResult struct {
	groupIndex int
	completed  deferredProcessRetryCompletedAttempt
}

type deferredProcessRetryScheduleState struct {
	group           *deferredProcessRetryGroup
	ctx             context.Context
	cancel          context.CancelFunc
	active          int
	remaining       int64
	completed       []deferredProcessRetryCompletedAttempt
	activated       bool
	done            bool
	stopPending     bool
	efdStopped      bool
	terminalAttempt bool
}

type deferredProcessRetryDrainOutcome struct {
	deferredFailed bool
	failfast       bool
	terminalPanic  any
	stopReason     string
}

type deferredProcessRetryAttemptRunner func(context.Context, *deferredProcessRetryGroup, deferredProcessRetryPreparedAttempt) processRetryAttemptResult

type processRetryAdmission struct {
	coordinator *processRetryCoordinator
	id          uint64
	done        atomic.Bool
}

var processRetryCoordinatorRegistry = struct {
	mu     locking.Mutex
	values map[*processRetryCoordinator]struct{}
}{values: make(map[*processRetryCoordinator]struct{})}

func newProcessRetryCoordinator(failfastEnabled func() bool, attemptRunner deferredProcessRetryAttemptRunner) *processRetryCoordinator {
	return &processRetryCoordinator{
		state:           processRetryCoordinatorAccepting,
		changed:         make(chan struct{}),
		shutdown:        make(chan struct{}),
		completed:       make(chan struct{}),
		failfastEnabled: failfastEnabled,
		attemptRunner:   attemptRunner,
		batchRunner:     runDeferredQuarantinedProcessRetryBatch,
		nativeRunner:    runDeferredNativeScheduledProcessRetryBatch,
	}
}

func (c *processRetryCoordinator) observeInvocationPhase(identity *testIdentity) uint64 {
	if c == nil || identity == nil || identity.FullName == "" || len(identity.Segments) != 1 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.invocationPhase == 0 {
		c.invocationPhase = 1
	}
	if c.phaseByTestName == nil {
		c.phaseByTestName = make(map[string]uint64)
	}
	if c.phaseByTestName[identity.FullName] == c.invocationPhase {
		c.invocationPhase++
	}
	c.phaseByTestName[identity.FullName] = c.invocationPhase
	return c.invocationPhase
}

func (c *processRetryCoordinator) beginAdmission() *processRetryAdmission {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != processRetryCoordinatorAccepting {
		return nil
	}
	c.nextID++
	c.inFlight++
	return &processRetryAdmission{coordinator: c, id: c.nextID}
}

func (a *processRetryAdmission) commit(group *deferredProcessRetryGroup) bool {
	if a == nil || a.coordinator == nil || group == nil || !a.done.CompareAndSwap(false, true) {
		return false
	}
	c := a.coordinator
	c.mu.Lock()
	group.id = a.id
	c.queue = append(c.queue, group)
	c.inFlight--
	c.notifyLocked()
	c.mu.Unlock()
	return true
}

func (a *processRetryAdmission) abort() bool {
	if a == nil || a.coordinator == nil || !a.done.CompareAndSwap(false, true) {
		return false
	}
	c := a.coordinator
	c.mu.Lock()
	c.inFlight--
	c.notifyLocked()
	c.mu.Unlock()
	return true
}

func (c *processRetryCoordinator) seal() []*deferredProcessRetryGroup {
	if c == nil {
		return nil
	}
	for {
		c.mu.Lock()
		if c.state == processRetryCoordinatorAccepting {
			c.state = processRetryCoordinatorSealed
			c.notifyLocked()
		}
		if c.inFlight == 0 {
			queue := append([]*deferredProcessRetryGroup(nil), c.queue...)
			c.mu.Unlock()
			return queue
		}
		changed := c.changed
		c.mu.Unlock()
		<-changed
	}
}

func (c *processRetryCoordinator) drain(nativeExitCode int) processRetryCoordinatorSummary {
	if c == nil {
		return processRetryCoordinatorSummary{exitCode: nativeExitCode, packageFailed: nativeExitCode != 0}
	}
	if c.completionOwner.CompareAndSwap(0, 1) {
		c.complete(nativeExitCode, false)
	}
	return c.waitSummary()
}

func (c *processRetryCoordinator) requestShutdown() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.state != processRetryCoordinatorDrained {
		c.state = processRetryCoordinatorShuttingDown
	}
	if !c.shutdownSignaled {
		c.shutdownSignaled = true
		close(c.shutdown)
	}
	c.notifyLocked()
	c.mu.Unlock()
}

func (c *processRetryCoordinator) completeShutdown() {
	if c == nil {
		return
	}
	c.requestShutdown()
	if c.completionOwner.CompareAndSwap(0, 2) {
		// Completion may need an in-flight first-attempt finalizer to publish or
		// abort its admission. Do not let that handoff block the pre-close owner;
		// stopActiveProcessRetryChildren bounds the wait on c.completed.
		go c.complete(processRetryFailureExitCode, true)
	}
}

func (c *processRetryCoordinator) abort() processRetryCoordinatorSummary {
	if c == nil {
		return processRetryCoordinatorSummary{exitCode: processRetryFailureExitCode, packageFailed: true}
	}
	c.requestShutdown()
	if c.completionOwner.CompareAndSwap(0, 2) {
		c.complete(processRetryFailureExitCode, true)
	}
	return c.waitSummary()
}

func (c *processRetryCoordinator) waitSummary() processRetryCoordinatorSummary {
	<-c.completed
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.summary
}

func (c *processRetryCoordinator) complete(nativeExitCode int, shuttingDown bool) {
	queue := c.seal()
	slices.SortStableFunc(queue, func(a, b *deferredProcessRetryGroup) int {
		return cmp.Compare(a.invocationOrdinal, b.invocationOrdinal)
	})
	packageFailed := nativeExitCode != 0
	deferredFailed := false
	failfastLatched := c.failfastEnabled() && packageFailed
	var terminalPanic any
	if !shuttingDown && !c.shutdownRequested() {
		if !failfastLatched {
			var batchFailed bool
			queue, batchFailed = c.drainDeferredFirstAttempts(queue)
			outcome := c.drainScheduledGroups(queue)
			deferredFailed = batchFailed || outcome.deferredFailed
			failfastLatched = outcome.failfast
			terminalPanic = outcome.terminalPanic
		} else {
			remaining, _ := c.drainDeferredFirstAttempts(queue)
			cancelDeferredProcessRetryGroups(remaining, "failfast", false)
		}
		packageFailed = deferredFailed || packageFailed
	} else {
		cancelDeferredProcessRetryGroups(queue, "process_shutdown", true)
		packageFailed = true
		deferredFailed = deferredFailed || len(queue) > 0
	}
	c.mu.Lock()
	shuttingDown = shuttingDown || c.shutdownSignaled
	if shuttingDown {
		c.state = processRetryCoordinatorShuttingDown
		nativeExitCode = processRetryFailureExitCode
		packageFailed = true
		deferredFailed = deferredFailed || len(queue) > 0
	} else {
		c.state = processRetryCoordinatorDraining
	}
	c.notifyLocked()
	c.summary = processRetryCoordinatorSummary{
		exitCode:       mergeDeferredProcessRetryExitCode(nativeExitCode, packageFailed),
		packageFailed:  packageFailed,
		deferredFailed: deferredFailed,
		queuedGroups:   len(queue),
		failfast:       failfastLatched,
		terminalPanic:  terminalPanic,
	}
	c.state = processRetryCoordinatorDrained
	c.notifyLocked()
	close(c.completed)
	c.mu.Unlock()
	unregisterProcessRetryCoordinator(c)
}

func (c *processRetryCoordinator) drainDeferredFirstAttempts(queue []*deferredProcessRetryGroup) ([]*deferredProcessRetryGroup, bool) {
	if len(queue) == 0 {
		return nil, false
	}
	hasDeferredFirstAttempt := false
	for _, group := range queue {
		if group.deferredFirstAttempt {
			hasDeferredFirstAttempt = true
			break
		}
	}
	if !hasDeferredFirstAttempt {
		return queue, false
	}
	batchFailed := false
	remaining := make([]*deferredProcessRetryGroup, 0, len(queue))
	groupsByPhase := make(map[uint64][]*deferredProcessRetryGroup)
	phaseOrder := make([]uint64, 0)
	for _, group := range queue {
		if !group.deferredFirstAttempt {
			remaining = append(remaining, group)
			continue
		}
		if _, exists := groupsByPhase[group.phaseID]; !exists {
			phaseOrder = append(phaseOrder, group.phaseID)
		}
		groupsByPhase[group.phaseID] = append(groupsByPhase[group.phaseID], group)
	}
	for _, phaseID := range phaseOrder {
		groups := groupsByPhase[phaseID]
		results := make(map[*deferredProcessRetryGroup]processRetryAttemptResult, len(groups))
		pending := make([]*deferredProcessRetryGroup, 0, len(groups))
		native := make([]*deferredProcessRetryGroup, 0, len(groups))
		missingNative := make([]*deferredProcessRetryGroup, 0, len(groups))
		for _, group := range groups {
			if group.firstAttemptResult == nil {
				pending = append(pending, group)
				continue
			}
			native = append(native, group)
			if errors.Is(group.firstAttemptResult.Err, errProcessRetryResultMissing) {
				missingNative = append(missingNative, group)
				continue
			}
			results[group] = *group.firstAttemptResult
		}
		if len(pending) > 0 {
			maps.Copy(results, c.batchRunner(context.Background(), pending))
		}
		if len(missingNative) > 0 {
			maps.Copy(results, c.nativeRunner(context.Background(), missingNative))
		}
		for _, group := range native {
			if processAttempt, ok := c.nativeScheduledBatchResult(group.invocationOrdinal); ok {
				preserveProcessRetryBatchFailure(processAttempt, []*deferredProcessRetryGroup{group}, results)
			}
		}
		for _, group := range groups {
			attempt, ok := results[group]
			if !ok {
				attempt = processRetryAttemptResult{
					Err:        errProcessRetryResultMissing,
					ExitCode:   processRetryExitCodeUnset,
					StartTime:  time.Now(),
					FinishTime: time.Now(),
				}
			}
			if deferredProcessRetryInfrastructureFailure(attempt) {
				batchFailed = true
			}
			if group.applyDeferredFirstAttempt(attempt) {
				group.deferredFirstAttempt = false
				remaining = append(remaining, group)
				continue
			}
			group.finish()
			group.printSummary()
		}
		for _, group := range native {
			c.cleanupNativeScheduledBatch(group.invocationOrdinal)
		}
	}
	slices.SortStableFunc(remaining, func(a, b *deferredProcessRetryGroup) int {
		return cmp.Compare(a.invocationOrdinal, b.invocationOrdinal)
	})
	return remaining, batchFailed
}

func (g *deferredProcessRetryGroup) applyDeferredFirstAttempt(attempt processRetryAttemptResult) bool {
	execMeta := &testExecutionMetadata{}
	if !applyProcessRetryMetadataSnapshot(execMeta, &g.metadata) {
		g.cancel("missing_metadata_snapshot", true)
		return false
	}
	execMeta.hasAdditionalFeatureWrapper = true
	execMeta.isARetry = false
	duration := max(attempt.FinishTime.Sub(attempt.StartTime), 0)
	// Match the inline runner: the configured count is sampled from the first
	// execution, then that completed execution consumes the initial slot.
	g.retryCount = computeAdjustedRetryCount(execMeta, duration) - 1
	execMeta.remainingRetries = g.retryCount
	execMeta.initialRetryCount = g.retryCount
	execMeta.initialRetryCountSet = true
	execMeta.isLastRetry = g.retryCount <= 0
	terminal := deferredProcessRetryAttemptTerminal(&g.metadata, attempt)
	continueGroup := false
	effective, tail := deferProcessRetryTestEventWithAdmission(&g.testInfo, execMeta, attempt, func(effective processRetryEffectiveStatus) {
		g.outcomes.observe(effective.Failed, effective.Skipped)
		continueGroup = !terminal && deferredProcessRetryShouldContinue(execMeta, effective.Failed, effective.Skipped, g.retryCount)
		if continueGroup {
			continueGroup = g.admitDeferredFirstAttemptContinuation(execMeta, effective)
		}
		execMeta.retryContinuationDecided = true
		execMeta.retryContinuationAdmitted = continueGroup
		execMeta.isLastRetry = !continueGroup
		execMeta.anyExecutionPassed = g.outcomes.anyPassed()
		execMeta.anyExecutionFailed = g.outcomes.anyFailed()
		execMeta.allAttemptsPassed = g.outcomes.allAttemptsPassed()
		execMeta.allRetriesFailed = g.executionIndex > 0 && g.outcomes.allRetriesFailed()
	})
	g.latest = retryAttemptObservation{
		executionIndex: 0,
		failed:         effective.Failed,
		skipped:        effective.Skipped,
		duration:       duration,
		raceDetected:   attempt.Result.RaceDetected,
		rootParallel:   attempt.Result.RootParallel,
	}
	g.rootParallel = attempt.Result.RootParallel
	if attempt.Result.Panic {
		g.panicPresent = true
		g.panicMessage = attempt.Result.ErrorMessage
		g.panicStack = attempt.Result.ErrorStack
	}
	if tail != nil {
		g.tailEvent = tail
	}
	return continueGroup
}

func (g *deferredProcessRetryGroup) admitDeferredFirstAttemptContinuation(execMeta *testExecutionMetadata, effective processRetryEffectiveStatus) bool {
	if g == nil || execMeta == nil {
		return false
	}
	if usesEfdRetrySemantics(execMeta) && g.efdFaultySessionGuard != nil {
		admission := earlyFlakeDetectionAdmissionAllowed
		switch {
		case execMeta.isANewTest:
			admission = g.efdFaultySessionGuard.admitNewTest(execMeta.identity)
		case execMeta.isAModifiedTest:
			admission = g.efdFaultySessionGuard.retryState()
		}
		if admission != earlyFlakeDetectionAdmissionAllowed {
			retryCount, ok := transitionSuppressedEFDToFlakyRetries(execMeta, effective.Failed, g.outcomes.anyPassed())
			if !ok {
				return false
			}
			g.metadata.efdFellBackToFlakyRetries = execMeta.efdFellBackToFlakyRetries
			g.retryCount = retryCount
			execMeta.remainingRetries = retryCount
			execMeta.initialRetryCount = retryCount
		}
	}
	if !usesFlakyRetryBudget(execMeta) {
		return true
	}
	if g.reservation == nil {
		g.reservation = &flakyRetryBudgetReservation{}
	}
	return g.reservation.reserve()
}

func cancelDeferredProcessRetryGroups(groups []*deferredProcessRetryGroup, reason string, failed bool) {
	for _, group := range groups {
		group.cancel(reason, failed)
	}
}

func (c *processRetryCoordinator) drainScheduledGroups(queue []*deferredProcessRetryGroup) deferredProcessRetryDrainOutcome {
	runner := c.attemptRunner
	var outcome deferredProcessRetryDrainOutcome
	batches := deferredProcessRetryScheduleBatches(queue)
	for index, groups := range batches {
		if outcome.terminalPanic != nil {
			cancelDeferredProcessRetryBatches(batches[index:], "terminal_replay", false)
			break
		}
		if outcome.failfast {
			cancelDeferredProcessRetryBatches(batches[index:], "failfast", false)
			break
		}
		batch := c.drainScheduledBatch(groups, runner)
		outcome.deferredFailed = outcome.deferredFailed || batch.deferredFailed
		outcome.failfast = outcome.failfast || batch.failfast
		if outcome.terminalPanic == nil {
			outcome.terminalPanic = batch.terminalPanic
		}
		if batch.stopReason != "" {
			canceledFailed := cancelDeferredProcessRetryBatches(batches[index+1:], batch.stopReason, true)
			outcome.deferredFailed = outcome.deferredFailed || canceledFailed
			break
		}
	}
	return outcome
}

func deferredProcessRetryScheduleBatches(queue []*deferredProcessRetryGroup) [][]*deferredProcessRetryGroup {
	var batches [][]*deferredProcessRetryGroup
	for index := 0; index < len(queue); {
		phaseEnd := index + 1
		phaseID := queue[index].phaseID
		if phaseID != 0 {
			for phaseEnd < len(queue) && queue[phaseEnd].phaseID == phaseID {
				phaseEnd++
			}
		}
		parallel := make([]*deferredProcessRetryGroup, 0, phaseEnd-index)
		for _, group := range queue[index:phaseEnd] {
			if group.rootParallel {
				parallel = append(parallel, group)
			} else {
				batches = append(batches, []*deferredProcessRetryGroup{group})
			}
		}
		if len(parallel) > 0 {
			batches = append(batches, parallel)
		}
		index = phaseEnd
	}
	return batches
}

func cancelDeferredProcessRetryBatches(batches [][]*deferredProcessRetryGroup, reason string, failed bool) bool {
	packageFailed := false
	for _, groups := range batches {
		cancelDeferredProcessRetryGroups(groups, reason, failed)
		for _, group := range groups {
			packageFailed = group.packageFailed() || packageFailed
		}
	}
	return packageFailed
}

func (c *processRetryCoordinator) drainScheduledBatch(
	groups []*deferredProcessRetryGroup,
	runner deferredProcessRetryAttemptRunner,
) deferredProcessRetryDrainOutcome {
	if len(groups) == 0 {
		return deferredProcessRetryDrainOutcome{}
	}
	workerCount := deferredProcessRetryWorkerCount(groups)
	maxActiveGroups := deferredProcessRetryMaxActiveGroups(groups)
	states := make([]deferredProcessRetryScheduleState, len(groups))
	for index, group := range groups {
		ctx, cancel := context.WithCancel(context.Background())
		states[index] = deferredProcessRetryScheduleState{
			group:     group,
			ctx:       ctx,
			cancel:    cancel,
			remaining: max(group.retryCount+1, 0),
		}
	}
	defer func() {
		for index := range states {
			states[index].cancel()
		}
	}()

	tasks := make(chan deferredProcessRetryScheduledTask, workerCount)
	results := make(chan deferredProcessRetryScheduledResult, workerCount)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for task := range tasks {
				group := groups[task.groupIndex]
				results <- deferredProcessRetryScheduledResult{
					groupIndex: task.groupIndex,
					completed: deferredProcessRetryCompletedAttempt{
						prepared: task.prepared,
						result:   runner(task.ctx, group, task.prepared),
					},
				}
			}
		}()
	}
	defer func() {
		close(tasks)
		workers.Wait()
	}()

	ready := make([]int, 0, len(states))
	nextToActivate := 0
	activeGroups := 0
	activeTasks := 0
	doneGroups := 0
	shutdown := c.shutdown
	var outcome deferredProcessRetryDrainOutcome
	var globalStopReason string

	activate := func() {
		for nextToActivate < len(states) && activeGroups < maxActiveGroups {
			state := &states[nextToActivate]
			state.activated = true
			ready = append(ready, nextToActivate)
			nextToActivate++
			activeGroups++
		}
	}
	finishState := func(index int) {
		state := &states[index]
		if state.done {
			return
		}
		state.done = true
		state.cancel()
		state.group.finish()
		state.group.printSummary()
		groupFailed := state.group.packageFailed()
		outcome.deferredFailed = outcome.deferredFailed || groupFailed
		if groupFailed && c.failfastEnabled() {
			outcome.failfast = true
		}
		if groupFailed && state.group.panicPresent && outcome.terminalPanic == nil {
			outcome.terminalPanic = deferredProcessRetryTerminalPanic(state.group)
		}
		activeGroups--
		doneGroups++
	}
	stopAll := func(reason string, failed, cancelActive bool) {
		ready = ready[:0]
		for index := range states {
			state := &states[index]
			if state.done {
				continue
			}
			if !state.activated || state.active == 0 {
				state.group.cancel(reason, failed)
				state.done = true
				state.cancel()
				if state.activated {
					activeGroups--
				}
				doneGroups++
				outcome.deferredFailed = outcome.deferredFailed || state.group.packageFailed()
				continue
			}
			state.stopPending = true
			state.remaining = 0
			state.group.truncated = true
			state.group.terminalFailureReason = reason
			if failed {
				state.group.terminalFailure = true
			}
			if cancelActive {
				state.cancel()
			}
		}
		nextToActivate = len(states)
	}
	activate()

	for doneGroups < len(states) {
		if shutdown != nil {
			select {
			case <-shutdown:
				stopAll("process_shutdown", true, true)
				shutdown = nil
			default:
			}
		}

		for len(ready) > 0 && activeTasks < workerCount && outcome.terminalPanic == nil && !outcome.failfast && shutdown != nil {
			index := ready[0]
			ready = ready[1:]
			state := &states[index]
			if state.done || state.stopPending {
				continue
			}
			prepared, ok, efdStopped := state.group.prepareAttempt(state.active == 0)
			if !ok {
				if efdStopped && state.active > 0 {
					state.stopPending = true
					state.efdStopped = true
					state.remaining = 0
					continue
				}
				finishState(index)
				activate()
				continue
			}
			state.active++
			activeTasks++
			if state.group.parallelEFD {
				state.remaining--
				if state.remaining > 0 {
					ready = append(ready, index)
				}
			}
			tasks <- deferredProcessRetryScheduledTask{groupIndex: index, ctx: state.ctx, prepared: prepared}
		}

		if activeTasks == 0 {
			if doneGroups == len(states) {
				break
			}
			activate()
			continue
		}

		var scheduled deferredProcessRetryScheduledResult
		if shutdown != nil {
			select {
			case scheduled = <-results:
			case <-shutdown:
				stopAll("process_shutdown", true, true)
				shutdown = nil
				continue
			}
		} else {
			scheduled = <-results
		}
		state := &states[scheduled.groupIndex]
		state.active--
		activeTasks--
		if globalStopReason == "" {
			globalStopReason = deferredProcessRetryGlobalStopReason(scheduled.completed.result)
			if globalStopReason != "" {
				outcome.stopReason = globalStopReason
			}
		}

		if state.group.parallelEFD {
			state.completed = append(state.completed, scheduled.completed)
			if deferredProcessRetryAttemptTerminal(&state.group.metadata, scheduled.completed.result) {
				state.stopPending = true
				state.terminalAttempt = true
				state.remaining = 0
				state.group.truncated = true
				state.cancel()
			}
			if state.active == 0 && (state.remaining == 0 || state.stopPending) {
				slices.SortFunc(state.completed, func(a, b deferredProcessRetryCompletedAttempt) int {
					return cmp.Compare(a.prepared.index, b.prepared.index)
				})
				continueGroup := false
				for resultIndex, completed := range state.completed {
					lastResult := resultIndex+1 == len(state.completed)
					continueGroup = state.group.applyCompletedAttempt(
						completed,
						state.efdStopped && !state.terminalAttempt && lastResult,
						!lastResult,
					)
				}
				state.completed = nil
				if continueGroup {
					state.stopPending = false
					state.efdStopped = false
					state.terminalAttempt = false
					ready = append(ready, scheduled.groupIndex)
					continue
				}
				finishState(scheduled.groupIndex)
			}
		} else {
			continueGroup := state.group.applyCompletedAttempt(scheduled.completed, !state.stopPending, false)
			if c.failfastEnabled() && state.group.metadata.isAttemptToFix && state.group.outcomes.anyFailed() {
				continueGroup = false
				state.group.truncated = true
			}
			if continueGroup && !state.stopPending {
				ready = append(ready, scheduled.groupIndex)
			} else {
				finishState(scheduled.groupIndex)
			}
		}

		if outcome.terminalPanic != nil {
			stopAll("terminal_replay", false, true)
		} else if outcome.failfast {
			stopAll("failfast", false, false)
		} else if globalStopReason != "" {
			stopAll(globalStopReason, true, true)
		}
		activate()
	}
	return outcome
}

func deferredProcessRetryGlobalStopReason(attempt processRetryAttemptResult) string {
	switch {
	case attempt.Unreaped || errors.Is(attempt.Err, errProcessRetryChildUnreaped):
		return "process_unreaped"
	case attempt.ContainmentLost || errors.Is(attempt.Err, errProcessRetryContainmentLost):
		return "containment_lost"
	case errors.Is(attempt.Err, errProcessRetryLaunchDisabled):
		return "launch_disabled"
	case errors.Is(attempt.Err, errProcessRetryShutdown):
		return "process_shutdown"
	default:
		return ""
	}
}

func deferredProcessRetryWorkerCount(groups []*deferredProcessRetryGroup) int {
	workers := 1
	useful := 0
	for _, group := range groups {
		workers = max(workers, int(processRetryParallelMaxConcurrencyForBaseline(group.launchBaseline)))
		if group.parallelEFD {
			useful += int(max(group.retryCount+1, 0))
		} else {
			useful++
		}
	}
	return max(min(workers, useful), 1)
}

func deferredProcessRetryMaxActiveGroups(groups []*deferredProcessRetryGroup) int {
	if len(groups) == 0 || !groups[0].rootParallel {
		return 1
	}
	limit := len(groups)
	for _, group := range groups {
		if group.nativeMaxParallel > 0 {
			limit = min(limit, group.nativeMaxParallel)
		}
	}
	return max(limit, 1)
}

func deferredProcessRetryTerminalPanic(group *deferredProcessRetryGroup) any {
	return fmt.Sprintf(
		"test failed and panicked after %d retries.\n%v\n%v",
		group.executionIndex,
		group.panicMessage,
		group.panicStack,
	)
}

func (c *processRetryCoordinator) shutdownRequested() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.shutdownSignaled
}

func (c *processRetryCoordinator) awaitCompletion(deadline time.Time) bool {
	if c == nil {
		return true
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		select {
		case <-c.completed:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-c.completed:
		return true
	case <-timer.C:
		return false
	}
}

func (c *processRetryCoordinator) notifyLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}

func registerProcessRetryCoordinator(c *processRetryCoordinator) bool {
	if c == nil {
		return false
	}
	processRetryLaunchGate.mu.Lock()
	defer processRetryLaunchGate.mu.Unlock()
	if processRetryLaunchGate.shuttingDown.Load() {
		return false
	}
	processRetryCoordinatorRegistry.mu.Lock()
	processRetryCoordinatorRegistry.values[c] = struct{}{}
	processRetryCoordinatorRegistry.mu.Unlock()
	return true
}

func unregisterProcessRetryCoordinator(c *processRetryCoordinator) {
	if c == nil {
		return
	}
	processRetryCoordinatorRegistry.mu.Lock()
	delete(processRetryCoordinatorRegistry.values, c)
	processRetryCoordinatorRegistry.mu.Unlock()
}

func snapshotProcessRetryCoordinators() []*processRetryCoordinator {
	processRetryCoordinatorRegistry.mu.Lock()
	defer processRetryCoordinatorRegistry.mu.Unlock()
	result := make([]*processRetryCoordinator, 0, len(processRetryCoordinatorRegistry.values))
	for coordinator := range processRetryCoordinatorRegistry.values {
		result = append(result, coordinator)
	}
	return result
}

func mergeDeferredProcessRetryExitCode(nativeExitCode int, packageFailed bool) int {
	if nativeExitCode != 0 || !packageFailed {
		return nativeExitCode
	}
	return processRetryFailureExitCode
}

func cloneDeferredTestIdentity(identity *testIdentity) (testIdentity, bool) {
	if identity == nil || identity.FullName == "" || len(identity.Segments) != 1 {
		return testIdentity{}, false
	}
	clone := *identity
	clone.Segments = append([]string(nil), identity.Segments...)
	return clone, true
}

func prepareDeferredProcessRetryInvocation(execOpts *executionOptions) {
	if execOpts == nil || execOpts.options == nil {
		return
	}
	// Invocation order belongs to the native first pass, so capture it before
	// execution metadata exists. Retry eligibility is checked later at enqueue.
	options := execOpts.options
	if options.processRetryCoordinator == nil || options.processRetryIdentity == nil || len(options.processRetryIdentity.Segments) != 1 {
		return
	}
	if fuzzActive, fuzzGuardSet := options.processRetryFuzzGuard.resolve(); !fuzzGuardSet || fuzzActive {
		return
	}
	execOpts.processRetryLaunchBaseline = captureProcessRetryLaunchBaselineFromTemplate(options.processRetryLaunchTemplate)
	if ok, _ := processRetryParallelBaselineReady(execOpts.processRetryLaunchBaseline); !ok {
		return
	}
	options.processRetryPhaseID = options.processRetryCoordinator.observeInvocationPhase(options.processRetryIdentity)
	if options.processRetryPhaseID != 0 {
		ensureProcessRetryInvocationOrdinal(options)
	}
}

func deferQuarantinedRaceFirstAttempt(options *runTestWithRetryOptions) {
	if options == nil {
		return
	}
	if options.t == nil || options.testInfo == nil || options.processRetryCoordinator == nil {
		runTestWithRetry(options)
		return
	}
	identity, ok := cloneDeferredTestIdentity(options.processRetryIdentity)
	if !ok {
		runTestWithRetry(options)
		return
	}
	execMeta := &testExecutionMetadata{}
	if options.preExecMetaAdjust != nil {
		options.preExecMetaAdjust(execMeta, 0)
	}
	testInfo := &testingTInfo{commonInfo: *options.testInfo}
	itrDecision := currentITRState().decisionFor(testInfo, execMeta, integrations.IsTestFuncUnskippable(options.testInfo.sourceFunc))
	if itrDecision.skip {
		originalExecMeta := getTestMetadata(options.t)
		if originalExecMeta == nil {
			originalExecMeta = createTestMetadata(options.t, nil)
			defer deleteTestMetadata(options.t)
		}
		if options.preExecMetaAdjust != nil {
			options.preExecMetaAdjust(originalExecMeta, 0)
		}
		options.targetFunc(options.t)
		return
	}
	if itrDecision.forcedRun {
		execMeta.isItrForcedRun = true
	}
	execMeta.identity = &identity
	execMeta.hasAdditionalFeatureWrapper = true
	execOpts := &executionOptions{options: options, executionMetadata: execMeta}
	prepareDeferredProcessRetryInvocation(execOpts)
	if ok, _ := processRetryParallelBaselineReady(execOpts.processRetryLaunchBaseline); !ok || options.processRetryPhaseID == 0 {
		runTestWithRetry(options)
		return
	}
	metadata := snapshotProcessRetryExecutionMetadata(execMeta)
	if metadata == nil {
		runTestWithRetry(options)
		return
	}
	admission := options.processRetryCoordinator.beginAdmission()
	if admission == nil {
		runTestWithRetry(options)
		return
	}
	lease, err := acquireProcessRetryGroupLease()
	if err != nil {
		admission.abort()
		runTestWithRetry(options)
		return
	}
	parentDeadline, parentDeadlineOK := options.t.Deadline()
	nativeMaxParallel, _ := retryAttemptNativeMaxParallel(options.t)
	module := session.GetOrCreateModule(options.testInfo.moduleName)
	suite := module.GetOrCreateSuite(options.testInfo.suiteName)
	group := &deferredProcessRetryGroup{
		identity:              identity,
		testInfo:              *options.testInfo,
		metadata:              *metadata,
		launchBaseline:        execOpts.processRetryLaunchBaseline,
		lease:                 lease,
		parentDeadline:        parentDeadline,
		parentDeadlineOK:      parentDeadlineOK,
		nativeMaxParallel:     nativeMaxParallel,
		mRunEpoch:             options.processRetryMRunEpoch,
		phaseID:               options.processRetryPhaseID,
		invocationOrdinal:     ensureProcessRetryInvocationOrdinal(options),
		executionIndex:        0,
		module:                module,
		suite:                 suite,
		efdFaultySessionGuard: options.efdFaultySessionGuard,
		deferredFirstAttempt:  true,
	}
	group.metadata.identity = &group.identity
	group.testInfo.identity = &group.identity
	infrastructureFailure := false
	if options.quarantinedRaceNativeOrder {
		firstAttempt := options.processRetryCoordinator.waitNativeScheduledFirstAttempt(group, options.t)
		group.firstAttemptResult = &firstAttempt
		infrastructureFailure = deferredProcessRetryInfrastructureFailure(firstAttempt)
	}
	if !admission.commit(group) {
		lease.release()
		runTestWithRetry(options)
		return
	}
	finishDeferredProcessRetryFirstAttempt(options, infrastructureFailure)
}

func deferredProcessRetryInfrastructureFailure(attempt processRetryAttemptResult) bool {
	switch effectiveProcessRetryStatus(attempt, false).FailureKind {
	case "", "test_fail", "test_panic", "test_race":
		return false
	default:
		return true
	}
}

func finishDeferredProcessRetryFirstAttempt(options *runTestWithRetryOptions, infrastructureFailure bool) {
	if infrastructureFailure && retryAttemptFailfastEnabled() {
		options.t.FailNow()
	}
	options.t.SkipNow()
}

func enqueueDeferredProcessRetryGroup(execOpts *executionOptions) bool {
	if execOpts == nil || execOpts.options == nil || execOpts.executionMetadata == nil {
		return false
	}
	options := execOpts.options
	coordinator := options.processRetryCoordinator
	if coordinator == nil || execOpts.lastObservation.stopsRetryContinuation() {
		return false
	}
	if ok, _ := processRetryEligible(execOpts.executionMetadata, options); !ok {
		return false
	}
	if ok, _ := processRetryParallelBaselineReady(execOpts.processRetryLaunchBaseline); !ok {
		return false
	}
	if execOpts.executionMetadata.retryAttemptFinalizer == nil {
		return false
	}
	parallelEFD := shouldUseParallelEFD(
		options,
		execOpts.executionMetadata,
		execOpts.retryCount+1,
		processRetryParallelMaxConcurrencyForBaseline(execOpts.processRetryLaunchBaseline),
	)
	identity, ok := cloneDeferredTestIdentity(options.processRetryIdentity)
	if !ok || options.testInfo == nil {
		return false
	}
	metadata := snapshotProcessRetryExecutionMetadata(execOpts.executionMetadata)
	if metadata == nil {
		return false
	}
	metadataCopy := *metadata
	testInfo := *options.testInfo
	panicMessage := execOpts.lastObservation.panicMessage
	if execOpts.lastObservation.panicData != nil && panicMessage == "" {
		panicMessage = fmt.Sprint(execOpts.lastObservation.panicData)
	}

	admission := coordinator.beginAdmission()
	if admission == nil {
		return false
	}
	lease, err := acquireProcessRetryGroupLease()
	if err != nil {
		admission.abort()
		return false
	}
	parentDeadline, parentDeadlineOK := options.t.Deadline()
	nativeMaxParallel := 0
	if execOpts.lastObservation.rootParallel {
		nativeMaxParallel, _ = retryAttemptNativeMaxParallel(options.t)
	}
	group := &deferredProcessRetryGroup{
		identity:          identity,
		testInfo:          testInfo,
		metadata:          metadataCopy,
		launchBaseline:    execOpts.processRetryLaunchBaseline,
		lease:             lease,
		parentDeadline:    parentDeadline,
		parentDeadlineOK:  parentDeadlineOK,
		mRunEpoch:         options.processRetryMRunEpoch,
		phaseID:           options.processRetryPhaseID,
		invocationOrdinal: ensureProcessRetryInvocationOrdinal(options),
		executionIndex:    execOpts.executionIndex,
		retryCount:        execOpts.retryCount,
		reservation:       execOpts.flakyRetryBudgetReservation,
		latest: retryAttemptObservation{
			executionIndex: execOpts.lastObservation.executionIndex,
			failed:         execOpts.lastObservation.failed,
			skipped:        execOpts.lastObservation.skipped,
			duration:       execOpts.lastObservation.duration,
			raceDetected:   execOpts.lastObservation.raceDetected,
			rootParallel:   execOpts.lastObservation.rootParallel,
		},
		module:                execOpts.module,
		suite:                 execOpts.suite,
		panicPresent:          execOpts.lastObservation.panicData != nil,
		panicMessage:          panicMessage,
		panicStack:            string(execOpts.lastObservation.panicStack),
		parallelEFD:           parallelEFD,
		rootParallel:          execOpts.lastObservation.rootParallel,
		nativeMaxParallel:     nativeMaxParallel,
		efdFaultySessionGuard: options.efdFaultySessionGuard,
	}
	group.metadata.identity = &group.identity
	group.testInfo.identity = &group.identity
	group.tailEvent = &deferredProcessRetryEvent{
		event:     execOpts.executionMetadata.test,
		admission: admission,
		group:     group,
	}
	execOpts.executionMetadata.deferredRetryEvent = group.tailEvent
	group.outcomes.observe(execOpts.lastObservation.failed, execOpts.lastObservation.skipped)
	// The pending admission owns any FTR reservation admitted after the first
	// attempt. It becomes visible to the coordinator only after the first event
	// finalizer publishes its immutable close disposition.
	execOpts.flakyRetryBudgetReservation = &flakyRetryBudgetReservation{}
	return true
}

func (g *deferredProcessRetryGroup) prepareAttempt(allowEFDTransition bool) (deferredProcessRetryPreparedAttempt, bool, bool) {
	if g == nil || g.retryCount < 0 {
		return deferredProcessRetryPreparedAttempt{}, false, false
	}
	allowed, stopped := g.admitEarlyFlakeDetectionContinuationWithTransition(allowEFDTransition)
	if !allowed {
		return deferredProcessRetryPreparedAttempt{}, false, stopped
	}
	execMeta := &testExecutionMetadata{}
	if !applyProcessRetryMetadataSnapshot(execMeta, &g.metadata) {
		g.cancel("missing_metadata_snapshot", true)
		return deferredProcessRetryPreparedAttempt{}, false, false
	}
	g.executionIndex++
	execMeta.identity = &g.identity
	execMeta.isARetry = true
	execMeta.hasAdditionalFeatureWrapper = true
	execMeta.allAttemptsPassed = g.outcomes.allAttemptsPassed()
	execMeta.allRetriesFailed = g.outcomes.allRetriesFailed()
	execMeta.anyExecutionPassed = g.outcomes.anyPassed()
	execMeta.anyExecutionFailed = g.outcomes.anyFailed()
	execMeta.remainingRetries = g.retryCount
	execMeta.isEfdInParallel = g.parallelEFD
	last, recognized := retryExecutionIsLast(execMeta, g.retryCount, flakyRetryBudgetRemaining(integrations.GetFlakyRetriesSettings()))
	execMeta.isLastRetry = !recognized || last
	retryReason, ok := processRetryReasonForExecution(execMeta)
	if !ok {
		g.cancel("missing_retry_reason", true)
		return deferredProcessRetryPreparedAttempt{}, false, false
	}
	consumeDeferredFlakyRetryReservation(g)
	return deferredProcessRetryPreparedAttempt{index: g.executionIndex, retryReason: retryReason, execMeta: execMeta}, true, false
}

func runDeferredProcessRetryAttempt(ctx context.Context, group *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
	return runProcessRetryAttemptWithBaselineAndShutdown(
		ctx,
		processRetryChildConfig{
			TestName:          group.identity.FullName,
			Attempt:           prepared.index,
			RetryReason:       prepared.retryReason,
			MRunEpoch:         group.mRunEpoch,
			InvocationOrdinal: group.invocationOrdinal,
		},
		group.parentDeadline,
		group.parentDeadlineOK,
		group.launchBaseline,
		group.shutdown(),
	)
}

func (g *deferredProcessRetryGroup) applyCompletedAttempt(completed deferredProcessRetryCompletedAttempt, decideContinuation, continuationAdmitted bool) bool {
	attempt := completed.result
	execMeta := completed.prepared.execMeta
	// Queue admission commits this continuation to the process backend. By the
	// time a late setup failure is observable, native M.Run and the original
	// testing.T have completed, so represent the admitted continuation exactly once.
	terminal := deferredProcessRetryAttemptTerminal(&g.metadata, attempt)
	continueGroup := continuationAdmitted && !terminal
	effective, nextTail := deferProcessRetryTestEventWithAdmission(&g.testInfo, execMeta, attempt, func(effective processRetryEffectiveStatus) {
		g.retryCount--
		g.outcomes.observe(effective.Failed, effective.Skipped)
		execMeta.allAttemptsPassed = g.outcomes.allAttemptsPassed()
		execMeta.allRetriesFailed = g.outcomes.allRetriesFailed()
		execMeta.anyExecutionPassed = g.outcomes.anyPassed()
		execMeta.anyExecutionFailed = g.outcomes.anyFailed()
		if decideContinuation {
			continueGroup = !terminal && deferredProcessRetryShouldContinue(execMeta, effective.Failed, effective.Skipped, g.retryCount)
			if continueGroup {
				continueGroup = g.admitEarlyFlakeDetectionContinuation()
			}
			if continueGroup && usesFlakyRetryBudget(execMeta) && g.reservation == nil {
				g.reservation = &flakyRetryBudgetReservation{}
				continueGroup = g.reservation.reserve()
			}
		}
		execMeta.retryContinuationDecided = true
		execMeta.retryContinuationAdmitted = continueGroup
		execMeta.isLastRetry = !continueGroup
	})
	if nextTail != nil {
		g.closeTailEvent(false)
		g.tailEvent = nextTail
	}
	g.latest = retryAttemptObservation{
		executionIndex: completed.prepared.index,
		failed:         effective.Failed,
		skipped:        effective.Skipped,
		duration:       max(attempt.FinishTime.Sub(attempt.StartTime), 0),
		raceDetected:   attempt.Result.RaceDetected,
		rootParallel:   attempt.Result.RootParallel,
	}
	if attempt.Result.Panic && !g.panicPresent {
		g.panicPresent = true
		g.panicMessage = attempt.Result.ErrorMessage
		g.panicStack = attempt.Result.ErrorStack
	}
	if attempt.Cleanup != nil {
		attempt.Cleanup()
	}
	return continueGroup
}

func (g *deferredProcessRetryGroup) admitEarlyFlakeDetectionContinuation() bool {
	allowed, _ := g.admitEarlyFlakeDetectionContinuationWithTransition(true)
	return allowed
}

func (g *deferredProcessRetryGroup) admitEarlyFlakeDetectionContinuationWithTransition(allowTransition bool) (bool, bool) {
	if g == nil || !usesEfdRetrySemanticsSnapshot(&g.metadata) || g.efdFaultySessionGuard == nil {
		return true, false
	}
	if g.efdFaultySessionGuard.retryState() == earlyFlakeDetectionAdmissionAllowed {
		return true, false
	}
	if !allowTransition {
		return false, true
	}
	transitionMeta := &testExecutionMetadata{
		isFlakyTestRetriesEnabled: g.metadata.isFlakyTestRetriesEnabled,
		efdFellBackToFlakyRetries: g.metadata.efdFellBackToFlakyRetries,
	}
	if retryCount, ok := transitionSuppressedEFDToFlakyRetries(transitionMeta, g.latest.failed, g.outcomes.anyPassed()); ok {
		g.metadata.efdFellBackToFlakyRetries = transitionMeta.efdFellBackToFlakyRetries
		g.retryCount = retryCount
		g.parallelEFD = false
		if g.retryCount < 0 {
			return false, true
		}
		g.reservation = &flakyRetryBudgetReservation{}
		return g.reservation.reserve(), true
	}
	return false, true
}

func (g *deferredProcessRetryGroup) shutdown() <-chan struct{} {
	if g == nil || g.lease == nil {
		return nil
	}
	return g.lease.shutdown
}

func deferredProcessRetryAttemptTerminal(metadata *processRetryMetadataSnapshot, attempt processRetryAttemptResult) bool {
	effective := effectiveProcessRetryStatus(attempt, false)
	// Attempt-to-fix owns a fixed execution count, so a test race is an outcome
	// rather than an infrastructure failure that truncates its continuations.
	if effective.FailureKind == "test_race" && metadata != nil && metadata.isAttemptToFix && metadata.shouldOrchestrateAttemptToFix {
		return false
	}
	return attempt.TimedOut || attempt.Unreaped || attempt.ContainmentLost || attempt.Result.RaceDetected ||
		errorsIsDeferredTerminal(attempt.Err) || processRetryFailureStopsContinuation(effective.FailureKind)
}

func errorsIsDeferredTerminal(err error) bool {
	return err != nil && (errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, errProcessRetryChildUnreaped) ||
		errors.Is(err, errProcessRetryContainmentLost) ||
		errors.Is(err, errProcessRetryLaunchDisabled) ||
		errors.Is(err, errProcessRetryShutdown))
}

func deferredProcessRetryShouldContinue(meta *testExecutionMetadata, failed, skipped bool, remaining int64) bool {
	remainingBudget := int64(0)
	if usesFlakyRetryBudget(meta) {
		remainingBudget = flakyRetryBudgetRemaining(integrations.GetFlakyRetriesSettings())
	}
	return willRetryAfterExecution(failed, skipped, meta, remaining, remainingBudget)
}

func consumeDeferredFlakyRetryReservation(group *deferredProcessRetryGroup) {
	if group == nil || group.reservation == nil {
		return
	}
	group.reservation.consume()
	group.reservation = nil
}

func (g *deferredProcessRetryGroup) packageFailed() bool {
	if g == nil || g.metadata.isDisabled || g.metadata.isQuarantined {
		return false
	}
	if g.terminalFailure {
		return true
	}
	if g.metadata.isAttemptToFix {
		return g.outcomes.anyFailed()
	}
	if usesEfdRetrySemanticsSnapshot(&g.metadata) {
		return !g.outcomes.anyPassed() && g.outcomes.anyFailed()
	}
	return g.latest.failed
}

func usesEfdRetrySemanticsSnapshot(snapshot *processRetryMetadataSnapshot) bool {
	return snapshot != nil &&
		snapshot.isEarlyFlakeDetectionEnabled &&
		!snapshot.isAttemptToFix &&
		(snapshot.isANewTest || snapshot.isAModifiedTest) &&
		!snapshot.efdFellBackToFlakyRetries
}

func (g *deferredProcessRetryGroup) cancel(reason string, failed bool) {
	if g == nil {
		return
	}
	g.terminalFailure = failed
	g.terminalFailureReason = reason
	g.truncated = true
	if g.reservation != nil {
		g.reservation.refund()
		g.reservation = nil
	}
	g.finish()
}

func (g *deferredProcessRetryGroup) finish() {
	if g == nil {
		return
	}
	g.closeTailEvent(true)
	if g.suite != nil && g.module != nil {
		checkModuleAndSuite(g.module, g.suite)
		g.suite = nil
		g.module = nil
	}
	if g.lease != nil {
		g.lease.release()
		g.lease = nil
	}
}

func (g *deferredProcessRetryGroup) closeTailEvent(final bool) {
	if g == nil || g.tailEvent == nil || g.tailEvent.closed || !g.tailEvent.ready || g.tailEvent.event == nil {
		return
	}
	if final {
		finalStatus := calculateFinalStatus(
			g.outcomes.anyPassed() && !g.terminalFailure,
			g.outcomes.anyFailed() || g.terminalFailure,
			g.latest.skipped,
			g.metadata.isQuarantined,
			g.metadata.isDisabled,
			g.metadata.isAttemptToFix,
		)
		g.tailEvent.event.SetTag(constants.TestFinalStatus, finalStatus)
		if g.metadata.isAttemptToFix {
			attemptToFixPassed := g.outcomes.allAttemptsPassed() && !g.truncated && !g.terminalFailure
			g.tailEvent.event.SetTag(constants.TestAttemptToFixPassed, strconv.FormatBool(attemptToFixPassed))
		}
		if g.executionIndex > 0 && g.tailEvent.failed && g.outcomes.allRetriesFailed() {
			g.tailEvent.event.SetTag(constants.TestHasFailedAllRetries, "true")
		}
	}
	options := make([]integrations.TestCloseOption, 0, 2)
	if !g.tailEvent.finishTime.IsZero() {
		options = append(options, integrations.WithTestFinishTime(g.tailEvent.finishTime))
	}
	if g.tailEvent.status == integrations.ResultStatusSkip && g.tailEvent.skipReason != "" {
		options = append(options, integrations.WithTestSkipReason(g.tailEvent.skipReason))
	}
	g.tailEvent.event.Close(g.tailEvent.status, options...)
	g.tailEvent.closed = true
}

func deferOrCloseInstrumentedTestEvent(
	execMeta *testExecutionMetadata,
	test integrations.Test,
	status integrations.TestResultStatus,
	skipReason string,
) {
	if execMeta != nil && execMeta.deferredRetryEvent != nil {
		execMeta.deferredRetryEvent.event = test
		execMeta.deferredRetryEvent.status = status
		execMeta.deferredRetryEvent.skipReason = skipReason
		execMeta.deferredRetryEvent.finishTime = time.Now()
		execMeta.deferredRetryEvent.failed = status == integrations.ResultStatusFail
		execMeta.deferredRetryEvent.ready = true
		return
	}
	if status == integrations.ResultStatusSkip && skipReason != "" {
		test.Close(status, integrations.WithTestSkipReason(skipReason))
		return
	}
	test.Close(status)
}

func completeDeferredProcessRetryEvent(execMeta *testExecutionMetadata) {
	if execMeta == nil || execMeta.deferredRetryEvent == nil {
		return
	}
	event := execMeta.deferredRetryEvent
	admission := event.admission
	if admission == nil {
		return
	}
	group := event.group
	event.admission = nil
	event.group = nil
	if event.ready && admission.commit(group) {
		return
	}
	admission.abort()
	if group != nil {
		if group.reservation != nil {
			group.reservation.refund()
			group.reservation = nil
		}
		if group.lease != nil {
			group.lease.release()
			group.lease = nil
		}
		group.tailEvent = nil
	}
	execMeta.deferredRetryEvent = nil
}

func (g *deferredProcessRetryGroup) printSummary() {
	if g == nil || g.executionIndex <= 0 {
		return
	}
	status := "passed"
	if g.packageFailed() {
		status = "failed"
	} else if !g.outcomes.anyPassed() && g.outcomes.skipped > 0 {
		status = "skipped"
	}
	reason := "auto test retries"
	if usesEfdRetrySemanticsSnapshot(&g.metadata) {
		reason = "early flake detection"
	} else if g.metadata.isAttemptToFix {
		reason = "attempt to fix"
	}
	fmt.Printf("  [ %s after %d retries by Datadog's %s ]\n", status, g.executionIndex, reason)
}
