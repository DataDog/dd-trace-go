// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

func requireRetryAttemptParallelConflict(t *testing.T, panicData any) {
	t.Helper()
	message := fmt.Sprint(panicData)
	require.Contains(t, message, "testing: test using")
	require.Contains(t, message, "can not use t.Parallel")
}

func TestProcessRetryParityFreshRunnerNormalLifecycle(t *testing.T) {
	var events []string
	attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
		local.Cleanup(func() {
			require.ErrorIs(t, local.Context().Err(), context.Canceled)
			events = append(events, "cleanup-1")
		})
		local.Cleanup(func() { events = append(events, "cleanup-2") })
		local.Output().Write([]byte("partial"))
		events = append(events, "body")
	})
	require.Empty(t, reason)
	require.NotNil(t, attempt)
	defer attempt.cancelContexts()

	require.Equal(t, []string{"body", "cleanup-2", "cleanup-1"}, events)
	require.False(t, result.failed)
	require.False(t, result.skipped)
	require.True(t, result.finished)
	require.True(t, result.done)
	require.True(t, result.ran)
	require.True(t, result.reportExecuted)
	require.True(t, result.nativeSignalExecuted)
	require.Equal(t, retryAttemptCompletionNormal, result.completionPhase)
	require.Equal(t, retryAttemptNotFailed, result.failureCheckpointPhase)
	require.Positive(t, result.duration)
	require.Contains(t, string(result.output), "partial\n")
}

type retryAttemptCountingStringer struct {
	calls atomic.Int32
}

func (s *retryAttemptCountingStringer) String() string {
	s.calls.Add(1)
	return "sentinel"
}

func TestProcessRetryParityGotestingFormattedMethodsEvaluateArgumentsOnce(t *testing.T) {
	tests := []struct {
		name        string
		invoke      func(*T, *retryAttemptCountingStringer)
		failed      bool
		skipped     bool
		wantMessage string
	}{
		{name: "Error", invoke: func(local *T, value *retryAttemptCountingStringer) { local.Error("manual", value) }, failed: true, wantMessage: "manual sentinel"},
		{name: "Errorf", invoke: func(local *T, value *retryAttemptCountingStringer) { local.Errorf("manual %s", value) }, failed: true, wantMessage: "manual sentinel"},
		{name: "Fatal", invoke: func(local *T, value *retryAttemptCountingStringer) { local.Fatal("manual", value) }, failed: true, wantMessage: "manual sentinel"},
		{name: "Fatalf", invoke: func(local *T, value *retryAttemptCountingStringer) { local.Fatalf("manual %s", value) }, failed: true, wantMessage: "manual sentinel"},
		{name: "Skip", invoke: func(local *T, value *retryAttemptCountingStringer) { local.Skip("manual", value) }, skipped: true, wantMessage: "manual sentinel"},
		{name: "Skipf", invoke: func(local *T, value *retryAttemptCountingStringer) { local.Skipf("manual %s", value) }, skipped: true, wantMessage: "manual sentinel"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			value := &retryAttemptCountingStringer{}
			attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
				tc.invoke(GetTest(local), value)
			})
			require.Empty(t, reason)
			require.NotNil(t, attempt)
			defer attempt.cancelContexts()
			require.Equal(t, int32(1), value.calls.Load())
			require.Equal(t, tc.failed, result.failed)
			require.Equal(t, tc.skipped, result.skipped)
			require.Contains(t, string(result.output), tc.wantMessage)
		})
	}
}

func TestProcessRetryParityGotestingBenchmarkFormattedMethodsEvaluateArgumentsOnce(t *testing.T) {
	tests := []struct {
		name   string
		invoke func(*B, *retryAttemptCountingStringer)
	}{
		{name: "Error", invoke: func(local *B, value *retryAttemptCountingStringer) { local.Error("manual", value) }},
		{name: "Errorf", invoke: func(local *B, value *retryAttemptCountingStringer) { local.Errorf("manual %s", value) }},
		{name: "Fatal", invoke: func(local *B, value *retryAttemptCountingStringer) { local.Fatal("manual", value) }},
		{name: "Fatalf", invoke: func(local *B, value *retryAttemptCountingStringer) { local.Fatalf("manual %s", value) }},
		{name: "Skip", invoke: func(local *B, value *retryAttemptCountingStringer) { local.Skip("manual", value) }},
		{name: "Skipf", invoke: func(local *B, value *retryAttemptCountingStringer) { local.Skipf("manual %s", value) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			value := &retryAttemptCountingStringer{}
			testing.Benchmark(func(local *testing.B) {
				tc.invoke((*B)(local), value)
			})
			require.Equal(t, int32(1), value.calls.Load())
		})
	}
}

