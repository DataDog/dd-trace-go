// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"

	"github.com/stretchr/testify/require"
)

func TestExitCiVisibilityStopsSignalHandler(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityMockModeForTesting)

	InitializeCIVisibilityMock()
	handler := currentCIVisibilitySignalHandlerForTesting()
	require.NotNil(t, handler)

	ExitCiVisibility()

	assertSignalHandlerDone(t, handler)
	require.Nil(t, currentCIVisibilitySignalHandlerForTesting())
	require.Equal(t, civisibility.StateExited, civisibility.GetState())
}

func TestStopCIVisibilitySignalHandlerIsIdempotent(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	t.Cleanup(restoreCIVisibilityMockModeForTesting)

	startCIVisibilitySignalHandler()
	handler := currentCIVisibilitySignalHandlerForTesting()
	require.NotNil(t, handler)

	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			stopCIVisibilitySignalHandler()
		})
	}

	waitForSignalHandlerStopCalls(t, &wg)
	assertSignalHandlerDone(t, handler)
	require.Nil(t, currentCIVisibilitySignalHandlerForTesting())
}

func TestCIVisibilitySignalHandlerSignalPathShutsDownWithoutSelfWait(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityMockModeForTesting)

	exitCodes := make(chan int, 1)
	originalExitFunc := ciVisibilitySignalExitFunc
	ciVisibilitySignalExitFunc = func(code int) {
		exitCodes <- code
	}
	t.Cleanup(func() {
		ciVisibilitySignalExitFunc = originalExitFunc
	})

	InitializeCIVisibilityMock()
	handler := currentCIVisibilitySignalHandlerForTesting()
	require.NotNil(t, handler)

	handler.signals <- syscall.SIGTERM

	require.Equal(t, 1, waitForSignalHandlerExitCode(t, exitCodes))
	assertSignalHandlerDone(t, handler)
	require.Equal(t, civisibility.StateExited, civisibility.GetState())
}

func TestCIVisibilitySignalHandlerDoesNotExitAfterNormalShutdownStarts(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityMockModeForTesting)

	exitCodes := make(chan int, 1)
	originalExitFunc := ciVisibilitySignalExitFunc
	ciVisibilitySignalExitFunc = func(code int) {
		exitCodes <- code
	}
	t.Cleanup(func() {
		ciVisibilitySignalExitFunc = originalExitFunc
	})

	InitializeCIVisibilityMock()
	handler := currentCIVisibilitySignalHandlerForTesting()
	require.NotNil(t, handler)

	markCIVisibilitySignalHandlerStopping()
	handler.signals <- syscall.SIGTERM

	assertSignalHandlerDone(t, handler)
	assertNoSignalHandlerExitCode(t, exitCodes)
	stopCIVisibilitySignalHandler()
	require.Nil(t, currentCIVisibilitySignalHandlerForTesting())
}

// currentCIVisibilitySignalHandlerForTesting returns the active handler under
// the same mutex used by production code.
func currentCIVisibilitySignalHandlerForTesting() *ciVisibilitySignalHandler {
	ciVisibilitySignalHandlerMu.Lock()
	defer ciVisibilitySignalHandlerMu.Unlock()
	return activeCIVisibilitySignalHandler
}

// assertSignalHandlerDone waits for the handler goroutine to exit.
func assertSignalHandlerDone(t *testing.T, handler *ciVisibilitySignalHandler) {
	t.Helper()
	require.NotNil(t, handler)
	select {
	case <-handler.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for CI Visibility signal handler to stop")
	}
}

// waitForSignalHandlerStopCalls waits for concurrent stop calls to finish.
func waitForSignalHandlerStopCalls(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for CI Visibility signal handler stop calls")
	}
}

// waitForSignalHandlerExitCode waits for the test exit seam to be called.
func waitForSignalHandlerExitCode(t *testing.T, exitCodes <-chan int) int {
	t.Helper()
	select {
	case code := <-exitCodes:
		return code
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for CI Visibility signal handler exit code")
		return 0
	}
}

// assertNoSignalHandlerExitCode verifies that normal shutdown suppression did
// not route through the process-exit path.
func assertNoSignalHandlerExitCode(t *testing.T, exitCodes <-chan int) {
	t.Helper()
	select {
	case code := <-exitCodes:
		t.Fatalf("unexpected CI Visibility signal handler exit code: %d", code)
	default:
	}
}
