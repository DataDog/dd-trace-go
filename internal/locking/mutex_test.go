// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build !deadlock
// +build !deadlock

package locking

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

// TestMutexAsParameter verifies mutexes can be passed to functions expecting sync types
func TestMutexAsParameter(t *testing.T) {
	var m Mutex

	// When not using deadlock build tag, this is a type alias so it should work directly
	acceptSyncMutex := func(m *sync.Mutex) {
		m.Lock()
		m.Unlock()
	}

	// With type aliases, we can pass locking.Mutex directly to functions expecting sync.Mutex
	acceptSyncMutex(&m)
}

// TestMutexBlocksConcurrentAccess verifies that Mutex actually blocks
// concurrent access. If Lock/Unlock were no-ops, this test would fail.
func TestMutexBlocksConcurrentAccess(t *testing.T) {
	var m Mutex
	var counter int
	done := make(chan struct{})

	m.Lock()

	// Start a goroutine that tries to acquire the lock
	go func() {
		m.Lock()
		counter++
		m.Unlock()
		close(done)
	}()

	// Give the goroutine time to attempt the lock
	time.Sleep(50 * time.Millisecond)

	// Counter should still be 0 because goroutine is blocked
	if counter != 0 {
		t.Error("Mutex did not block concurrent access")
	}

	m.Unlock()

	// Wait for goroutine to complete
	<-done

	if counter != 1 {
		t.Errorf("Counter should be 1, got %d", counter)
	}
}

// TestRWMutexBlocksConcurrentWrite verifies RWMutex write lock behavior
func TestRWMutexBlocksConcurrentWrite(t *testing.T) {
	var m RWMutex
	var counter int
	done := make(chan struct{})

	m.Lock()

	go func() {
		m.Lock()
		counter++
		m.Unlock()
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)

	if counter != 0 {
		t.Error("RWMutex did not block concurrent write access")
	}

	m.Unlock()
	<-done

	if counter != 1 {
		t.Errorf("Counter should be 1, got %d", counter)
	}
}

// TestRWMutexAllowsConcurrentRead verifies RWMutex allows multiple readers
func TestRWMutexAllowsConcurrentRead(t *testing.T) {
	var m RWMutex
	var readCount int32
	var wg sync.WaitGroup

	m.RLock()

	// Start multiple reader goroutines
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.RLock()
			atomic.AddInt32(&readCount, 1)
			time.Sleep(10 * time.Millisecond)
			m.RUnlock()
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// All readers should have acquired the lock
	if atomic.LoadInt32(&readCount) != 3 {
		t.Errorf("Expected 3 concurrent readers, got %d", readCount)
	}

	m.RUnlock()
	wg.Wait()
}
