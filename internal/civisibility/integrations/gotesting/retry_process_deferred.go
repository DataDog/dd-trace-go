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
	"slices"
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
	panicStack             []byte
	cleanupPanicData       any
	terminalTrace          []retryAttemptTerminal
	rootParallel           bool
}

func observeFreshRetryAttempt(executionIndex int, result retryAttemptResult) retryAttemptObservation {
	return retryAttemptObservation{
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
	anyPassed             bool
	anyFailed             bool
	allAttemptsPassed     bool
	allRetriesFailed      bool
	skipCount             int
	module                integrations.TestModule
	suite                 integrations.TestSuite
	terminalFailure       bool
	terminalFailureReason string
	truncated             bool
	tailEvent             *deferredProcessRetryEvent
	panicData             any
	panicStack            string
	parallelEFD           bool
	rootParallel          bool
	nativeMaxParallel     int
}

type deferredProcessRetryEvent struct {
	event      integrations.Test
	status     integrations.TestResultStatus
	skipReason string
	finishTime time.Time
	failed     bool
	ready      bool
	closed     bool
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
	drainGroup       func(*deferredProcessRetryGroup) bool
	attemptRunner    deferredProcessRetryAttemptRunner
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
	group       *deferredProcessRetryGroup
	ctx         context.Context
	cancel      context.CancelFunc
	active      int
	remaining   int64
	completed   []deferredProcessRetryCompletedAttempt
	activated   bool
	done        bool
	stopPending bool
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

func newProcessRetryCoordinator() *processRetryCoordinator {
	return &processRetryCoordinator{
		state:           processRetryCoordinatorAccepting,
		changed:         make(chan struct{}),
		shutdown:        make(chan struct{}),
		completed:       make(chan struct{}),
		failfastEnabled: retryAttemptFailfastEnabled,
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
	<-c.completed
	c.mu.Lock()
	summary := c.summary
	c.mu.Unlock()
	return summary
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
		c.complete(processRetryFailureExitCode, true)
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
	<-c.completed
	c.mu.Lock()
	summary := c.summary
	c.mu.Unlock()
	return summary
}

func (c *processRetryCoordinator) complete(nativeExitCode int, shuttingDown bool) {
	queue := c.seal()
	slices.SortStableFunc(queue, func(a, b *deferredProcessRetryGroup) int {
		return cmp.Compare(a.invocationOrdinal, b.invocationOrdinal)
	})
	packageFailed := nativeExitCode != 0
	deferredFailed := false
	failfastLatched := c.failfastEnabled != nil && c.failfastEnabled() && packageFailed
	var terminalPanic any
	if !shuttingDown && !c.shutdownRequested() {
		if c.drainGroup != nil {
			deferredFailed, failfastLatched, terminalPanic = c.completeWithDrainHook(queue, failfastLatched)
		} else if !failfastLatched {
			outcome := c.drainScheduledGroups(queue)
			deferredFailed = outcome.deferredFailed
			failfastLatched = outcome.failfast
			terminalPanic = outcome.terminalPanic
		} else {
			cancelDeferredProcessRetryGroups(queue, "failfast", false)
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

func (c *processRetryCoordinator) completeWithDrainHook(queue []*deferredProcessRetryGroup, failfastLatched bool) (bool, bool, any) {
	deferredFailed := false
	var terminalPanic any
	for _, group := range queue {
		if terminalPanic != nil {
			group.cancel("terminal_replay", false)
			continue
		}
		if failfastLatched {
			group.cancel("failfast", false)
			continue
		}
		groupFailed := c.drainGroup(group)
		deferredFailed = groupFailed || deferredFailed
		if groupFailed && c.failfastEnabled != nil && c.failfastEnabled() {
			failfastLatched = true
		}
		if groupFailed && group.panicData != nil {
			terminalPanic = deferredProcessRetryTerminalPanic(group)
		}
	}
	return deferredFailed, failfastLatched, terminalPanic
}

func cancelDeferredProcessRetryGroups(groups []*deferredProcessRetryGroup, reason string, failed bool) {
	for _, group := range groups {
		group.cancel(reason, failed)
	}
}

func (c *processRetryCoordinator) drainScheduledGroups(queue []*deferredProcessRetryGroup) deferredProcessRetryDrainOutcome {
	runner := c.attemptRunner
	if runner == nil {
		runner = runDeferredProcessRetryAttempt
	}
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
			cancelDeferredProcessRetryBatches(batches[index+1:], batch.stopReason, true)
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

func cancelDeferredProcessRetryBatches(batches [][]*deferredProcessRetryGroup, reason string, failed bool) {
	for _, groups := range batches {
		cancelDeferredProcessRetryGroups(groups, reason, failed)
	}
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
		if groupFailed && c.failfastEnabled != nil && c.failfastEnabled() {
			outcome.failfast = true
		}
		if groupFailed && state.group.panicData != nil && outcome.terminalPanic == nil {
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
			prepared, ok := state.group.prepareAttempt()
			if !ok {
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
			if deferredProcessRetryAttemptTerminal(scheduled.completed.result) {
				state.stopPending = true
				state.remaining = 0
				state.group.truncated = true
				state.cancel()
			}
			if state.active == 0 && (state.remaining == 0 || state.stopPending) {
				slices.SortFunc(state.completed, func(a, b deferredProcessRetryCompletedAttempt) int {
					return cmp.Compare(a.prepared.index, b.prepared.index)
				})
				for resultIndex, completed := range state.completed {
					state.group.applyCompletedAttempt(completed, false, resultIndex+1 < len(state.completed))
				}
				finishState(scheduled.groupIndex)
			}
		} else {
			continueGroup := state.group.applyCompletedAttempt(scheduled.completed, !state.stopPending, false)
			if c.failfastEnabled != nil && c.failfastEnabled() && state.group.metadata.isAttemptToFix && state.group.anyFailed {
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
		group.panicData,
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

func processRetryCoordinatorRegistered(c *processRetryCoordinator) bool {
	processRetryCoordinatorRegistry.mu.Lock()
	defer processRetryCoordinatorRegistry.mu.Unlock()
	_, ok := processRetryCoordinatorRegistry.values[c]
	return ok
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
	if execOpts == nil || execOpts.options == nil || execOpts.executionMetadata == nil {
		return
	}
	options := execOpts.options
	if ok, _ := processRetryEligible(execOpts.executionMetadata, options); !ok {
		return
	}
	if ok, _ := processRetryParallelBaselineReady(execOpts.processRetryLaunchBaseline); !ok {
		return
	}
	options.processRetryPhaseID = options.processRetryCoordinator.observeInvocationPhase(options.processRetryIdentity)
	if options.processRetryPhaseID != 0 {
		ensureProcessRetryInvocationOrdinal(options)
	}
}

func enqueueDeferredProcessRetryGroup(execOpts *executionOptions) bool {
	if execOpts == nil || execOpts.options == nil || execOpts.executionMetadata == nil {
		return false
	}
	options := execOpts.options
	coordinator := options.processRetryCoordinator
	if coordinator == nil || !options.processRetryDeferredAllowed || execOpts.lastObservation.stopsRetryContinuation() {
		return false
	}
	if ok, _ := processRetryEligible(execOpts.executionMetadata, options); !ok {
		return false
	}
	if ok, _ := processRetryParallelBaselineReady(execOpts.processRetryLaunchBaseline); !ok {
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
		latest:            execOpts.lastObservation,
		allAttemptsPassed: true,
		allRetriesFailed:  true,
		module:            execOpts.module,
		suite:             execOpts.suite,
		parallelEFD:       parallelEFD,
		rootParallel:      execOpts.lastObservation.rootParallel,
		nativeMaxParallel: nativeMaxParallel,
	}
	group.metadata.identity = &group.identity
	group.testInfo.identity = &group.identity
	group.tailEvent = &deferredProcessRetryEvent{event: execOpts.executionMetadata.test}
	execOpts.executionMetadata.deferredRetryEvent = group.tailEvent
	group.observe(execOpts.lastObservation.failed, execOpts.lastObservation.skipped)
	if !admission.commit(group) {
		execOpts.executionMetadata.deferredRetryEvent = nil
		group.tailEvent = nil
		lease.release()
		return false
	}
	// The queue now owns any FTR reservation admitted after the first attempt.
	execOpts.flakyRetryBudgetReservation = &flakyRetryBudgetReservation{}
	return true
}

func (g *deferredProcessRetryGroup) prepareAttempt() (deferredProcessRetryPreparedAttempt, bool) {
	if g == nil || g.retryCount < 0 {
		return deferredProcessRetryPreparedAttempt{}, false
	}
	execMeta := &testExecutionMetadata{}
	if !applyProcessRetryMetadataSnapshot(execMeta, &g.metadata) {
		g.cancel("missing_metadata_snapshot", true)
		return deferredProcessRetryPreparedAttempt{}, false
	}
	g.executionIndex++
	execMeta.identity = &g.identity
	execMeta.isARetry = true
	execMeta.hasAdditionalFeatureWrapper = true
	execMeta.allAttemptsPassed = g.allAttemptsPassed
	execMeta.allRetriesFailed = g.allRetriesFailed
	execMeta.anyExecutionPassed = g.anyPassed
	execMeta.anyExecutionFailed = g.anyFailed
	execMeta.remainingRetries = g.retryCount
	execMeta.isEfdInParallel = g.parallelEFD
	execMeta.isLastRetry = deferredProcessRetryIsLast(execMeta, g.retryCount)
	retryReason, ok := processRetryReasonForExecution(execMeta)
	if !ok {
		g.cancel("missing_retry_reason", true)
		return deferredProcessRetryPreparedAttempt{}, false
	}
	consumeDeferredFlakyRetryReservation(g)
	return deferredProcessRetryPreparedAttempt{index: g.executionIndex, retryReason: retryReason, execMeta: execMeta}, true
}

func runDeferredProcessRetryAttempt(ctx context.Context, group *deferredProcessRetryGroup, prepared deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
	return runProcessRetryAttemptWithCoordinator(
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
		nil,
	)
}

func (g *deferredProcessRetryGroup) applyCompletedAttempt(completed deferredProcessRetryCompletedAttempt, decideContinuation, continuationAdmitted bool) bool {
	attempt := completed.result
	execMeta := completed.prepared.execMeta
	if attempt.SetupFailure {
		attempt.SetupFailureConsumed = true
	}
	terminal := deferredProcessRetryAttemptTerminal(attempt)
	continueGroup := continuationAdmitted && !terminal
	effective, nextTail := deferProcessRetryTestEventWithAdmission(&g.testInfo, execMeta, attempt, func(effective processRetryEffectiveStatus) {
		g.retryCount--
		g.observe(effective.Failed, effective.Skipped)
		execMeta.allAttemptsPassed = g.allAttemptsPassed
		execMeta.allRetriesFailed = g.allRetriesFailed
		execMeta.anyExecutionPassed = g.anyPassed
		execMeta.anyExecutionFailed = g.anyFailed
		if decideContinuation {
			continueGroup = !terminal && deferredProcessRetryShouldContinue(execMeta, effective.Failed, effective.Skipped, g.retryCount)
			if continueGroup && usesFlakyRetryBudget(execMeta) {
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
	if attempt.Result.Panic && g.panicData == nil {
		g.panicData = attempt.Result.ErrorMessage
		g.panicStack = attempt.Result.ErrorStack
		g.latest.panicData = attempt.Result.ErrorMessage
	}
	if attempt.Cleanup != nil {
		attempt.Cleanup()
	}
	return continueGroup
}

func (g *deferredProcessRetryGroup) shutdown() <-chan struct{} {
	if g == nil || g.lease == nil {
		return nil
	}
	return g.lease.shutdown
}

func deferredProcessRetryAttemptTerminal(attempt processRetryAttemptResult) bool {
	effective := effectiveProcessRetryStatus(attempt, false)
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

func deferredProcessRetryIsLast(meta *testExecutionMetadata, remaining int64) bool {
	if meta == nil {
		return true
	}
	if meta.isAttemptToFix && meta.shouldOrchestrateAttemptToFix || usesEfdRetrySemantics(meta) {
		return remaining == 1
	}
	if meta.isFlakyTestRetriesEnabled {
		return remaining == 1 || flakyRetryBudgetRemaining(integrations.GetFlakyRetriesSettings()) == 0
	}
	return true
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

func (g *deferredProcessRetryGroup) observe(failed, skipped bool) {
	if failed || skipped {
		g.allAttemptsPassed = false
	}
	if !failed {
		g.allRetriesFailed = false
	}
	switch {
	case failed:
		g.anyFailed = true
	case skipped:
		g.skipCount++
	default:
		g.anyPassed = true
	}
}

func (g *deferredProcessRetryGroup) packageFailed() bool {
	if g == nil || g.metadata.isDisabled || g.metadata.isQuarantined {
		return false
	}
	if g.terminalFailure {
		return true
	}
	if g.metadata.isAttemptToFix {
		return g.anyFailed
	}
	if usesEfdRetrySemanticsSnapshot(&g.metadata) {
		return !g.anyPassed && g.anyFailed
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
	g.closeTailEvent(true)
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
			g.anyPassed,
			g.anyFailed || g.terminalFailure,
			g.latest.skipped,
			g.metadata.isQuarantined,
			g.metadata.isDisabled,
			g.metadata.isAttemptToFix,
		)
		g.tailEvent.event.SetTag(constants.TestFinalStatus, finalStatus)
		if g.metadata.isAttemptToFix {
			attemptToFixPassed := g.allAttemptsPassed && !g.truncated && !g.terminalFailure
			g.tailEvent.event.SetTag(constants.TestAttemptToFixPassed, fmt.Sprint(attemptToFixPassed))
		}
		if g.tailEvent.failed && g.allRetriesFailed {
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

func (g *deferredProcessRetryGroup) printSummary() {
	if g == nil || g.executionIndex <= 0 {
		return
	}
	status := "passed"
	if g.packageFailed() {
		status = "failed"
	} else if !g.anyPassed && g.skipCount > 0 {
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