func TestProcessRetryParityFreshRunnerTerminalSemantics(t *testing.T) {
	panicToken := &struct{ value string }{"panic-token"}
	tests := []struct {
		name          string
		target        func(*testing.T)
		failed        bool
		skipped       bool
		finished      bool
		done          bool
		completion    retryAttemptCompletionPhase
		checkpoint    retryAttemptFailureCheckpointPhase
		panicIdentity any
		terminalKinds []retryAttemptTerminalKind
	}{
		{
			name:       "Fail",
			target:     func(local *testing.T) { local.Fail() },
			failed:     true,
			finished:   true,
			done:       true,
			completion: retryAttemptCompletionNormal,
			checkpoint: retryAttemptFailurePreCheckpoint,
		},
		{
			name:       "Error",
			target:     func(local *testing.T) { local.Error("error") },
			failed:     true,
			finished:   true,
			done:       true,
			completion: retryAttemptCompletionNormal,
			checkpoint: retryAttemptFailurePreCheckpoint,
		},
		{
			name:       "Errorf",
			target:     func(local *testing.T) { local.Errorf("error: %s", "formatted") },
			failed:     true,
			finished:   true,
			done:       true,
			completion: retryAttemptCompletionNormal,
			checkpoint: retryAttemptFailurePreCheckpoint,
		},
		{
			name:          "FailNow",
			target:        func(local *testing.T) { local.FailNow() },
			failed:        true,
			finished:      true,
			done:          true,
			completion:    retryAttemptCompletionNormal,
			checkpoint:    retryAttemptFailurePreCheckpoint,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalBodyFailNow},
		},
		{
			name:          "Fatal",
			target:        func(local *testing.T) { local.Fatal("fatal") },
			failed:        true,
			finished:      true,
			done:          true,
			completion:    retryAttemptCompletionNormal,
			checkpoint:    retryAttemptFailurePreCheckpoint,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalBodyFailNow},
		},
		{
			name:          "Fatalf",
			target:        func(local *testing.T) { local.Fatalf("fatal: %s", "formatted") },
			failed:        true,
			finished:      true,
			done:          true,
			completion:    retryAttemptCompletionNormal,
			checkpoint:    retryAttemptFailurePreCheckpoint,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalBodyFailNow},
		},
		{
			name:          "SkipNow",
			target:        func(local *testing.T) { local.SkipNow() },
			skipped:       true,
			finished:      true,
			done:          true,
			completion:    retryAttemptCompletionNormal,
			checkpoint:    retryAttemptNotFailed,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalBodySkipNow},
		},
		{
			name:          "Skip",
			target:        func(local *testing.T) { local.Skip("skip") },
			skipped:       true,
			finished:      true,
			done:          true,
			completion:    retryAttemptCompletionNormal,
			checkpoint:    retryAttemptNotFailed,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalBodySkipNow},
		},
		{
			name:          "Skipf",
			target:        func(local *testing.T) { local.Skipf("skip: %s", "formatted") },
			skipped:       true,
			finished:      true,
			done:          true,
			completion:    retryAttemptCompletionNormal,
			checkpoint:    retryAttemptNotFailed,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalBodySkipNow},
		},
		{
			name: "Fail then SkipNow",
			target: func(local *testing.T) {
				local.Fail()
				local.SkipNow()
			},
			failed:        true,
			skipped:       true,
			finished:      true,
			done:          true,
			completion:    retryAttemptCompletionNormal,
			checkpoint:    retryAttemptFailurePreCheckpoint,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalBodySkipNow},
		},
		{
			name: "Fail then bare Goexit",
			target: func(local *testing.T) {
				local.Fail()
				runtime.Goexit()
			},
			failed:        true,
			finished:      false,
			done:          false,
			completion:    retryAttemptCompletionUnexpectedGoexit,
			checkpoint:    retryAttemptFailurePreCheckpoint,
			panicIdentity: errRetryAttemptNilPanicOrGoexit,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalBodyGoexit, retryAttemptTerminalSynthesizedGoexit},
		},
		{
			name:          "bare Goexit",
			target:        func(local *testing.T) { runtime.Goexit() },
			failed:        true,
			finished:      false,
			done:          false,
			completion:    retryAttemptCompletionUnexpectedGoexit,
			checkpoint:    retryAttemptFailurePostCheckpoint,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalBodyGoexit, retryAttemptTerminalSynthesizedGoexit},
		},
		{
			name:          "panic identity",
			target:        func(local *testing.T) { panic(panicToken) },
			failed:        true,
			finished:      false,
			done:          false,
			completion:    retryAttemptCompletionPanic,
			checkpoint:    retryAttemptFailurePostCheckpoint,
			panicIdentity: panicToken,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalBodyPanic},
		},
		{
			name: "cleanup FailNow",
			target: func(local *testing.T) {
				local.Cleanup(func() { local.FailNow() })
			},
			failed:        true,
			finished:      true,
			done:          true,
			completion:    retryAttemptCompletionNormal,
			checkpoint:    retryAttemptFailurePreCheckpoint,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalCleanupGoexit},
		},
		{
			name: "cleanup SkipNow",
			target: func(local *testing.T) {
				local.Cleanup(func() { local.SkipNow() })
			},
			skipped:       true,
			finished:      true,
			done:          true,
			completion:    retryAttemptCompletionNormal,
			checkpoint:    retryAttemptNotFailed,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalCleanupSkipNow},
		},
		{
			name: "cleanup panic",
			target: func(local *testing.T) {
				local.Cleanup(func() { panic(panicToken) })
			},
			failed:        true,
			finished:      true,
			done:          false,
			completion:    retryAttemptCompletionPanic,
			checkpoint:    retryAttemptFailurePostCheckpoint,
			panicIdentity: panicToken,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalCleanupPanic},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			attempt, result, reason := runFreshRetryAttempt(t, tc.target)
			require.Empty(t, reason)
			require.NotNil(t, attempt)
			defer attempt.cancelContexts()
			require.Equal(t, tc.failed, result.failed)
			require.Equal(t, tc.skipped, result.skipped)
			require.Equal(t, tc.finished, result.finished)
			require.Equal(t, tc.done, result.done)
			require.Equal(t, tc.completion, result.completionPhase)
			require.Equal(t, tc.checkpoint, result.failureCheckpointPhase)
			require.Equal(t, tc.terminalKinds, retryAttemptTerminalKinds(result.terminalTrace))
			if tc.panicIdentity != nil {
				require.Same(t, tc.panicIdentity, result.panicData)
			}
			require.False(t, t.Failed(), "attempt failure must not mutate the original test")
		})
	}
}

