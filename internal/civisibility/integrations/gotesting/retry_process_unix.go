// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build unix

package gotesting

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func processRetryChildStartsSuspended() bool { return false }

func setProcessGroupForCommand(cmd *exec.Cmd) error {
	if cmd == nil {
		return errProcessRetryProcessNotStarted
	}
	// Process groups contain ordinary descendants. A descendant that creates a
	// new session is deliberately outside that containment contract.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func attachProcessTree(*exec.Cmd) error { return nil }

func resumeProcessTree(*exec.Cmd) error { return nil }

func releaseProcessTree(*exec.Cmd) error { return nil }

func terminateProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return errProcessRetryProcessNotStarted
	}
	return signalProcessRetryGroup(cmd, syscall.SIGTERM)
}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return errProcessRetryProcessNotStarted
	}
	return signalProcessRetryGroup(cmd, syscall.SIGKILL)
}

func signalProcessRetryGroup(cmd *exec.Cmd, signal syscall.Signal) error {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return errProcessRetryProcessNotStarted
	}
	err := syscall.Kill(-cmd.Process.Pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func killDirectChild(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return errProcessRetryProcessNotStarted
	}
	err := cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
