// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

type retryAttemptCompletionPhase uint8

const (
	retryAttemptCompletionUnknown retryAttemptCompletionPhase = iota
	retryAttemptCompletionNormal
	retryAttemptCompletionPanic
	retryAttemptCompletionUnexpectedGoexit
)

type retryAttemptFailureCheckpointPhase uint8

const (
	retryAttemptNotFailed retryAttemptFailureCheckpointPhase = iota
	retryAttemptFailurePreCheckpoint
	retryAttemptFailurePostCheckpoint
)

type retryAttemptCleanupObservation uint32

const (
	retryAttemptCleanupUnknown retryAttemptCleanupObservation = iota
	retryAttemptCleanupReturned
	retryAttemptCleanupPanicked
	retryAttemptCleanupFailNowObserved
	retryAttemptCleanupSkipNowObserved
	retryAttemptCleanupGoexitAmbiguous
)

type retryAttemptTerminalKind uint8

const (
	retryAttemptTerminalBodyPanic retryAttemptTerminalKind = iota + 1
	retryAttemptTerminalBodyFailNow
	retryAttemptTerminalBodySkipNow
	retryAttemptTerminalBodyGoexit
	retryAttemptTerminalCleanupPanic
	retryAttemptTerminalCleanupFailNow
	retryAttemptTerminalCleanupSkipNow
	retryAttemptTerminalCleanupGoexit
	retryAttemptTerminalSynthesizedGoexit
)

type retryAttemptTerminal struct {
	kind  retryAttemptTerminalKind
	value any
	stack []byte
}

type retryAttemptResult struct {
	failed                 bool
	skipped                bool
	finished               bool
	done                   bool
	ran                    bool
	reportExecuted         bool
	nativeSignalExecuted   bool
	nativeFatalRequired    bool
	nativeFatalTraceReplay bool
	raceDetected           bool
	raceCheckpointCount    int64
	duration               time.Duration
	completionPhase        retryAttemptCompletionPhase
	failureCheckpointPhase retryAttemptFailureCheckpointPhase
	panicData              any
	panicStack             []byte
	cleanupPanicData       any
	cleanupPanicStack      []byte
	cleanupObservation     retryAttemptCleanupObservation
	parallelLeaseHeld      bool
	schedulerSlotReleased  bool
	terminalTrace          []retryAttemptTerminal
	output                 []byte
	nativeOutput           []byte
}

var errRetryAttemptNilPanicOrGoexit = errors.New("test executed panic(nil) or runtime.Goexit")

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

func runFreshRetryAttemptInGroupWithCallbacks(
	group *retryAttemptGroup,
	prepare func(*retryAttemptRoot) string,
	target func(*testing.T),
	complete func(*retryAttemptRoot, retryAttemptResult),
) (*retryAttemptRoot, retryAttemptResult, string) {
	if group == nil {
		return nil, retryAttemptResult{}, "missing_retry_attempt_group"
	}
	group.executionMu.Lock()
	defer group.executionMu.Unlock()
	group.matcher.restore()
	defer group.matcher.restore()

	attempt, reason := newRetryAttemptRootInGroup(group)
	if reason != "" {
		return nil, retryAttemptResult{}, reason
	}
	if prepare != nil {
		if reason = prepare(attempt); reason != "" {
			attempt.cancelContexts()
			return nil, retryAttemptResult{}, reason
		}
	}

	resultCh := make(chan retryAttemptResult, 1)
	go runFreshRetryAttemptOwner(attempt, target, resultCh)

	finish := func(result retryAttemptResult) (*retryAttemptRoot, retryAttemptResult, string) {
		reconcileRetryAttemptCallerLease(attempt, result)
		reconcileRetryAttemptRaceBaselines(attempt, result.raceCheckpointCount)
		if complete != nil {
			complete(attempt, result)
		}
		return attempt, result, ""
	}
	select {
	case result := <-resultCh:
		return finish(result)
	case <-attempt.testSignal():
		base := commonBaseForTest(attempt.test, attempt.layout)
		if !*fieldPtr[bool](base, attempt.layout.common.isParallel) {
			return finish(<-resultCh)
		}
		attempt.beginRootParallelSchedulerTransfer()
		result := <-resultCh
		if !result.parallelLeaseHeld {
			testingTestStateWaitParallel(getTestState(attempt.test))
		}
		attempt.finishRootParallelSchedulerTransfer(result.duration)
		reconcileRetryAttemptRaceBaselines(attempt, result.raceCheckpointCount)
		if complete != nil {
			complete(attempt, result)
		}
		return attempt, result, ""
	}
}

