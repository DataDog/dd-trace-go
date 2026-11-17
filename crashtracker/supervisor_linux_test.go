// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSupervisorDeathKillsChild(t *testing.T) {
	cmd, dir := startSupervisorHelper(t, "wait")
	pid, err := strconv.Atoi(string(waitForFile(t, filepath.Join(dir, "ready"))))
	if err != nil {
		t.Fatal(err)
	}
	// Check that the child stays alive before the supervisor dies.
	// This does not force the spawning OS thread to retire.
	time.Sleep(100 * time.Millisecond)
	if childIsDead(t, pid) {
		t.Fatal("child died while its supervisor was still alive")
	}
	start := time.Now()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = waitForVictim(t, cmd, time.Second)
	for !childIsDead(t, pid) {
		if time.Since(start) >= time.Second {
			t.Fatal("child survived supervisor SIGKILL for 1 second")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Logf("child died within %s of supervisor SIGKILL", time.Since(start))
}

func TestSupervisorWaitFailure(t *testing.T) {
	cmd, dir := startSupervisorHelper(t, "wait-error")
	_ = waitForVictim(t, cmd, 5*time.Second)
	if cmd.ProcessState.ExitCode() != 1 {
		t.Fatalf("supervisor exit code = %d, want 1", cmd.ProcessState.ExitCode())
	}
	output, err := os.ReadFile(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "crashtracker: wait for application process failed:") {
		t.Fatalf("missing wait failure warning: %s", output)
	}
}

func childIsDead(t *testing.T, pid int) bool {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, os.ErrNotExist) || errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return true
	}
	if err != nil {
		t.Fatal(err)
	}
	// A zombie has exited even if the container's PID 1 has not reaped it.
	_, rest, _ := strings.Cut(string(data), ") ")
	return strings.HasPrefix(rest, "Z")
}
