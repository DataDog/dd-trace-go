// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build unix

package gls

import (
	"os"
	"os/signal"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSignal tests that the GLS is correctly set even when the code comes from a signal handler.
func TestSignal(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("Orchestrion is not enabled")
	}

	expected := "I am inside a signal handler"

	set(nil)

	doneSigChan := make(chan struct{}, 1)
	checkChan := make(chan struct{}, 1)
	doneCheckChan := make(chan struct{}, 1)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGUSR1)

	go func() {
		<-sigChan
		set(expected)
		doneSigChan <- struct{}{}

		<-checkChan
		assert.Equal(t, expected, get())
		doneCheckChan <- struct{}{}
	}()

	_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
	<-doneSigChan
	checkChan <- struct{}{}
	<-doneCheckChan
}
