// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"os/exec"
	"runtime"
	"syscall"
)

func retainSupervisorThread(cmd *exec.Cmd) func() {
	// Linux sends Pdeathsig when the forking OS thread exits, not only when
	// the process exits. Keep this goroutine locked for the supervisor's
	// lifetime so Go cannot retire that thread while the app still runs.
	runtime.LockOSThread()
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	return runtime.UnlockOSThread
}
