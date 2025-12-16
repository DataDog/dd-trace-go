// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build debug && !deadlock
// +build debug,!deadlock

package assert

import (
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/stretchr/testify/assert"
)

func TestMutexLockedPanicsWhenUnlockedDebug(t *testing.T) {
	tests := []struct {
		name string
		lock locking.TryLocker
	}{
		{
			name: "locking.Mutex panics when not locked",
			lock: &locking.Mutex{},
		},
		{
			name: "sync.Mutex panics when not locked",
			lock: &sync.Mutex{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.PanicsWithValue(t, "mutex not locked", func() {
				MutexLocked(tt.lock)
			})
		})
	}
}

func TestMutexLockedDoesNotPanicWhenLockedDebug(t *testing.T) {
	tests := []struct {
		name     string
		lock     locker
		lockMode lockMode
	}{
		{
			name:     "locking.Mutex does not panic when locked",
			lock:     &locking.RWMutex{}, // Use RWMutex to satisfy locker interface
			lockMode: lock,
		},
		{
			name:     "sync.Mutex does not panic when locked",
			lock:     &sync.RWMutex{}, // Use RWMutex to satisfy locker interface
			lockMode: lock,
		},
		{
			name:     "locking.RWMutex does not panic when write-locked",
			lock:     &locking.RWMutex{},
			lockMode: lock,
		},
		{
			name:     "sync.RWMutex does not panic when write-locked",
			lock:     &sync.RWMutex{},
			lockMode: lock,
		},
		{
			name:     "locking.RWMutex does not panic when read-locked",
			lock:     &locking.RWMutex{},
			lockMode: rlock,
		},
		{
			name:     "sync.RWMutex does not panic when read-locked",
			lock:     &sync.RWMutex{},
			lockMode: rlock,
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

			assert.NotPanics(t, func() {
				MutexLocked(tt.lock)
			})
		})
	}
}

func TestRWMutexRLockedPanicsWhenUnlockedDebug(t *testing.T) {
	tests := []struct {
		name string
		lock locking.TryRLocker
	}{
		{
			name: "locking.RWMutex panics when not read-locked",
			lock: &locking.RWMutex{},
		},
		{
			name: "sync.RWMutex panics when not read-locked",
			lock: &sync.RWMutex{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.PanicsWithValue(t, "rwmutex not read-locked", func() {
				RWMutexRLocked(tt.lock)
			})
		})
	}
}

func TestRWMutexRLockedDoesNotPanicWhenLockedDebug(t *testing.T) {
	tests := []struct {
		name     string
		lock     locker
		lockMode lockMode
	}{
		{
			name:     "locking.RWMutex does not panic when read-locked",
			lock:     &locking.RWMutex{},
			lockMode: rlock,
		},
		{
			name:     "sync.RWMutex does not panic when read-locked",
			lock:     &sync.RWMutex{},
			lockMode: rlock,
		},
		{
			name:     "locking.RWMutex does not panic when write-locked",
			lock:     &locking.RWMutex{},
			lockMode: lock,
		},
		{
			name:     "sync.RWMutex does not panic when write-locked",
			lock:     &sync.RWMutex{},
			lockMode: lock,
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

			assert.NotPanics(t, func() {
				RWMutexRLocked(tt.lock)
			})
		})
	}
}
