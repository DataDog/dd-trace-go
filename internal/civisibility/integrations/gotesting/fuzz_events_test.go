// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
)

func TestFuzzEventsReconcileNativeOutcome(t *testing.T) {
	for _, tc := range []struct {
		name                                                            string
		rawFailed, rawSkipped, nativeFailed, nativeSkipped, quarantined bool
		want                                                            processRetryStatus
		final                                                           string
	}{
		{name: "late failure", nativeFailed: true, want: processRetryStatusFail, final: constants.TestStatusFail},
		{name: "masked failure", rawFailed: true, nativeSkipped: true, quarantined: true, want: processRetryStatusFail, final: constants.TestStatusSkip},
		{name: "masked pass", nativeSkipped: true, quarantined: true, want: processRetryStatusPass, final: constants.TestStatusSkip},
		{name: "native skip", nativeSkipped: true, want: processRetryStatusSkip, final: constants.TestStatusSkip},
		{name: "raw skip", rawSkipped: true, want: processRetryStatusSkip, final: constants.TestStatusSkip},
		{name: "pass", want: processRetryStatusPass, final: constants.TestStatusPass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native := &testing.T{}
			fields := getTestPrivateFields(native)
			fields.SetFailed(tc.nativeFailed)
			fields.SetSkipped(tc.nativeSkipped)
			event := newProcessRetryRecordingTestForTesting(tc.name)
			queue := &fuzzEventQueue{}
			queue.add(fuzzTestEvent{native: native, metadata: &testExecutionMetadata{isQuarantined: tc.quarantined}, test: event, suite: event.suite, module: event.suite.module, failed: tc.rawFailed, skipped: tc.rawSkipped, finishTime: time.Now()})
			require.Zero(t, event.closeCount)
			queue.finish()
			require.Equal(t, tc.want, event.status)
			require.Equal(t, tc.final, event.tags[constants.TestFinalStatus])
			queue.finish()
			require.Equal(t, 1, event.closeCount)
			requireFuzzQueueDrained(t, queue)
		})
	}
}

func TestFuzzEventsPreserveExecutionDuration(t *testing.T) {
	for _, kind := range []string{"root", "seed"} {
		for _, nativeFinished := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/native_finished=%t", kind, nativeFinished), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					var native testing.TB = &testing.T{}
					if kind == "root" {
						native = &testing.F{}
					}
					setFuzzNativeField(t, native, "duration", 23*time.Millisecond)
					start := time.Now()
					event := &fuzzTimedRecordingTest{processRetryRecordingTest: newProcessRetryRecordingTestForTesting("duration"), start: start}
					pending := fuzzTestEvent{native: native, metadata: &testExecutionMetadata{}, test: event, suite: event.suite, module: event.suite.module, finishTime: start.Add(time.Millisecond)}
					queue := &fuzzEventQueue{}
					queue.add(pending)
					time.Sleep(time.Hour) // Package completion must not extend execution time.
					require.Zero(t, event.closeCount)
					wantFinish := pending.finishTime
					if nativeFinished {
						queue.finishAfterNativeRun()
						wantFinish = start.Add(23 * time.Millisecond)
					} else {
						queue.finish()
					}
					require.Equal(t, wantFinish, event.finish)
					require.Equal(t, 1, event.closeCount)
					queue.finishAfterNativeRun()
					require.Equal(t, wantFinish, event.finish)
					require.Equal(t, 1, event.closeCount)
					requireFuzzQueueDrained(t, queue)
				})
			})
		}
	}
}

