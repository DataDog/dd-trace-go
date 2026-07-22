// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build !unix && !windows

package retryprocess

import (
	"errors"
	"os/exec"
)

func prepareOrchestrionRetryProcessControlTransport(*exec.Cmd) (*orchestrionRetryProcessControlTransport, error) {
	return nil, errors.New("process retry fixture control transport is unsupported")
}
