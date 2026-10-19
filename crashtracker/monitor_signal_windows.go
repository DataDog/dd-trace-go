// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// ignoreTerminalSignals ignores Ctrl-C (the only console-control-forwarded
// signal os/signal exposes portably on Windows). detachProcessGroup already
// removes the monitor from the parent's console process group, which is what
// stops Ctrl-Break from reaching it.
func ignoreTerminalSignals() {
	signal.Ignore(os.Interrupt)
}

// detachProcessGroup creates the monitor in a new process group so console
// control events (Ctrl-C, Ctrl-Break) delivered to the parent's console are
// not also delivered to the monitor.
func detachProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
