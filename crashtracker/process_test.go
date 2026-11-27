// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

const testProcessReapTimeout = 2 * time.Second

type testProcessWait struct {
	done chan struct{}
	err  error
}

var testProcessWaits sync.Map

// StartTestProcess starts a helper and makes the test responsible for its cleanup.
func StartTestProcess(t *testing.T, cmd *exec.Cmd) error {
	t.Helper()
	stop, err := prepareTestProcess(t, cmd)
	if err != nil {
		return err
	}
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	containErr := containTestProcess(cmd)
	wait := &testProcessWait{done: make(chan struct{})}
	testProcessWaits.Store(cmd, wait)
	go func() {
		wait.err = cmd.Wait()
		close(wait.done)
	}()
	t.Cleanup(func() {
		stopTestProcess(t, cmd, wait, stop)
		testProcessWaits.Delete(cmd)
	})
	if containErr != nil {
		stopTestProcess(t, cmd, wait, stop)
	}
	return containErr
}

// WaitTestProcess waits for the helper until the deadline.
func WaitTestProcess(t *testing.T, cmd *exec.Cmd, timeout time.Duration) error {
	t.Helper()
	entry, ok := testProcessWaits.Load(cmd)
	if !ok {
		t.Fatal("wait for a helper that did not start")
	}
	wait := entry.(*testProcessWait)
	select {
	case <-wait.done:
		return wait.err
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for helper PID %d after %s", cmd.Process.Pid, timeout)
		return nil
	}
}

func stopTestProcess(t *testing.T, cmd *exec.Cmd, wait *testProcessWait, scope string) {
	t.Helper()
	if scope != "" {
		_ = os.Remove(scope)
	}
	select {
	case <-wait.done:
		return
	default:
		killTestProcess(cmd)
	}
	select {
	case <-wait.done:
	case <-time.After(testProcessReapTimeout):
		t.Errorf("could not reap helper PID %d after %s", cmd.Process.Pid, testProcessReapTimeout)
	}
}