func retryAttemptTerminalKinds(trace []retryAttemptTerminal) []retryAttemptTerminalKind {
	if trace == nil {
		return nil
	}
	kinds := make([]retryAttemptTerminalKind, len(trace))
	for i := range trace {
		kinds[i] = trace[i].kind
	}
	return kinds
}

func TestProcessRetryParityFreshRunnerPreservesMultiTerminalTrace(t *testing.T) {
	bodyPanic := &struct{ name string }{name: "body panic"}
	cleanupPanic := &struct{ name string }{name: "cleanup panic"}
	tests := []struct {
		name          string
		cleanup       func()
		terminalKinds []retryAttemptTerminalKind
		policyPanic   any
		nativeSignal  bool
		nativeFatal   bool
	}{
		{
			name:          "body panic then cleanup panic",
			cleanup:       func() { panic(cleanupPanic) },
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalBodyPanic, retryAttemptTerminalCleanupPanic},
			policyPanic:   cleanupPanic,
			nativeFatal:   true,
		},
		{
			name:          "body panic then cleanup Goexit",
			cleanup:       runtime.Goexit,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalBodyPanic, retryAttemptTerminalCleanupGoexit, retryAttemptTerminalSynthesizedGoexit},
			policyPanic:   errRetryAttemptNilPanicOrGoexit,
			nativeFatal:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
				local.Cleanup(tc.cleanup)
				panic(bodyPanic)
			})
			require.Empty(t, reason)
			require.NotNil(t, attempt)
			defer attempt.cancelContexts()
			require.Equal(t, tc.terminalKinds, retryAttemptTerminalKinds(result.terminalTrace))
			require.Same(t, bodyPanic, result.terminalTrace[0].value)
			require.Same(t, tc.policyPanic, result.panicData)
			require.Equal(t, tc.nativeSignal, result.nativeSignalExecuted)
			require.Equal(t, tc.nativeFatal, result.nativeFatalRequired)
			require.Equal(t, tc.nativeFatal, result.nativeFatalTraceReplay)
			require.False(t, result.done)
			require.True(t, result.failed)
		})
	}
}

