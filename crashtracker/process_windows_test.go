// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"os/exec"
	"sync"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

var testProcessJobs sync.Map

func prepareTestProcess(t *testing.T, cmd *exec.Cmd) (string, error) {
	t.Helper()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return "", err
	}
	t.Cleanup(func() {
		testProcessJobs.Delete(cmd)
		_ = windows.CloseHandle(job)
	})
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return "", err
	}
	testProcessJobs.Store(cmd, job)
	return "", nil
}

func containTestProcess(cmd *exec.Cmd) error {
	entry, _ := testProcessJobs.Load(cmd)
	job := entry.(windows.Handle)
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		const stillActive = 259
		var exitCode uint32
		if getErr := windows.GetExitCodeProcess(process, &exitCode); getErr == nil && exitCode != stillActive {
			return nil
		}
		return err
	}
	return nil
}

func killTestProcess(cmd *exec.Cmd) {
	if job, ok := testProcessJobs.Load(cmd); ok {
		_ = windows.TerminateJobObject(job.(windows.Handle), 1)
	}
	_ = cmd.Process.Kill()
}

func WatchTestProcessOwner() {}
