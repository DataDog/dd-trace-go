// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !windows

package crashtracker

import (
	"os/exec"
	"os/signal"
	"syscall"
)

// ignoreTerminalSignals makes the monitor immune to the signals a terminal or
// supervisor commonly forwards to a whole process group (Ctrl-\ sends SIGQUIT
// to the foreground group; dumb-init without --single-child forwards SIGTERM
// the same way). Without this, the monitor can be killed at the exact moment
// the app it is meant to report on receives the same signal.
func ignoreTerminalSignals() {
	signal.Ignore(syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP)
}

// detachProcessGroup puts the monitor in its own process group so signals
// delivered to the parent's group (terminal signals, a supervisor's group
// broadcast) are not also delivered to the monitor by the kernel.
func detachProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