func reconcileRetryAttemptCallerLease(attempt *retryAttemptRoot, result retryAttemptResult) {
	if result.schedulerSlotReleased && !result.parallelLeaseHeld {
		testingTestStateWaitParallel(getTestState(attempt.test))
	}
}

func reconcileRetryAttemptRaceBaselines(attempt *retryAttemptRoot, raceErrors int64) {
	if attempt == nil || raceErrors <= 0 {
		return
	}
	for base := commonBaseForTest(attempt.original, attempt.layout); base != nil; base = pointerWord(base, attempt.layout.common.parent) {
		advanceRetryAttemptRaceBaseline(fieldPtr[atomic.Int64](base, attempt.layout.common.lastRaceErrors), raceErrors)
	}
}

func advanceRetryAttemptRaceBaseline(baseline *atomic.Int64, raceErrors int64) {
	if baseline == nil {
		return
	}
	for {
		current := baseline.Load()
		if current >= raceErrors || baseline.CompareAndSwap(current, raceErrors) {
			return
		}
	}
}

func runFreshRetryAttemptOwner(attempt *retryAttemptRoot, target func(*testing.T), resultCh chan<- retryAttemptResult) {
	t := attempt.test
	layout := attempt.layout
	base := commonBaseForTest(t, layout)
	setRetryAttemptRunner(base, layout)
	initializeRetryAttemptStart(base, layout.common.start.unsafeField)

	defer finalizeFreshRetryAttempt(attempt, resultCh)
	defer func() {
		if len(*fieldPtr[[]*testing.T](base, layout.common.sub)) == 0 {
			runRetryAttemptCleanup(attempt, t, 0)
		}
	}()

	runRetryAttemptBody(attempt, t, target)
	mu := fieldPtr[sync.RWMutex](base, layout.common.mu)
	mu.Lock()
	*fieldPtr[bool](base, layout.common.finished) = true
	mu.Unlock()
}

