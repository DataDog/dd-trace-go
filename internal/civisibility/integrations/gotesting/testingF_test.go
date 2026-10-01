// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"runtime"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
)

func TestCompleteFuzzTargetLifecycleCleanupFatalSuppressesBodyPanic(t *testing.T) {
	f := &testing.F{}
	f.Cleanup(func() { f.Fatal("cleanup fatal") })
	terminal, _ := completeFuzzTargetLifecycle(f, false, "body panic")
	require.Nil(t, terminal, "native cleanup Fatal replaces the panic with a normal failure")
	require.True(t, f.Failed())
}

func TestCompleteFuzzTargetLifecycleCleanupErrorPreservesBodyPanic(t *testing.T) {
	f := &testing.F{}
	f.Cleanup(func() { f.Error("cleanup error") })
	terminal, _ := completeFuzzTargetLifecycle(f, false, "body panic")
	require.Equal(t, "body panic", terminal)
	require.True(t, f.Failed())
}

func TestCompleteFuzzParallelSeedsOffsetsNativeDurationOnce(t *testing.T) {
	f := &testing.F{}
	fields := getTestPrivateFields((*testing.T)(unsafe.Pointer(f)))
	seed := &testing.T{}
	seedFields := getTestPrivateFields(seed)
	*seedFields.signal = make(chan bool)
	*fields.barrier = make(chan bool)
	*fields.sub = []*testing.T{seed}
	setFuzzNativeField(t, f, "duration", 20*time.Millisecond)
	go func() {
		<-*fields.barrier
		*seedFields.signal <- true
	}()

	completeFuzzParallelSeeds(f)

	event := fuzzTestEvent{native: f}
	_, _, duration := event.nativeResult(true)
	require.Less(t, duration, 20*time.Millisecond, "fRunner must exclude the already-drained wait")
	require.Empty(t, *fields.sub)
	completeFuzzParallelSeeds(f)
	_, _, repeated := event.nativeResult(true)
	require.Equal(t, duration, repeated, "a second drain must not subtract the wait again")
}

func TestRecordFuzzPanicPreservesFirstError(t *testing.T) {
	meta := &testExecutionMetadata{}
	recordFuzzPanic(meta, "body panic", "body stack")
	meta.processRetryError.CompareAndSwap(nil, &processRetryErrorInfo{Type: "Error", Message: "cleanup error"})
	require.Equal(t, &processRetryErrorInfo{Type: "panic", Message: "body panic", Stack: "body stack"}, meta.processRetryError.Load())
	recordFuzzPanic(meta, "secondary panic", "secondary stack")
	require.Equal(t, "body panic", meta.processRetryError.Load().Message)
}

func TestCompleteFuzzTargetLifecycleObservesCleanupPanic(t *testing.T) {
	f := &testing.F{}
	f.Cleanup(func() { panic("cleanup panic") })

	terminal, _ := completeFuzzTargetLifecycle(f, true, nil)

	require.Equal(t, "cleanup panic", terminal)
	require.True(t, f.Failed())
}

func TestCompleteFuzzTargetLifecyclePreservesBodyPanicOverCleanupPanic(t *testing.T) {
	f := &testing.F{}
	f.Cleanup(func() { panic("cleanup panic") })

	terminal, _ := completeFuzzTargetLifecycle(f, false, "body panic")

	require.Equal(t, "body panic", terminal)
	require.True(t, f.Failed())
}

func TestCompleteFuzzTargetLifecycleObservesCleanupFailure(t *testing.T) {
	f := &testing.F{}
	f.Cleanup(f.Fail)

	terminal, _ := completeFuzzTargetLifecycle(f, true, nil)

	require.Nil(t, terminal)
	require.True(t, f.Failed())
}

func TestCompleteFuzzTargetLifecycleObservesMissingFuzzCall(t *testing.T) {
	f := &testing.F{}

	terminal, _ := completeFuzzTargetLifecycle(f, true, nil)

	require.Nil(t, terminal)
	require.True(t, f.Failed())
}

func TestCompleteFuzzTargetLifecyclePreservesCleanupSkip(t *testing.T) {
	f := &testing.F{}
	f.Cleanup(func() { f.Skip("cleanup skip") })

	terminal, _ := completeFuzzTargetLifecycle(f, true, nil)

	require.Nil(t, terminal)
	require.True(t, f.Skipped())
	require.False(t, f.Failed())
}

func TestCompleteFuzzTargetLifecycleHandlesGoexit(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		bodyReturned, fuzzCalled  bool
		wantTerminal, wantFailure bool
	}{
		{name: "cleanup after F.Fuzz", bodyReturned: true, fuzzCalled: true},
		{name: "unfinished body", fuzzCalled: true, wantTerminal: true, wantFailure: true},
		{name: "missing F.Fuzz", bodyReturned: true, wantFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &testing.F{}
			setFuzzNativeField(t, f, "fuzzCalled", tc.fuzzCalled)
			remainingCleanupRan := false
			f.Cleanup(func() { remainingCleanupRan = true })
			f.Cleanup(runtime.Goexit)

			terminal, _ := completeFuzzTargetLifecycle(f, tc.bodyReturned, nil)

			if tc.wantTerminal {
				require.ErrorIs(t, terminal.(error), errTestingDidNotReturn)
			} else {
				require.Nil(t, terminal)
			}
			require.Equal(t, tc.wantFailure, f.Failed())
			require.True(t, remainingCleanupRan, "Goexit must not discard earlier cleanups")
		})
	}
}

func TestTestingFuzzWorkerRequested(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "short flag", args: []string{"-test.fuzzworker"}, want: true},
		{name: "long flag", args: []string{"--test.fuzzworker"}, want: true},
		{name: "explicit true", args: []string{"-test.fuzzworker=true"}, want: true},
		{name: "numeric true", args: []string{"--test.fuzzworker=1"}, want: true},
		{name: "explicit false", args: []string{"-test.fuzzworker=false"}},
		{name: "numeric false", args: []string{"--test.fuzzworker=0"}},
		{name: "invalid value", args: []string{"-test.fuzzworker=invalid"}},
		{name: "unrelated flag", args: []string{"-test.fuzz=FuzzNativeParity"}},
		{name: "after terminator", args: []string{"--", "-test.fuzzworker"}},
		{name: "after positional argument", args: []string{"package.test", "-test.fuzzworker"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, testingFuzzWorkerRequested(tt.args))
		})
	}
}