func TestProcessRetryParityMultiTerminalReplayPreservesNativeOutput(t *testing.T) {
	tests := []struct {
		name     string
		cleanup  string
		contains []string
	}{
		{
			name:     "cleanup panic",
			cleanup:  "panic",
			contains: []string{"panic: retry parity body panic", "panic: retry parity cleanup panic"},
		},
		{
			name:     "cleanup Goexit",
			cleanup:  "goexit",
			contains: []string{"panic: retry parity body panic", "test executed panic(nil) or runtime.Goexit"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryParityMultiTerminalReplayFixture$", "-test.count=1", "-test.timeout=10s")
			cmd.Env = append(os.Environ(),
				"Bypass=true",
				"RETRY_PARITY_MULTI_TERMINAL_FIXTURE=true",
				"RETRY_PARITY_MULTI_TERMINAL_CLEANUP="+tc.cleanup,
			)
			var output bytes.Buffer
			cmd.Stdout = &output
			cmd.Stderr = &output
			err := cmd.Run()
			require.Error(t, err)
			for _, expected := range tc.contains {
				require.Contains(t, output.String(), expected)
			}
			first := bytes.Index(output.Bytes(), []byte(tc.contains[0]))
			second := bytes.Index(output.Bytes(), []byte(tc.contains[1]))
			require.Greater(t, first, -1)
			require.Greater(t, second, first)
		})
	}
}

func TestProcessRetryParityMultiTerminalReplayFixture(t *testing.T) {
	if os.Getenv("RETRY_PARITY_MULTI_TERMINAL_FIXTURE") != "true" {
		t.Skip("subprocess fixture")
	}
	attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
		switch os.Getenv("RETRY_PARITY_MULTI_TERMINAL_CLEANUP") {
		case "panic":
			local.Cleanup(func() { panic("retry parity cleanup panic") })
		case "goexit":
			local.Cleanup(runtime.Goexit)
		default:
			t.Fatal("missing cleanup mode")
		}
		panic("retry parity body panic")
	})
	if reason != "" {
		t.Fatal(reason)
	}
	defer attempt.group.retire()
	if !result.nativeFatalRequired || !result.nativeFatalTraceReplay {
		t.Fatal("multi-terminal attempt was incorrectly made continuable")
	}
	replayRetryAttemptNativeTerminalTrace(result.terminalTrace)
}