func finalizeFreshRetryAttempt(attempt *retryAttemptRoot, resultCh chan<- retryAttemptResult) {
	t := attempt.test
	layout := attempt.layout
	base := commonBaseForTest(t, layout)
	result := retryAttemptResult{}
	published := false
	defer func() {
		if published {
			return
		}
		result.cleanupObservation = retryAttemptCleanupObservation(attempt.cleanupObservation.Load())
		if result.completionPhase == retryAttemptCompletionUnknown {
			result.completionPhase = retryAttemptCompletionNormal
		}
		snapshotRetryAttemptResult(attempt, base, layout, &result)
		attempt.freezeGenerationFailure()
		if result.panicData == nil {
			*fieldPtr[chan bool](base, layout.common.signal) <- true
			result.nativeSignalExecuted = true
		}
		resultCh <- result
	}()

	result.raceCheckpointCount, result.raceDetected = checkRetryAttemptRaces(base, layout)
	if t.Failed() {
		result.failureCheckpointPhase = retryAttemptFailurePreCheckpoint
	}

	recovered := recover()
	mu := fieldPtr[sync.RWMutex](base, layout.common.mu)
	mu.RLock()
	finished := *fieldPtr[bool](base, layout.common.finished)
	mu.RUnlock()
	if !finished && recovered == nil {
		recovered = errRetryAttemptNilPanicOrGoexit
		result.completionPhase = retryAttemptCompletionUnexpectedGoexit
		attempt.appendTerminal(retryAttemptTerminal{
			kind:  retryAttemptTerminalSynthesizedGoexit,
			value: recovered,
			stack: debug.Stack(),
		})
	}

	if recovered != nil {
		if result.failureCheckpointPhase == retryAttemptNotFailed {
			result.failureCheckpointPhase = retryAttemptFailurePostCheckpoint
		}
		t.Fail()
		result.panicData = recovered
		result.panicStack = debug.Stack()
		queuedSubtests := len(*fieldPtr[[]*testing.T](base, layout.common.sub)) > 0
		result.nativeFatalRequired = queuedSubtests
		if !queuedSubtests && retryAttemptTerminalTraceRequiresNativeReplay(attempt.terminalTraceSnapshot()) {
			result.nativeFatalRequired = true
			result.nativeFatalTraceReplay = true
		}
		if retryAttemptCleanupObservation(attempt.cleanupObservation.Load()) == retryAttemptCleanupPanicked {
			result.cleanupPanicData = recovered
			trace := attempt.terminalTraceSnapshot()
			for i := len(trace) - 1; i >= 0; i-- {
				if trace[i].kind == retryAttemptTerminalCleanupPanic {
					result.cleanupPanicStack = append([]byte(nil), trace[i].stack...)
					break
				}
			}
		}
		if result.completionPhase == retryAttemptCompletionUnknown {
			result.completionPhase = retryAttemptCompletionPanic
		}
		if cleanupPanic := runRetryAttemptCleanup(attempt, t, 1); cleanupPanic != nil {
			result.cleanupPanicData = cleanupPanic
			result.cleanupPanicStack = debug.Stack()
			if queuedSubtests {
				t.Logf("cleanup panicked with %v", cleanupPanic)
			}
		}
		result.cleanupObservation = retryAttemptCleanupObservation(attempt.cleanupObservation.Load())
		result.parallelLeaseHeld = attempt.parallelLeaseTransfer.Load() && *fieldPtr[bool](base, layout.common.isParallel)
		snapshotRetryAttemptResult(attempt, base, layout, &result)
		attempt.freezeGenerationFailure()
		resultCh <- result
		published = true
		return
	}

	addRetryAttemptElapsed(base, layout)
	if subtests := *fieldPtr[[]*testing.T](base, layout.common.sub); len(subtests) > 0 {
		testingTestStateRelease(getTestState(t))
		result.schedulerSlotReleased = true
		close(*fieldPtr[chan bool](base, layout.common.barrier))
		for _, subtest := range subtests {
			<-*fieldPtr[chan bool](commonBaseForTest(subtest, layout), layout.common.signal)
		}

		initializeRetryAttemptStart(base, layout.common.start.unsafeField)
		if cleanupPanic := runRetryAttemptCleanup(attempt, t, 1); cleanupPanic != nil {
			t.Fail()
			result.cleanupPanicData = cleanupPanic
			result.cleanupPanicStack = debug.Stack()
			result.completionPhase = retryAttemptCompletionPanic
			result.nativeFatalRequired = true
			if result.failureCheckpointPhase == retryAttemptNotFailed {
				result.failureCheckpointPhase = retryAttemptFailurePostCheckpoint
			}
			result.cleanupObservation = retryAttemptCleanupObservation(attempt.cleanupObservation.Load())
			snapshotRetryAttemptResult(attempt, base, layout, &result)
			attempt.freezeGenerationFailure()
			resultCh <- result
			published = true
			return
		}
		addRetryAttemptElapsed(base, layout)
		result.raceCheckpointCount, result.raceDetected = checkRetryAttemptRaces(base, layout)
		if result.raceDetected && result.failureCheckpointPhase == retryAttemptNotFailed {
			result.failureCheckpointPhase = retryAttemptFailurePostCheckpoint
		}
		if !*fieldPtr[bool](base, layout.common.isParallel) {
			testingTestStateWaitParallel(getTestState(t))
			result.schedulerSlotReleased = false
		}
	} else if *fieldPtr[bool](base, layout.common.isParallel) && !attempt.parallelLeaseTransfer.Load() {
		testingTestStateRelease(getTestState(t))
		result.schedulerSlotReleased = true
	}

	flushRetryAttemptPartial(base, layout)
	result.output = freezeRetryAttemptOutput(attempt, base, layout)
	reportRetryAttempt(base, layout)
	result.reportExecuted = true
	*fieldPtr[bool](base, layout.common.done) = true
	if !fieldPtr[atomic.Bool](base, layout.common.hasSub).Load() {
		setRetryAttemptRan(base, layout)
	}
	result.completionPhase = retryAttemptCompletionNormal
	result.cleanupObservation = retryAttemptCleanupObservation(attempt.cleanupObservation.Load())
	result.parallelLeaseHeld = retryAttemptParallelLeaseHeld(base, layout, attempt)
	snapshotRetryAttemptResult(attempt, base, layout, &result)
	attempt.freezeGenerationFailure()
	*fieldPtr[chan bool](base, layout.common.signal) <- true
	result.nativeSignalExecuted = true
	resultCh <- result
	published = true
}

