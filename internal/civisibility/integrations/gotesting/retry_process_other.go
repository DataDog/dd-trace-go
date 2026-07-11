// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build !unix && !windows

package gotesting

import (
	"errors"
	"os"
	"os/exec"
)

func processRetryChildStartsSuspended() bool { return false }

func setProcessGroupForCommand(cmd *exec.Cmd) error {
	if cmd == nil {
		return errProcessRetryProcessNotStarted
	}
	return errProcessRetryTreeUnsupported
}

func attachProcessTree(*exec.Cmd) error { return nil }

func resumeProcessTree(*exec.Cmd) error { return nil }

func releaseProcessTree(*exec.Cmd) error { return nil }

func terminateProcessTree(cmd *exec.Cmd) error {
	return killDirectChild(cmd)
}

func killProcessTree(cmd *exec.Cmd) error {
	return killDirectChild(cmd)
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
