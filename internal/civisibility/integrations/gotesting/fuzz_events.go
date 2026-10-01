// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"fmt"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// fuzzEventQueue belongs to one instrumentation claim. Admission can run in
// parallel seeds; normal draining starts only after native M.Run returns. Fatal
// shutdown drains completed executions before the session's close actions.
type fuzzEventQueue struct {
	mu             locking.Mutex
	events         []*fuzzTestEvent // +checklocks:mu
	finished       bool             // +checklocks:mu
	nativeFinished bool             // +checklocks:mu
}

type fuzzTestEvent struct {
	native          testing.TB
	metadata        *testExecutionMetadata
	test            integrations.Test
	suite           integrations.TestSuite
	module          integrations.TestModule
	finishTime      time.Time
	failed, skipped bool // raw outcome captured before Test Management masks it
	closeContainers bool
}

func (q *fuzzEventQueue) add(event fuzzTestEvent) *fuzzTestEvent {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.finished {
		// A fatal close may overlap a seed completing. Preserve its captured
		// outcome rather than adding references to an already drained queue.
		event.finish(q.nativeFinished)
		tracer.Flush()
		if event.closeContainers {
			checkModuleAndSuite(event.module, event.suite)
		}
		return &event
	}
	q.events = append(q.events, &event)
	return &event
}

// complete freezes a root's raw outcome before native Test Management masking.
// Roots are admitted before calling F.Fuzz so a fatal seed also closes its root.
func (q *fuzzEventQueue) complete(event *fuzzTestEvent, failed, skipped bool, finishTime time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.finished {
		return
	}
	event.failed = failed
	event.skipped = skipped
	event.finishTime = finishTime
}

func (q *fuzzEventQueue) finish() {
	q.drain(false)
}

// finishAfterNativeRun is the only path allowed to read native durations. Go
// writes them without common.mu; M.Run returning provides the required barrier.
func (q *fuzzEventQueue) finishAfterNativeRun() {
	q.drain(true)
}

func (q *fuzzEventQueue) drain(nativeFinished bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.finished {
		return
	}
	q.finished = true
	q.nativeFinished = nativeFinished
	for i := range q.events {
		q.events[i].finish(nativeFinished)
		// CI Flush drains the tracer's accepted chunks before returning. Keep
		// each burst below its 1,000-entry shared queue; no sleeps or worker
		// scheduling assumptions are needed for a large deferred corpus.
		if (i+1)%256 == 0 || i == len(q.events)-1 {
			tracer.Flush()
		}
	}
	// Close containers only after every queued test event has been emitted.
	for i := range q.events {
		if q.events[i].closeContainers {
			checkModuleAndSuite(q.events[i].module, q.events[i].suite)
		}
		q.events[i] = nil
	}
	q.events = nil
}

// nativeResult reads Go's stored result, never Failed's process-wide race check.
// Fatal shutdown can read an active root's already-propagated seed failure, but
// must not read duration: Go writes it without holding common.mu.
func (e *fuzzTestEvent) nativeResult(nativeFinished bool) (failed, skipped bool, duration time.Duration) {
	var t *testing.T
	switch native := e.native.(type) {
	case *testing.T:
		t = native
	case *testing.F:
		t = (*testing.T)(unsafe.Pointer(native))
	default:
		return
	}
	fields := getTestPrivateFields(t)
	if fields == nil || fields.mu == nil {
		log.Debug("civisibility: native fuzz result layout unavailable; retaining captured outcome and finish time")
		return
	}
	fields.mu.RLock()
	if fields.failed != nil {
		failed = *fields.failed
	}
	if fields.skipped != nil {
		skipped = *fields.skipped
	}
	fields.mu.RUnlock()
	if nativeFinished {
		durationPtr, err := getFieldPointerFromWithType(e.native, "duration", reflect.TypeFor[time.Duration]())
		if err != nil {
			log.Debug("civisibility: native fuzz duration field unavailable; retaining captured finish time")
			return
		}
		duration = *(*time.Duration)(durationPtr)
	}
	return
}

func (e *fuzzTestEvent) finish(nativeFinished bool) {
	failed, skipped, duration := e.nativeResult(nativeFinished)
	failed = failed || e.failed
	// A managed pass/failure is changed to native skip by design. That skip
	// must not replace the raw event status captured before masking.
	skipped = e.skipped || skipped && !e.metadata.isQuarantined && !e.metadata.isDisabled
	finishTime := e.finishTime
	if finishTime.IsZero() {
		// A seed panic aborts its root before the root wrapper can complete.
		finishTime = time.Now()
	}
	if duration > 0 {
		finishTime = e.test.StartTime().Add(duration)
	}
	finishFuzzTestEvent(failed, skipped, e.metadata, e.test, e.suite, e.module, finishTime)
}

func finishFuzzTestEvent(failed, skipped bool, execMeta *testExecutionMetadata, test integrations.Test, suite integrations.TestSuite, module integrations.TestModule, finishTime time.Time) {
	status := integrations.ResultStatusPass
	if failed {
		status = integrations.ResultStatusFail
		if captured := execMeta.processRetryError.Load(); captured != nil {
			test.SetError(integrations.WithErrorInfo(captured.Type, captured.Message, captured.Stack))
		} else if execMeta.panicData != nil {
			test.SetError(integrations.WithErrorInfo("panic", fmt.Sprint(execMeta.panicData), execMeta.panicStacktrace))
		} else {
			test.SetTag(ext.Error, true)
		}
		suite.SetTag(ext.Error, true)
		module.SetTag(ext.Error, true)
	} else if skipped {
		status = integrations.ResultStatusSkip
	}
	test.SetTag(constants.TestFinalStatus, calculateFinalStatus(!failed && !skipped, failed, skipped, execMeta.isQuarantined, execMeta.isDisabled, execMeta.isAttemptToFix))
	reason := execMeta.skipReason
	if reason == "" {
		if captured := execMeta.processRetrySkipReason.Load(); captured != nil {
			reason = *captured
		}
	}
	test.Close(status, integrations.WithTestFinishTime(finishTime), integrations.WithTestSkipReason(reason))
}