func retryAttemptTerminalTraceRequiresNativeReplay(trace []retryAttemptTerminal) bool {
	var bodyTerminal bool
	for _, terminal := range trace {
		switch terminal.kind {
		case retryAttemptTerminalBodyPanic, retryAttemptTerminalBodyGoexit:
			bodyTerminal = true
		case retryAttemptTerminalCleanupPanic, retryAttemptTerminalCleanupGoexit:
			if bodyTerminal {
				return true
			}
		}
	}
	return false
}

func replayRetryAttemptNativeTerminalTrace(trace []retryAttemptTerminal) {
	filtered := make([]retryAttemptTerminal, 0, len(trace))
	for _, terminal := range trace {
		switch terminal.kind {
		case retryAttemptTerminalBodyPanic, retryAttemptTerminalBodyGoexit,
			retryAttemptTerminalCleanupPanic, retryAttemptTerminalCleanupGoexit:
			filtered = append(filtered, terminal)
		}
	}
	if len(filtered) == 0 {
		panic(errRetryAttemptNilPanicOrGoexit)
	}
	replayRetryAttemptNativeTerminal(filtered)
}

func replayRetryAttemptNativeTerminal(trace []retryAttemptTerminal) {
	terminal := trace[0]
	if len(trace) > 1 {
		defer replayRetryAttemptNativeTerminal(trace[1:])
	}
	switch terminal.kind {
	case retryAttemptTerminalBodyPanic, retryAttemptTerminalCleanupPanic:
		panic(terminal.value)
	case retryAttemptTerminalBodyGoexit, retryAttemptTerminalCleanupGoexit:
		runtime.Goexit()
	default:
		panic(errRetryAttemptNilPanicOrGoexit)
	}
}

func runRetryAttemptCleanup(attempt *retryAttemptRoot, t *testing.T, panicHandling int) (panicValue any) {
	skippedBefore := t.Skipped()
	returned := false
	defer func() {
		if returned {
			return
		}
		if recovered := recover(); recovered != nil {
			attempt.cleanupObservation.Store(uint32(retryAttemptCleanupPanicked))
			attempt.appendTerminal(retryAttemptTerminal{kind: retryAttemptTerminalCleanupPanic, value: recovered, stack: debug.Stack()})
			panic(recovered)
		}
		switch {
		case !skippedBefore && t.Skipped():
			attempt.cleanupObservation.Store(uint32(retryAttemptCleanupSkipNowObserved))
			attempt.appendTerminal(retryAttemptTerminal{kind: retryAttemptTerminalCleanupSkipNow, stack: debug.Stack()})
		default:
			attempt.cleanupObservation.Store(uint32(retryAttemptCleanupGoexitAmbiguous))
			attempt.appendTerminal(retryAttemptTerminal{kind: retryAttemptTerminalCleanupGoexit, stack: debug.Stack()})
		}
	}()
	panicValue = testingTRunCleanup(t, panicHandling)
	returned = true
	if panicValue != nil {
		attempt.cleanupObservation.Store(uint32(retryAttemptCleanupPanicked))
		attempt.appendTerminal(retryAttemptTerminal{kind: retryAttemptTerminalCleanupPanic, value: panicValue, stack: debug.Stack()})
	} else {
		attempt.cleanupObservation.Store(uint32(retryAttemptCleanupReturned))
	}
	return panicValue
}

func runRetryAttemptBody(attempt *retryAttemptRoot, t *testing.T, target func(*testing.T)) {
	returned := false
	defer func() {
		if returned {
			return
		}
		if recovered := recover(); recovered != nil {
			attempt.appendTerminal(retryAttemptTerminal{kind: retryAttemptTerminalBodyPanic, value: recovered, stack: debug.Stack()})
			panic(recovered)
		}
		switch {
		case retryAttemptTestFinished(t, attempt.layout) && t.Skipped():
			attempt.appendTerminal(retryAttemptTerminal{kind: retryAttemptTerminalBodySkipNow, stack: debug.Stack()})
		case retryAttemptTestFinished(t, attempt.layout) && t.Failed():
			attempt.appendTerminal(retryAttemptTerminal{kind: retryAttemptTerminalBodyFailNow, stack: debug.Stack()})
		default:
			attempt.appendTerminal(retryAttemptTerminal{kind: retryAttemptTerminalBodyGoexit, value: errRetryAttemptNilPanicOrGoexit, stack: debug.Stack()})
		}
	}()
	target(t)
	returned = true
}

