// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build debug && !deadlock

package assert

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

// Note: go-mutexasserts uses os.Exit(1) instead of panic when assertions fail,
// so we can't test the failure cases in unit tests as they would terminate the test process.
// We only test the success cases here.

func TestMutexLockedWhenLockedDebug(t *testing.T) {
	m := &locking.Mutex{}
	m.Lock()
	defer m.Unlock()

	// This should not exit when the mutex is actually locked
	MutexLocked(m)
}

func TestRWMutexLockedWhenLockedDebug(t *testing.T) {
	m := &locking.RWMutex{}
	m.Lock()
	defer m.Unlock()

	// This should not exit when the mutex is actually locked
	RWMutexLocked(m)
}

func TestRWMutexRLockedWhenRLockedDebug(t *testing.T) {
	m := &locking.RWMutex{}
	m.RLock()
	defer m.RUnlock()

	// This should not exit when the mutex is actually RLocked
	RWMutexRLocked(m)
}

func TestRWMutexRLockedWhenWriteLockedDebug(t *testing.T) {
	m := &locking.RWMutex{}
	m.Lock()
	defer m.Unlock()

	// This should not exit when the mutex has a write lock
	// (go-mutexasserts considers write lock as also satisfying RLocked)
	RWMutexRLocked(m)
}