func TestProcessRetryParityFreshRunnerBodyAndCleanupTerminalMatrix(t *testing.T) {
	bodyPanic := &struct{ name string }{name: "body"}
	cleanupPanic := &struct{ name string }{name: "cleanup"}
	tests := []struct {
		name          string
		body          func()
		cleanup       func(*testing.T)
		failed        bool
		skipped       bool
		done          bool
		completion    retryAttemptCompletionPhase
		policyPanic   any
		terminalKinds []retryAttemptTerminalKind
	}{
		{
			name: "body panic cleanup FailNow", body: func() { panic(bodyPanic) },
			cleanup: func(local *testing.T) { local.FailNow() }, failed: true, done: true,
			completion:    retryAttemptCompletionNormal,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalBodyPanic, retryAttemptTerminalCleanupGoexit},
		},
		{
			name: "body panic cleanup SkipNow", body: func() { panic(bodyPanic) },
			cleanup: func(local *testing.T) { local.SkipNow() }, skipped: true, done: true,
			completion:    retryAttemptCompletionNormal,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalBodyPanic, retryAttemptTerminalCleanupSkipNow},
		},
		{
			name: "body Goexit cleanup FailNow", body: runtime.Goexit,
			cleanup: func(local *testing.T) { local.FailNow() }, failed: true, done: true,
			completion:    retryAttemptCompletionNormal,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalBodyGoexit, retryAttemptTerminalCleanupGoexit},
		},
		{
			name: "body Goexit cleanup SkipNow", body: runtime.Goexit,
			cleanup: func(local *testing.T) { local.SkipNow() }, skipped: true, done: true,
			completion:    retryAttemptCompletionNormal,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalBodyGoexit, retryAttemptTerminalCleanupSkipNow},
		},
		{
			name: "body Goexit cleanup panic", body: runtime.Goexit,
			cleanup: func(*testing.T) { panic(cleanupPanic) }, failed: true,
			completion: retryAttemptCompletionPanic, policyPanic: cleanupPanic,
			terminalKinds: []retryAttemptTerminalKind{retryAttemptTerminalBodyGoexit, retryAttemptTerminalCleanupPanic},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
				local.Cleanup(func() { tc.cleanup(local) })
				tc.body()
			})
			require.Empty(t, reason)
			require.NotNil(t, attempt)
			defer attempt.group.retire()
			require.Equal(t, tc.failed, result.failed)
			require.Equal(t, tc.skipped, result.skipped)
			require.Equal(t, tc.done, result.done)
			require.Equal(t, tc.completion, result.completionPhase)
			require.Equal(t, tc.terminalKinds, retryAttemptTerminalKinds(result.terminalTrace))
			if tc.policyPanic != nil {
				require.Same(t, tc.policyPanic, result.panicData)
			} else {
				require.Nil(t, result.panicData)
			}
		})
	}
}

func TestProcessRetryParityQueuedSubtestFatalUsesNativeProcessBoundary(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryParityQueuedSubtestFatalFixture$", "-test.count=1", "-test.timeout=10s")
	cmd.Env = append(os.Environ(), "Bypass=true", "RETRY_PARITY_QUEUED_FATAL_FIXTURE=true")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	require.Error(t, err)
	require.Contains(t, output.String(), "retry parity queued fatal")
	require.NotContains(t, output.String(), "test timed out")
}

func TestProcessRetryParityQueuedSubtestFatalFixture(t *testing.T) {
	if os.Getenv("RETRY_PARITY_QUEUED_FATAL_FIXTURE") != "true" {
		t.Skip("subprocess fixture")
	}
	attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
		local.Run("parallel", func(child *testing.T) { child.Parallel() })
		panic("retry parity queued fatal")
	})
	if reason != "" {
		t.Fatal(reason)
	}
	defer attempt.group.retire()
	if !result.nativeFatalRequired {
		t.Fatal("queued fatal attempt was incorrectly made continuable")
	}
	panic(result.panicData)
}

func TestProcessRetryParityFreshRunnerParallelSubtest(t *testing.T) {
	continued := make(chan struct{})
	attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
		local.Run("parallel", func(child *testing.T) {
			child.Parallel()
			close(continued)
		})
	})
	require.Empty(t, reason)
	require.NotNil(t, attempt)
	defer attempt.cancelContexts()
	<-continued
	require.False(t, result.failed)
	require.True(t, result.finished)
	require.True(t, result.done)
	require.True(t, result.ran)
}

