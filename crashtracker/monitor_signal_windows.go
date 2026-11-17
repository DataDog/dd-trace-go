// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
)

func supervisorTerminalMode() bool {
	return false
}

func configureChild(cmd *exec.Cmd, pipe *os.File, _ bool) (string, error) {
	h := syscall.Handle(pipe.Fd())
	if err := syscall.SetHandleInformation(h, syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT); err != nil {
		return "", err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{AdditionalInheritedHandles: []syscall.Handle{h}}
	return strconv.FormatUint(uint64(h), 10), nil
}

func closeInheritedDescriptors(_ *exec.Cmd, _ *os.File) {}

func inheritedCrashPipe(fd uintptr) (_ *os.File, err error) {
	f := os.NewFile(fd, "crashtracker-pipe")
	if f == nil {
		return nil, os.ErrInvalid
	}
	defer func() {
		if err != nil {
			_ = f.Close()
		}
	}()
	h := syscall.Handle(fd)
	kind, err := syscall.GetFileType(h)
	if err != nil {
		return nil, err
	}
	if kind != syscall.FILE_TYPE_PIPE {
		return nil, errors.New("handle is not a pipe")
	}
	if err := syscall.SetHandleInformation(h, syscall.HANDLE_FLAG_INHERIT, 0); err != nil {
		return nil, err
	}
	return f, nil
}

func supervisorSignals(_ bool) (start func(*os.Process), stop func()) {
	// Console Ctrl events reach both processes without forwarding.
	signal.Ignore(os.Interrupt)
	return func(*os.Process) {}, func() { signal.Reset(os.Interrupt) }
}

func exitWithChildStatus(state *os.ProcessState) {
	os.Exit(state.ExitCode())
}