func retryAttemptTestFinished(t *testing.T, layout *testingInternalsLayout) bool {
	base := commonBaseForTest(t, layout)
	mu := fieldPtr[sync.RWMutex](base, layout.common.mu)
	mu.RLock()
	defer mu.RUnlock()
	return *fieldPtr[bool](base, layout.common.finished)
}

func (r *retryAttemptRoot) appendTerminal(terminal retryAttemptTerminal) {
	r.terminalMu.Lock()
	defer r.terminalMu.Unlock()
	r.terminalTrace = append(r.terminalTrace, terminal)
}

func (r *retryAttemptRoot) terminalTraceSnapshot() []retryAttemptTerminal {
	if r == nil {
		return nil
	}
	r.terminalMu.Lock()
	defer r.terminalMu.Unlock()
	return cloneRetryAttemptTerminalTrace(r.terminalTrace)
}

func cloneRetryAttemptTerminalTrace(trace []retryAttemptTerminal) []retryAttemptTerminal {
	if trace == nil {
		return nil
	}
	cloned := make([]retryAttemptTerminal, len(trace))
	copy(cloned, trace)
	for i := range cloned {
		cloned[i].stack = append([]byte(nil), cloned[i].stack...)
	}
	return cloned
}

func retryAttemptParallelLeaseHeld(base unsafe.Pointer, layout *testingInternalsLayout, attempt *retryAttemptRoot) bool {
	if attempt == nil || !attempt.parallelLeaseTransfer.Load() || !*fieldPtr[bool](base, layout.common.isParallel) {
		return false
	}
	return len(*fieldPtr[[]*testing.T](base, layout.common.sub)) == 0
}

func (r *retryAttemptRoot) beginRootParallelSchedulerTransfer() {
	layout := r.layout
	localBase := commonBaseForTest(r.test, layout)
	reconcileRetryAttemptRaceBaselines(r, fieldPtr[atomic.Int64](localBase, layout.common.lastRaceErrors).Load())
	r.parallelPauseDuration = *fieldPtr[time.Duration](localBase, layout.common.duration)
	r.parallelLeaseTransfer.Store(true)

	transitioned := r.group.transitionOriginalToParallel()
	if bridge := r.group.rootParallelBridge; bridge != nil {
		if err := bridge(); err != nil {
			panic(err)
		}
	}
	if transitioned {
		testingTestStateRelease(getTestState(r.test))
	}
	close(*fieldPtr[chan bool](commonBaseForTest(r.parent, layout), layout.common.barrier))
}

func (r *retryAttemptRoot) finishRootParallelSchedulerTransfer(localDuration time.Duration) {
	originalBase := commonBaseForTest(r.original, r.layout)
	if postParallelDuration := localDuration - r.parallelPauseDuration; postParallelDuration > 0 {
		*fieldPtr[time.Duration](originalBase, r.layout.common.duration) += postParallelDuration
	}
	initializeRetryAttemptStart(originalBase, r.layout.common.start.unsafeField)
}

func setRetryAttemptRunner(base unsafe.Pointer, layout *testingInternalsLayout) {
	pc := make([]uintptr, 1)
	if runtime.Callers(2, pc) == 0 {
		return
	}
	if fn := runtime.FuncForPC(pc[0]); fn != nil {
		*fieldPtr[string](base, layout.common.runner) = fn.Name()
	}
}

func checkRetryAttemptRaces(base unsafe.Pointer, layout *testingInternalsLayout) (int64, bool) {
	raceErrors := retryAttemptRaceErrors()
	lastRaceErrors := fieldPtr[atomic.Int64](base, layout.common.lastRaceErrors)
	for {
		last := lastRaceErrors.Load()
		if raceErrors <= last {
			return raceErrors, false
		}
		if lastRaceErrors.CompareAndSwap(last, raceErrors) {
			break
		}
	}

	t := (*testing.T)(base)
	if fieldPtr[atomic.Bool](base, layout.common.raceErrorLogged).CompareAndSwap(false, true) {
		t.Errorf("race detected during execution of test")
	}
	for parent := pointerWord(base, layout.common.parent); parent != nil; parent = pointerWord(parent, layout.common.parent) {
		parentRaceErrors := fieldPtr[atomic.Int64](parent, layout.common.lastRaceErrors)
		for {
			last := parentRaceErrors.Load()
			if raceErrors <= last {
				return raceErrors, true
			}
			if parentRaceErrors.CompareAndSwap(last, raceErrors) {
				break
			}
		}
	}
	return raceErrors, true
}