func TestFuzzEventsSkipHooksWaitForCleanup(t *testing.T) {
	oldEnabled := atomic.LoadInt32(&ciVisibilityEnabledValue)
	atomic.StoreInt32(&ciVisibilityEnabledValue, 1)
	t.Cleanup(func() { atomic.StoreInt32(&ciVisibilityEnabledValue, oldEnabled) })
	for _, kind := range []string{"root", "seed"} {
		for _, hook := range []struct {
			name   string
			skip   func(testing.TB)
			reason string
		}{
			{name: "Skip", skip: func(tb testing.TB) { instrumentCloseAndSkip(tb, "skip sentinel") }, reason: "skip sentinel"},
			{name: "Skipf", skip: func(tb testing.TB) { instrumentCloseAndSkip(tb, "formatted sentinel") }, reason: "formatted sentinel"},
			{name: "SkipNow", skip: instrumentSkipNow},
		} {
			for _, cleanupFails := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/cleanup_fails=%t", kind, hook.name, cleanupFails), func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						nativeT := &testing.T{}
						var native testing.TB = nativeT
						if kind == "root" {
							f := &testing.F{}
							native, nativeT = f, (*testing.T)(unsafe.Pointer(f))
						}
						fields := getTestPrivateFields(nativeT)
						fields.SetSkipped(true)
						event := newProcessRetryRecordingTestForTesting("skip")
						queue := &fuzzEventQueue{}
						meta := createTestMetadata(native, nil)
						defer deleteTestMetadata(native)
						meta.test, meta.fuzzEvents = event, queue
						queue.add(fuzzTestEvent{native: native, metadata: meta, test: event, suite: event.suite, module: event.suite.module, finishTime: time.Now()})
						hook.skip(native)
						require.Zero(t, event.closeCount, "skip must not close before cleanup starts")
						require.Equal(t, hook.reason, meta.skipReason)

						entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
						t.Cleanup(func() {
							select {
							case <-release:
							default:
								close(release)
							}
						})
						native.Cleanup(func() {
							close(entered)
							<-release
							if cleanupFails {
								native.Error("cleanup error sentinel")
							}
						})
						go func() {
							runTestCleanupCallbacks(nativeT, &testCleanupResult{})
							close(done)
						}()
						<-entered
						synctest.Wait()
						require.Zero(t, event.closeCount, "skip must not close while cleanup is blocked")
						close(release)
						<-done
						require.Zero(t, event.closeCount, "native completion still owns finalization")
						queue.finishAfterNativeRun()
						want := processRetryStatusSkip
						if cleanupFails {
							want = processRetryStatusFail
						}
						require.Equal(t, want, event.status)
						require.Equal(t, hook.reason, event.skipReason)
						require.Equal(t, 1, event.closeCount)
					})
				})
			}
		}
	}
}

func TestFuzzEventsPreserveCleanupPanicDetails(t *testing.T) {
	for _, bodyPanic := range []bool{false, true} {
		for _, quarantined := range []bool{false, true} {
			t.Run(fmt.Sprintf("body_panic=%t/quarantined=%t", bodyPanic, quarantined), func(t *testing.T) {
				native := &testing.T{}
				remainingCleanupRan := false
				native.Cleanup(func() { remainingCleanupRan = true })
				native.Cleanup(func() { panic("cleanup panic sentinel") })
				meta := &testExecutionMetadata{cleanupResult: &testCleanupResult{}, isQuarantined: quarantined}
				runAndApplyTestCleanupWithDurationOptions(native, meta, time.Millisecond, true)
				require.Equal(t, "cleanup panic sentinel", meta.panicData)
				require.True(t, remainingCleanupRan)
				require.False(t, meta.cleanupResult.goexit, "panic is not Goexit")
				require.Contains(t, meta.panicStacktrace, "TestFuzzEventsPreserveCleanupPanicDetails")
				wantMessage, wantStack := "cleanup panic sentinel", meta.panicStacktrace
				if bodyPanic {
					wantMessage, wantStack = "body panic sentinel", "body stack sentinel"
					meta.processRetryError.Store(&processRetryErrorInfo{Type: "panic", Message: wantMessage, Stack: wantStack})
				}
				event := newProcessRetryRecordingTestForTesting("cleanup panic")
				queue := &fuzzEventQueue{}
				queue.add(fuzzTestEvent{native: native, metadata: meta, test: event, suite: event.suite, module: event.suite.module, failed: native.Failed(), finishTime: time.Now()})
				queue.finishAfterNativeRun()
				require.Equal(t, processRetryStatusFail, event.status)
				require.Equal(t, "panic", event.errorType)
				require.Equal(t, wantMessage, event.errorMessage)
				require.Equal(t, wantStack, event.errorStack)
				wantFinal := constants.TestStatusFail
				if quarantined {
					wantFinal = constants.TestStatusSkip
				}
				require.Equal(t, wantFinal, event.tags[constants.TestFinalStatus])
			})
		}
	}
}

