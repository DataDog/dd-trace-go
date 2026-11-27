// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !linux && !windows

package crashtracker_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func setHelperParentDeathSignal(_ *testing.T, _ *exec.Cmd) {}

func TestSupervisorDeathDoesNotKillChild(t *testing.T) {
	cmd, dir := startSupervisorHelper(t, "survive-supervisor")
	app := waitForFile(t, filepath.Join(dir, "ready"))
	pid, err := strconv.Atoi(string(app))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = waitForVictim(t, cmd, 5*time.Second)
	if got := waitForFile(t, filepath.Join(dir, "survived")); !bytes.Equal(got, app) {
		t.Fatalf("application PID after supervisor exit = %s, want %s", got, app)
	}
}
