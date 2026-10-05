// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build windows

package retryprocess

import (
	"os"
	"os/exec"
	"syscall"
)

func prepareOrchestrionRetryProcessControlTransport(cmd *exec.Cmd) (*orchestrionRetryProcessControlTransport, error) {
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
	readHandle := syscall.Handle(parentToChildRead.Fd())
	writeHandle := syscall.Handle(childToParentWrite.Fd())
	if err := syscall.SetHandleInformation(readHandle, syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT); err != nil {
		_ = parentToChildRead.Close()
		_ = parentToChildWrite.Close()
		_ = childToParentRead.Close()
		_ = childToParentWrite.Close()
		return nil, err
	}
	if err := syscall.SetHandleInformation(writeHandle, syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT); err != nil {
		_ = parentToChildRead.Close()
		_ = parentToChildWrite.Close()
		_ = childToParentRead.Close()
		_ = childToParentWrite.Close()
		return nil, err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.AdditionalInheritedHandles = append(cmd.SysProcAttr.AdditionalInheritedHandles, readHandle, writeHandle)
	return &orchestrionRetryProcessControlTransport{
		read:       childToParentRead,
		write:      parentToChildWrite,
		childRead:  parentToChildRead,
		childWrite: childToParentWrite,
		config: orchestrionRetryProcessControlConfig{
			Transport:     "windows_handles",
			ReadEndpoint:  uint64(readHandle),
			WriteEndpoint: uint64(writeHandle),
		},
	}, nil
}