func setRetryAttemptRan(base unsafe.Pointer, layout *testingInternalsLayout) {
	if parent := pointerWord(base, layout.common.parent); parent != nil {
		setRetryAttemptRan(parent, layout)
	}
	mu := fieldPtr[sync.RWMutex](base, layout.common.mu)
	mu.Lock()
	*fieldPtr[bool](base, layout.common.ran) = true
	mu.Unlock()
}

func flushRetryAttemptPartial(base unsafe.Pointer, layout *testingInternalsLayout) {
	outputWriter := pointerWord(base, wordCopiedField{unsafeField: layout.common.o})
	if outputWriter == nil || len(*fieldPtr[[]byte](outputWriter, layout.outputWriter.partial)) == 0 {
		return
	}
	_, _ = (*testing.T)(base).Output().Write([]byte("\n"))
}

func reportRetryAttempt(base unsafe.Pointer, layout *testingInternalsLayout) {
	parent := pointerWord(base, layout.common.parent)
	if parent == nil || (layout.common.isSynctest.available && *fieldPtr[bool](base, layout.common.isSynctest)) {
		return
	}

	duration := *fieldPtr[time.Duration](base, layout.common.duration)
	name := *fieldPtr[string](base, layout.common.name)
	status := "PASS"
	failed := *fieldPtr[bool](base, layout.common.failed)
	skipped := *fieldPtr[bool](base, layout.common.skipped)
	if failed {
		status = "FAIL"
	} else if skipped {
		status = "SKIP"
	}
	if !failed && pointerWord(base, layout.common.chatty) == nil {
		return
	}

	format := "--- %s: %s (%s)\n"
	args := []any{status, name, fmt.Sprintf("%.2fs", duration.Seconds())}
	parentMu := fieldPtr[sync.RWMutex](parent, layout.common.mu)
	parentMu.Lock()
	defer parentMu.Unlock()
	mu := fieldPtr[sync.RWMutex](base, layout.common.mu)
	mu.Lock()
	defer mu.Unlock()

	output := fieldPtr[[]byte](base, layout.common.output)
	if len(*output) > 0 {
		format += "%s"
		args = append(args, *output)
		*output = (*output)[:0]
	}

	_, _ = fmt.Fprintf(*fieldPtr[io.Writer](parent, layout.common.w), format, args...)
}

func freezeRetryAttemptOutput(attempt *retryAttemptRoot, base unsafe.Pointer, layout *testingInternalsLayout) []byte {
	mu := fieldPtr[sync.RWMutex](base, layout.common.mu)
	mu.RLock()
	output := append([]byte(nil), (*fieldPtr[[]byte](base, layout.common.output))...)
	mu.RUnlock()
	if attempt != nil {
		output = append(output, attempt.outputCapture.snapshot()...)
	}
	return output
}

func snapshotRetryAttemptResult(attempt *retryAttemptRoot, base unsafe.Pointer, layout *testingInternalsLayout, result *retryAttemptResult) {
	mu := fieldPtr[sync.RWMutex](base, layout.common.mu)
	mu.RLock()
	result.failed = *fieldPtr[bool](base, layout.common.failed)
	result.skipped = *fieldPtr[bool](base, layout.common.skipped)
	result.finished = *fieldPtr[bool](base, layout.common.finished)
	result.done = *fieldPtr[bool](base, layout.common.done)
	result.ran = *fieldPtr[bool](base, layout.common.ran)
	result.duration = *fieldPtr[time.Duration](base, layout.common.duration)
	result.nativeOutput = append([]byte(nil), (*fieldPtr[[]byte](base, layout.common.output))...)
	if result.output == nil {
		result.output = append([]byte(nil), result.nativeOutput...)
		if attempt != nil {
			result.output = append(result.output, attempt.outputCapture.snapshot()...)
		}
	}
	mu.RUnlock()
	result.terminalTrace = attempt.terminalTraceSnapshot()
	if result.failureCheckpointPhase == retryAttemptNotFailed && result.failed {
		result.failureCheckpointPhase = retryAttemptFailurePostCheckpoint
	}
}

func (r *retryAttemptRoot) testSignal() <-chan bool {
	base := commonBaseForTest(r.test, r.layout)
	return *fieldPtr[chan bool](base, r.layout.common.signal)
}
