// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build debug && deadlock
// +build debug,deadlock

package assert

import (
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

func TestLockAssertionsDebugDeadlock(t *testing.T) {
	tests := []struct {
		name       string
		lock       locker
		lockMode   lockMode
		assertFunc func(locking.TryLocker)
	}{
		{
			name:       "locking.Mutex locked",
			lock:       &locking.RWMutex{}, // Use RWMutex to satisfy locker interface
			lockMode:   lock,
			assertFunc: MutexLocked,
		},
		{
			name:       "sync.Mutex locked",
			lock:       &sync.RWMutex{}, // Use RWMutex to satisfy locker interface
			lockMode:   lock,
			assertFunc: MutexLocked,
		},
		{
			name:       "locking.RWMutex write-locked",
			lock:       &locking.RWMutex{},
			lockMode:   lock,
			assertFunc: MutexLocked,
		},
		{
			name:       "sync.RWMutex write-locked",
			lock:       &sync.RWMutex{},
			lockMode:   lock,
			assertFunc: MutexLocked,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.lockMode == lock {
				tt.lock.Lock()
				defer tt.lock.Unlock()
			} else {
				tt.lock.RLock()
				defer tt.lock.RUnlock()
			}

			// Should not panic/exit when lock is in expected state
			tt.assertFunc(tt.lock)
		})
	}
}

func TestRLockAssertionsDebugDeadlock(t *testing.T) {
	tests := []struct {
		name       string
		lock       locker
		lockMode   lockMode
		assertFunc func(locking.TryRLocker)
	}{
		{
			name:       "locking.RWMutex read-locked",
			lock:       &locking.RWMutex{},
			lockMode:   rlock,
			assertFunc: RWMutexRLocked,
		},
		{
			name:       "sync.RWMutex read-locked",
			lock:       &sync.RWMutex{},
			lockMode:   rlock,
			assertFunc: RWMutexRLocked,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.lockMode == lock {
				tt.lock.Lock()
				defer tt.lock.Unlock()
			} else {
				tt.lock.RLock()
				defer tt.lock.RUnlock()
			}

			// Should not panic/exit when lock is in expected state
			tt.assertFunc(tt.lock)
		})
	}
}
