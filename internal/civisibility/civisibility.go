// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package civisibility

import "sync/atomic"

type State int

const (
	StateUninitialized State = iota
	StateInitializing
	StateInitialized
	StateExiting
	StateExited
)

var (
	status     atomic.Int32
	isTestMode atomic.Bool
)

func GetState() State {
	// Get the state atomically
	return State(status.Load())
}

func SetState(state State) {
	// Set the state atomically
	status.Store(int32(state))
}

func SetTestMode() {
	isTestMode.Store(true)
}

func IsTestMode() bool {
	return isTestMode.Load()
}