func TestFuzzEventsFatalDrainDoesNotReadUnprotectedDuration(t *testing.T) {
	native := &testing.F{}
	ptr, err := getFieldPointerFromWithType(native, "duration", reflect.TypeFor[time.Duration]())
	require.NoError(t, err)
	done, err := getFieldPointerFromWithType(native, "done", reflect.TypeFor[bool]())
	require.NoError(t, err)
	started, stop, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		close(started)
		for {
			select {
			case <-stop:
				return
			default:
				*(*time.Duration)(ptr) += time.Nanosecond
				*(*bool)(done) = !*(*bool)(done)
			}
		}
	}()
	<-started
	defer func() { close(stop); <-stopped }()
	for range 100 {
		event := newProcessRetryRecordingTestForTesting("active root")
		queue := &fuzzEventQueue{}
		queue.add(fuzzTestEvent{native: native, metadata: &testExecutionMetadata{}, test: event, suite: event.suite, module: event.suite.module, finishTime: time.Now()})
		queue.finish()
		require.Equal(t, 1, event.closeCount)
	}
}

type fuzzTimedRecordingTest struct {
	*processRetryRecordingTest
	start, finish time.Time
}

func (e *fuzzTimedRecordingTest) StartTime() time.Time { return e.start }

func (e *fuzzTimedRecordingTest) Close(status integrations.TestResultStatus, options ...integrations.TestCloseOption) {
	e.processRetryRecordingTest.Close(status, options...)
	for _, option := range options {
		fn := reflect.ValueOf(option)
		value := reflect.New(fn.Type().In(0).Elem())
		fn.Call([]reflect.Value{value})
		ptr, err := getFieldPointerFromWithType(value.Interface(), "finishTime", reflect.TypeFor[time.Time]())
		if err == nil && !(*time.Time)(ptr).IsZero() {
			e.finish = *(*time.Time)(ptr)
		}
	}
}

func TestFuzzEventsConcurrentAdmissionAndRepeatedFinish(t *testing.T) {
	queue := &fuzzEventQueue{}
	const count = 1000
	events := make([]*processRetryRecordingTest, count)
	var workers sync.WaitGroup
	for i := range count {
		events[i] = newProcessRetryRecordingTestForTesting("seed")
		workers.Go(func() {
			event := events[i]
			queue.add(fuzzTestEvent{native: &testing.T{}, metadata: &testExecutionMetadata{}, test: event, suite: event.suite, module: event.suite.module, finishTime: time.Now()})
		})
	}
	workers.Wait()
	queue.finish()
	queue.finish()
	for _, event := range events {
		require.Equal(t, 1, event.closeCount)
	}
	requireFuzzQueueDrained(t, queue)
	other := &fuzzEventQueue{}
	event := newProcessRetryRecordingTestForTesting("other M.Run")
	other.add(fuzzTestEvent{native: &testing.T{}, metadata: &testExecutionMetadata{}, test: event, suite: event.suite, module: event.suite.module, finishTime: time.Now()})
	queue.finish()
	require.Zero(t, event.closeCount, "one M.Run must not drain another claim")
	other.finish()
	require.Equal(t, 1, event.closeCount)
}

func TestFuzzEventsAdmissionOverlapsFatalDrain(t *testing.T) {
	queue := &fuzzEventQueue{}
	const count = 1000
	events := make([]*processRetryRecordingTest, count)
	var workers sync.WaitGroup
	for i := range count {
		events[i] = newProcessRetryRecordingTestForTesting("seed")
		workers.Go(func() {
			event := events[i]
			queue.add(fuzzTestEvent{native: &testing.T{}, metadata: &testExecutionMetadata{}, test: event, suite: event.suite, module: event.suite.module, finishTime: time.Now()})
		})
		if i%100 == 0 {
			workers.Go(queue.finish)
		}
	}
	workers.Wait()
	queue.finish()
	for _, event := range events {
		require.Equal(t, 1, event.closeCount, "a seed racing fatal shutdown must still finish exactly once")
	}
	requireFuzzQueueDrained(t, queue)
}

func requireFuzzQueueDrained(t *testing.T, queue *fuzzEventQueue) {
	t.Helper()
	queue.mu.Lock()
	defer queue.mu.Unlock()
	require.Nil(t, queue.events, "release native tests and event references after finishing")
}

func setFuzzNativeField[V any](t *testing.T, native any, name string, value V) {
	t.Helper()
	ptr, err := getFieldPointerFromWithType(native, name, reflect.TypeFor[V]())
	require.NoError(t, err)
	*(*V)(ptr) = value
}

func BenchmarkFuzzEventsRetention(b *testing.B) {
	for b.Loop() {
		queue := &fuzzEventQueue{}
		for range 10000 {
			queue.add(fuzzTestEvent{})
		}
		// Measure only queue storage; native testing objects and tracer events
		// are measured separately by the corpus fixture.
	}
	b.ReportMetric(float64(unsafe.Sizeof(fuzzTestEvent{})), "record-bytes")
}
