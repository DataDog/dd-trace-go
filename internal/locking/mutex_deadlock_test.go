// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build deadlock
// +build deadlock

package locking

import (
	"sync"
	"testing"
)

// TestMutexInterface verifies that locking.Mutex satisfies sync.Locker interface
func TestMutexInterface(t *testing.T) {
	var m Mutex

	// This should compile because Mutex implements sync.Locker
	var _ sync.Locker = &m

	// Test basic lock/unlock
	m.Lock()
	m.Unlock()
}

// TestRWMutexInterface verifies that locking.RWMutex works correctly
func TestRWMutexInterface(t *testing.T) {
	var m RWMutex

	// This should compile because RWMutex implements sync.Locker
	var _ sync.Locker = &m

	// Test write lock
	m.Lock()
	m.Unlock()

	// Test read lock
	m.RLock()
	m.RUnlock()

	// Test RLocker
	rl := m.RLocker()
	rl.Lock()   // This is actually RLock
	rl.Unlock() // This is actually RUnlock
}
