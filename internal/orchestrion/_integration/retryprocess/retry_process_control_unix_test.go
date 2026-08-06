// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build unix

package retryprocess

import (
	"os"
	"os/exec"
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
	readFD := 3 + len(cmd.ExtraFiles)
	writeFD := readFD + 1
	cmd.ExtraFiles = append(cmd.ExtraFiles, parentToChildRead, childToParentWrite)
	return &orchestrionRetryProcessControlTransport{
		read:       childToParentRead,
		write:      parentToChildWrite,
		childRead:  parentToChildRead,
		childWrite: childToParentWrite,
		config: orchestrionRetryProcessControlConfig{
			Transport:     "unix_pipes",
			ReadEndpoint:  uint64(readFD),
			WriteEndpoint: uint64(writeFD),
		},
	}, nil
}
