// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !windows

package crashtracker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

const testProcessOwnerEnv = "_CRASHTRACKER_TEST_OWNER"
const testProcessScopeEnv = "_CRASHTRACKER_TEST_SCOPE"

func prepareTestProcess(t *testing.T, cmd *exec.Cmd) (string, error) {
	t.Helper()
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	if os.Getenv(testProcessOwnerEnv) != "" {
		return "", nil
	}
	if !retainTestProcessOwner(t, cmd) {
		return "", nil
	}
	scope := filepath.Join(t.TempDir(), "active")
	if err := os.WriteFile(scope, nil, 0o600); err != nil {
		return "", err
	}
	cmd.Env = append(cmd.Env, testProcessOwnerEnv+"="+strconv.Itoa(os.Getpid()), testProcessScopeEnv+"="+scope)
	return scope, nil
}

func containTestProcess(*exec.Cmd) error { return nil }

func killTestProcess(cmd *exec.Cmd) {
	// A detached application can use a different group from its supervisor.
	if attr := cmd.SysProcAttr; attr.Setsid || (attr.Setpgid && attr.Pgid == 0) {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	_ = cmd.Process.Kill()
}

// WatchTestProcessOwner stops helpers even if SIGKILL prevents test cleanup.
func WatchTestProcessOwner() {
	owner, err := strconv.Atoi(os.Getenv(testProcessOwnerEnv))
	if err != nil || owner <= 1 {
		return
	}
	scope := os.Getenv(testProcessScopeEnv)
	go func() {
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			_, err := os.Stat(scope)
			if os.IsNotExist(err) || syscall.Kill(owner, 0) == syscall.ESRCH {
				_ = syscall.Kill(-syscall.Getpgrp(), syscall.SIGKILL)
				os.Exit(1)
			}
		}
	}()
}
