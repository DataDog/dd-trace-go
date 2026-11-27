// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"os/exec"
	"runtime"
	"syscall"
	"testing"
)

func retainTestProcessOwner(t *testing.T, cmd *exec.Cmd) bool {
	t.Helper()
	// Linux sends Pdeathsig when the calling thread exits.
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	// The namespaced helper cannot see the test owner's PID.
	return cmd.SysProcAttr.Cloneflags&syscall.CLONE_NEWPID == 0
}
