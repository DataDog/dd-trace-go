// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build unix

package gotesting

import (
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestSetProcessGroupForCommandPreservesSysProcAttr(t *testing.T) {
	attr := &syscall.SysProcAttr{}
	cmd := &exec.Cmd{SysProcAttr: attr}

	require.NoError(t, setProcessGroupForCommand(cmd))
	require.Same(t, attr, cmd.SysProcAttr)
	require.True(t, cmd.SysProcAttr.Setpgid)
}

func TestProcessRetryChildControlTransportSetsCloseOnExec(t *testing.T) {
	parentRead, parentWrite, err := os.Pipe()
	require.NoError(t, err)
	childRead, childWrite, err := os.Pipe()
	require.NoError(t, err)
	defer parentRead.Close()
	defer parentWrite.Close()
	defer childRead.Close()
	defer childWrite.Close()
	readFD, err := unix.Dup(int(parentRead.Fd()))
	require.NoError(t, err)
	writeFD, err := unix.Dup(int(childWrite.Fd()))
	require.NoError(t, err)

	read, write, err := openProcessRetryChildControlTransport(processRetryControlConfig{
		Transport:     processRetryControlTransportUnixPipes,
		ReadEndpoint:  uint64(readFD),
		WriteEndpoint: uint64(writeFD),
	})
	require.NoError(t, err)
	defer read.Close()
	defer write.Close()

	readFlags, err := unix.FcntlInt(read.Fd(), unix.F_GETFD, 0)
	require.NoError(t, err)
	writeFlags, err := unix.FcntlInt(write.Fd(), unix.F_GETFD, 0)
	require.NoError(t, err)
	require.NotZero(t, readFlags&unix.FD_CLOEXEC)
	require.NotZero(t, writeFlags&unix.FD_CLOEXEC)
}
