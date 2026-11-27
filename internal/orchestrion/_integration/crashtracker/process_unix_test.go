// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//generator:ignore-build-constraint
//go:build !windows

package crashtracker

import (
	"os/exec"
	"syscall"
)

func init() {
	configureCrashtrackerCommand = func(cmd *exec.Cmd) {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	}
}
