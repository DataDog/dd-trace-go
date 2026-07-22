// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build unix

package gotesting

import (
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetProcessGroupForCommandPreservesSysProcAttr(t *testing.T) {
	attr := &syscall.SysProcAttr{}
	cmd := &exec.Cmd{SysProcAttr: attr}

	require.NoError(t, setProcessGroupForCommand(cmd))
	require.Same(t, attr, cmd.SysProcAttr)
	require.True(t, cmd.SysProcAttr.Setpgid)
}
