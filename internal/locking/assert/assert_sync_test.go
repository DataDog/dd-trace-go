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

// Test that the no-op versions don't panic or cause issues

func TestMutexLockedNoOp(t *testing.T) {
	m := &locking.Mutex{}

	// Should not panic on unlocked mutex
	MutexLocked(m)

	m.Lock()
	defer m.Unlock()

	// Should not panic on locked mutex
	MutexLocked(m)
}

func TestRWMutexLockedNoOp(t *testing.T) {
	m := &locking.RWMutex{}

	// Should not panic on unlocked mutex
	RWMutexLocked(m)

	m.Lock()
	defer m.Unlock()

	// Should not panic on locked mutex
	RWMutexLocked(m)
}

func TestRWMutexRLockedNoOp(t *testing.T) {
	m := &locking.RWMutex{}

	// Should not panic on unlocked mutex
	RWMutexRLocked(m)

	m.RLock()
	defer m.RUnlock()

	// Should not panic on read-locked mutex
	RWMutexRLocked(m)
}
