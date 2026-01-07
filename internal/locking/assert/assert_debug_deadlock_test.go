// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build debug && deadlock
// +build debug,deadlock

package assert

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

func TestLockAssertionsDebugDeadlock(t *testing.T) {
	t.Run("locking.Mutex locked", func(t *testing.T) {
		m := &locking.Mutex{}
		m.Lock()
		defer m.Unlock()
		MutexLocked(m)
	})

	t.Run("locking.RWMutex write-locked", func(t *testing.T) {
		m := &locking.RWMutex{}
		m.Lock()
		defer m.Unlock()
		RWMutexLocked(m)
	})
}

func TestRLockAssertionsDebugDeadlock(t *testing.T) {
	t.Run("locking.RWMutex read-locked", func(t *testing.T) {
		m := &locking.RWMutex{}
		m.RLock()
		defer m.RUnlock()
		RWMutexRLocked(m)
	})

	t.Run("locking.RWMutex write-locked also satisfies RLocked", func(t *testing.T) {
		m := &locking.RWMutex{}
		m.Lock()
		defer m.Unlock()
		RWMutexRLocked(m)
	})
}
