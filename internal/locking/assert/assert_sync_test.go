// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build !deadlock && !debug
// +build !deadlock,!debug

package assert

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

// Test that the mutexasserts versions work correctly

func TestMutexLockedWhenLocked(t *testing.T) {
	m := &locking.Mutex{}
	m.Lock()
	defer m.Unlock()

	// This should not panic when the mutex is actually locked
	MutexLocked(m)
}

func TestRWMutexLockedWhenLocked(t *testing.T) {
	m := &locking.RWMutex{}
	m.Lock()
	defer m.Unlock()

	// This should not panic when the mutex is actually locked
	RWMutexLocked(m)
}

func TestRWMutexRLockedWhenRLocked(t *testing.T) {
	m := &locking.RWMutex{}
	m.RLock()
	defer m.RUnlock()

	// This should not panic when the mutex is actually RLocked
	RWMutexRLocked(m)
}

func TestRWMutexRLockedWhenWriteLocked(t *testing.T) {
	m := &locking.RWMutex{}
	m.Lock()
	defer m.Unlock()

	// This should not panic when the mutex has a write lock
	// (go-mutexasserts considers write lock as also satisfying RLocked)
	RWMutexRLocked(m)
}