func TestProcessRetryParityFreshRunnerQueuedParallelSubtestCleanupTerminals(t *testing.T) {
	panicIdentity := &struct{ name string }{name: "queued cleanup panic"}
	tests := []struct {
		name              string
		cleanup           func(*testing.T)
		observation       retryAttemptCleanupObservation
		failed            bool
		skipped           bool
		done              bool
		nativeSignal      bool
		completion        retryAttemptCompletionPhase
		cleanupPanicValue any
		schedulerReleased bool
		nativeFatal       bool
	}{
		{name: "returned", cleanup: func(*testing.T) {}, observation: retryAttemptCleanupReturned, done: true, nativeSignal: true, completion: retryAttemptCompletionNormal},
		{name: "FailNow", cleanup: func(local *testing.T) { local.FailNow() }, observation: retryAttemptCleanupGoexitAmbiguous, failed: true, nativeSignal: true, completion: retryAttemptCompletionNormal, schedulerReleased: true},
		{name: "SkipNow", cleanup: func(local *testing.T) { local.SkipNow() }, observation: retryAttemptCleanupSkipNowObserved, skipped: true, nativeSignal: true, completion: retryAttemptCompletionNormal, schedulerReleased: true},
		{name: "bare Goexit", cleanup: func(*testing.T) { runtime.Goexit() }, observation: retryAttemptCleanupGoexitAmbiguous, nativeSignal: true, completion: retryAttemptCompletionNormal, schedulerReleased: true},
		{name: "panic", cleanup: func(*testing.T) { panic(panicIdentity) }, observation: retryAttemptCleanupPanicked, failed: true, completion: retryAttemptCompletionPanic, cleanupPanicValue: panicIdentity, schedulerReleased: true, nativeFatal: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
				local.Run("parallel", func(child *testing.T) {
					child.Parallel()
				})
				local.Cleanup(func() { tc.cleanup(local) })
			})
			require.Empty(t, reason)
			require.NotNil(t, attempt)
			defer attempt.cancelContexts()
			require.Equal(t, tc.observation, result.cleanupObservation)
			require.Equal(t, tc.failed, result.failed)
			require.Equal(t, tc.skipped, result.skipped)
			require.Equal(t, tc.done, result.done)
			require.Equal(t, tc.nativeSignal, result.nativeSignalExecuted)
			require.Equal(t, tc.completion, result.completionPhase)
			require.Equal(t, tc.schedulerReleased, result.schedulerSlotReleased)
			require.Equal(t, tc.nativeFatal, result.nativeFatalRequired)
			if tc.cleanupPanicValue != nil {
				require.Same(t, tc.cleanupPanicValue, result.cleanupPanicData)
			}
		})
	}
}

func TestProcessRetryParityFreshRunnerRootParallelPreservesConflicts(t *testing.T) {
	t.Run("parallel then Setenv", func(t *testing.T) {
		attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
			local.Parallel()
			local.Setenv("DD_RETRY_ATTEMPT_PARITY", "forbidden")
		})
		require.Empty(t, reason)
		require.NotNil(t, attempt)
		defer attempt.cancelContexts()
		require.True(t, result.failed)
		require.Equal(t, retryAttemptCompletionPanic, result.completionPhase)
		requireRetryAttemptParallelConflict(t, result.panicData)
	})

	t.Run("Setenv then parallel", func(t *testing.T) {
		const key = "DD_RETRY_ATTEMPT_PARITY"
		original, hadOriginal := os.LookupEnv(key)
		attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
			local.Setenv(key, "temporary")
			local.Parallel()
		})
		require.Empty(t, reason)
		require.NotNil(t, attempt)
		defer attempt.cancelContexts()
		require.True(t, result.failed)
		require.Equal(t, retryAttemptCompletionPanic, result.completionPhase)
		requireRetryAttemptParallelConflict(t, result.panicData)
		value, exists := os.LookupEnv(key)
		require.Equal(t, hadOriginal, exists)
		if hadOriginal {
			require.Equal(t, original, value)
		}
	})

	t.Run("parallel then Chdir", func(t *testing.T) {
		targetDir := t.TempDir()
		attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
			local.Parallel()
			local.Chdir(targetDir)
		})
		require.Empty(t, reason)
		require.NotNil(t, attempt)
		defer attempt.group.retire()
		require.True(t, result.failed)
		require.Equal(t, retryAttemptCompletionPanic, result.completionPhase)
		requireRetryAttemptParallelConflict(t, result.panicData)
	})

	t.Run("Chdir then parallel", func(t *testing.T) {
		originalDir, err := os.Getwd()
		require.NoError(t, err)
		targetDir := t.TempDir()
		attempt, result, reason := runFreshRetryAttempt(t, func(local *testing.T) {
			local.Chdir(targetDir)
			local.Parallel()
		})
		require.Empty(t, reason)
		require.NotNil(t, attempt)
		defer attempt.group.retire()
		require.True(t, result.failed)
		require.Equal(t, retryAttemptCompletionPanic, result.completionPhase)
		requireRetryAttemptParallelConflict(t, result.panicData)
		cwd, err := os.Getwd()
		require.NoError(t, err)
		require.Equal(t, originalDir, cwd)
	})
}

