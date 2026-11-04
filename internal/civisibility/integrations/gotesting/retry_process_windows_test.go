// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build windows

package gotesting

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessRetryWindowsAttachUsesRetainedProcessHandle(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$", "-test.count=1")
	cmd.Env = append(os.Environ(), "Bypass=true")
	require.NoError(t, setProcessGroupForCommand(cmd))
	pid := 0
	t.Cleanup(func() {
		if cmd.Process != nil && pid > 0 {
			cmd.Process.Pid = pid
			_ = killDirectChild(cmd)
		}
		_ = releaseProcessTree(cmd)
	})

	require.NoError(t, cmd.Start())
	pid = cmd.Process.Pid
	require.NoError(t, retainProcessTreeHandle(cmd))

	cmd.Process.Pid = 0
	require.NoError(t, attachProcessTree(cmd))
	cmd.Process.Pid = pid
	require.NoError(t, resumeProcessTree(cmd))
	require.NoError(t, cmd.Wait())
	require.NoError(t, releaseProcessTree(cmd))
}
