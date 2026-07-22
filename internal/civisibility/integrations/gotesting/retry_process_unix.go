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

// ProcessRetryContainmentSupported reports whether this platform can contain
// ordinary retry-child descendants.
func ProcessRetryContainmentSupported() bool { return true }

func processRetryChildStartsSuspended() bool { return false }

func prepareProcessRetryControlTransport(cmd *exec.Cmd) (*processRetryControlTransport, error) {
	if cmd == nil {
		return nil, errProcessRetryProcessNotStarted
	}
	parentToChildRead, parentToChildWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	childToParentRead, childToParentWrite, err := os.Pipe()
	if err != nil {
		_ = parentToChildRead.Close()
		_ = parentToChildWrite.Close()
		return nil, err
	}
	readFD := 3 + len(cmd.ExtraFiles)
	writeFD := readFD + 1
	cmd.ExtraFiles = append(cmd.ExtraFiles, parentToChildRead, childToParentWrite)
	return &processRetryControlTransport{
		read:       childToParentRead,
		write:      parentToChildWrite,
		childRead:  parentToChildRead,
		childWrite: childToParentWrite,
		config: processRetryControlConfig{
			Transport:     processRetryControlTransportUnixPipes,
			ReadEndpoint:  uint64(readFD),
			WriteEndpoint: uint64(writeFD),
		},
	}, nil
}

func openProcessRetryChildControlTransport(cfg processRetryControlConfig) (*os.File, *os.File, error) {
	if cfg.Transport != processRetryControlTransportUnixPipes || cfg.ReadEndpoint < 3 || cfg.WriteEndpoint < 3 {
		return nil, nil, errProcessRetryControlInvalid
	}
	read := os.NewFile(uintptr(cfg.ReadEndpoint), "dd-process-retry-control-read")
	write := os.NewFile(uintptr(cfg.WriteEndpoint), "dd-process-retry-control-write")
	if read == nil || write == nil {
		_ = closeProcessRetryControlFile(read)
		_ = closeProcessRetryControlFile(write)
		return nil, nil, errProcessRetryControlInvalid
	}
	return read, write, nil
}

func setProcessGroupForCommand(cmd *exec.Cmd) error {
	if cmd == nil {
		return errProcessRetryProcessNotStarted
	}
	// Process groups contain ordinary descendants. A descendant that creates a
	// new session is deliberately outside that containment contract.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
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