type retryAttemptTestStateSnapshot struct {
	running     int
	numWaiting  int
	maxParallel int
}

func snapshotRetryAttemptTestState(t *testing.T) retryAttemptTestStateSnapshot {
	layout, reason := getRetryAttemptLayout()
	require.Empty(t, reason)
	state := getTestState(t)
	base := unsafe.Pointer(state)
	mu := fieldPtr[sync.Mutex](base, layout.testState.mu)
	mu.Lock()
	defer mu.Unlock()
	return retryAttemptTestStateSnapshot{
		running:     *fieldPtr[int](base, layout.testState.running),
		numWaiting:  *fieldPtr[int](base, layout.testState.numWaiting),
		maxParallel: *fieldPtr[int](base, layout.testState.maxParallel),
	}
}

func TestProcessRetryParityFreshRunnerBalancesSchedulerLease(t *testing.T) {
	tests := []struct {
		name   string
		target func(*testing.T)
	}{
		{name: "sequential", target: func(*testing.T) {}},
		{
			name: "sequential queued cleanup Goexit",
			target: func(local *testing.T) {
				local.Run("parallel", func(child *testing.T) { child.Parallel() })
				local.Cleanup(runtime.Goexit)
			},
		},
		{name: "root parallel", target: func(local *testing.T) { local.Parallel() }},
		{
			name: "root parallel queued cleanup Goexit",
			target: func(local *testing.T) {
				local.Parallel()
				local.Run("parallel", func(child *testing.T) { child.Parallel() })
				local.Cleanup(runtime.Goexit)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(container *testing.T) {
			container.Run("attempt", func(t *testing.T) {
				before := snapshotRetryAttemptTestState(t)
				attempt, _, reason := runFreshRetryAttempt(t, tc.target)
				require.Empty(t, reason)
				require.NotNil(t, attempt)
				defer attempt.cancelContexts()
				require.Equal(t, before, snapshotRetryAttemptTestState(t))
			})
		})
	}
}

func TestProcessRetryParityFreshRunnerSharesRootParallelLeaseAcrossSequentialAttempts(t *testing.T) {
	t.Run("container", func(container *testing.T) {
		container.Run("attempt", func(original *testing.T) {
			before := snapshotRetryAttemptTestState(original)
			group, reason := newRetryAttemptGroup(original)
			require.Empty(original, reason)
			defer group.retire()

			first, firstResult, reason := runFreshRetryAttemptInGroup(group, func(local *testing.T) {
				local.Parallel()
			})
			require.Empty(original, reason)
			require.NotNil(original, first)
			defer first.cancelContexts()
			require.False(original, firstResult.failed)

			second, secondResult, reason := runFreshRetryAttemptInGroup(group, func(local *testing.T) {
				local.Parallel()
			})
			require.Empty(original, reason)
			require.NotNil(original, second)
			defer second.cancelContexts()
			require.False(original, secondResult.failed)

			third, thirdResult, reason := runFreshRetryAttemptInGroup(group, func(local *testing.T) {
				local.Setenv("DD_RETRY_ATTEMPT_PARITY", "forbidden-after-parallel")
			})
			require.Empty(original, reason)
			require.NotNil(original, third)
			defer third.cancelContexts()
			require.True(original, thirdResult.failed)
			require.Equal(original, retryAttemptCompletionPanic, thirdResult.completionPhase)
			requireRetryAttemptParallelConflict(original, thirdResult.panicData)

			layout, layoutReason := getRetryAttemptLayout()
			require.Empty(original, layoutReason)
			originalBase := commonBaseForTest(original, layout)
			parentBase := pointerWord(originalBase, layout.common.parent)
			occurrences := 0
			for _, sub := range *fieldPtr[[]*testing.T](parentBase, layout.common.sub) {
				if sub == original {
					occurrences++
				}
			}
			require.Equal(original, 1, occurrences)
			require.Equal(original, before, snapshotRetryAttemptTestState(original))
		})
	})
}

func TestProcessRetryParityFreshRunnerSerializesAttemptsInOneGroup(t *testing.T) {
	group, reason := newRetryAttemptGroup(t)
	require.Empty(t, reason)
	defer group.retire()

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan string, 1)
	go func() {
		_, _, runReason := runFreshRetryAttemptInGroup(group, func(*testing.T) {
			close(firstStarted)
			<-releaseFirst
		})
		firstDone <- runReason
	}()
	<-firstStarted
	require.False(t, group.executionMu.TryLock(), "the active attempt must own the group execution lease")

	secondDone := make(chan string, 1)
	go func() {
		_, _, runReason := runFreshRetryAttemptInGroup(group, func(*testing.T) {})
		secondDone <- runReason
	}()
	close(releaseFirst)
	require.Empty(t, <-firstDone)
	require.Empty(t, <-secondDone)
	require.True(t, group.executionMu.TryLock())
	group.executionMu.Unlock()
}

