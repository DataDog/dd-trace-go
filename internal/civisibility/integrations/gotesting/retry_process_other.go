// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build !unix && !windows

package gotesting

import (
	"os"
	"os/exec"
)

// ProcessRetryContainmentSupported reports whether this platform can contain
// ordinary retry-child descendants.
func ProcessRetryContainmentSupported() bool { return false }

func processRetryChildStartsSuspended() bool { return false }

func prepareProcessRetryControlTransport(*exec.Cmd) (*processRetryControlTransport, error) {
	return nil, errProcessRetryTreeUnsupported
}

func openProcessRetryChildControlTransport(processRetryControlConfig) (*os.File, *os.File, error) {
	return nil, nil, errProcessRetryTreeUnsupported
}

func setProcessGroupForCommand(cmd *exec.Cmd) error {
	if cmd == nil {
		return errProcessRetryProcessNotStarted
	}
	return errProcessRetryTreeUnsupported
}

func attachProcessTree(*exec.Cmd) error { return nil }

func resumeProcessTree(*exec.Cmd) error { return nil }

func releaseProcessTree(*exec.Cmd) error { return nil }

func terminateProcessTree(cmd *exec.Cmd) error {
	return killDirectChild(cmd)
}

func killProcessTree(cmd *exec.Cmd) error {
	return killDirectChild(cmd)
}