func TestProcessRetryParityMatcherTransactionRestoresOnlyTopLevelDescendants(t *testing.T) {
	group, reason := newRetryAttemptGroup(t)
	require.Empty(t, reason)
	defer group.retire()

	runNames := func() []string {
		names := make([]string, 0, 2)
		attempt, result, runReason := runFreshRetryAttemptInGroup(group, func(local *testing.T) {
			local.Run("duplicate", func(child *testing.T) { names = append(names, child.Name()) })
			local.Run("duplicate", func(child *testing.T) { names = append(names, child.Name()) })
		})
		require.Empty(t, runReason)
		require.NotNil(t, attempt)
		defer attempt.cancelContexts()
		require.False(t, result.failed)
		return names
	}

	first := runNames()
	second := runNames()
	require.Equal(t, first, second)
	require.Equal(t, []string{t.Name() + "/duplicate", t.Name() + "/duplicate#01"}, first)

	var nativeName string
	require.True(t, t.Run("duplicate", func(child *testing.T) { nativeName = child.Name() }))
	require.Equal(t, t.Name()+"/duplicate", nativeName)
}

func TestProcessRetryParityFreshRunnerRetiresLateMethodDestinations(t *testing.T) {
	group, reason := newRetryAttemptGroup(t)
	require.Empty(t, reason)

	var local *testing.T
	var writer io.Writer
	attempt, result, reason := runFreshRetryAttemptInGroup(group, func(current *testing.T) {
		local = current
		writer = current.Output()
	})
	require.Empty(t, reason)
	require.NotNil(t, attempt)
	require.True(t, result.done)

	require.NotPanics(t, func() { local.Log("late while logical group is active") })
	require.NotPanics(t, func() { _, _ = writer.Write([]byte("late writer while active\n")) })
	require.False(t, group.hasLateFailure())
	require.Panics(t, local.Fail)
	require.True(t, group.hasLateFailure())
	group.retire()

	require.Panics(t, func() { local.Log("late after logical group retirement") })
	require.Panics(t, func() { _, _ = writer.Write([]byte("late writer after retirement\n")) })
	require.Panics(t, local.Fail)
	require.NotPanics(t, local.Helper)
	require.False(t, t.Failed())
}

func TestProcessRetryParityRaceBaselineReconciliationIsMonotonic(t *testing.T) {
	attempt, reason := newRetryAttemptRoot(t)
	require.Empty(t, reason)
	require.NotNil(t, attempt)
	defer attempt.group.retire()

	originalBase := commonBaseForTest(t, attempt.layout)
	parentBase := pointerWord(originalBase, attempt.layout.common.parent)
	require.NotNil(t, parentBase)
	originalBaseline := fieldPtr[atomic.Int64](originalBase, attempt.layout.common.lastRaceErrors)
	parentBaseline := fieldPtr[atomic.Int64](parentBase, attempt.layout.common.lastRaceErrors)
	target := max(originalBaseline.Load(), parentBaseline.Load()) + 7

	reconcileRetryAttemptRaceBaselines(attempt, target)
	require.Equal(t, target, originalBaseline.Load())
	require.Equal(t, target, parentBaseline.Load())

	reconcileRetryAttemptRaceBaselines(attempt, target-1)
	require.Equal(t, target, originalBaseline.Load())
	require.Equal(t, target, parentBaseline.Load())
}
