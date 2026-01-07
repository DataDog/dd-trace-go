// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build !deadlock
// +build !deadlock

package assert

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

// Benchmark Results (Apple M4 Max):
//
// Serial Performance:
//   - Mutex (go-mutexasserts):  0.25 ns/op  vs  TryLock: 1.54 ns/op  (~6x faster)
//   - RWMutex Write (go-mutexasserts): 0.26 ns/op  vs  TryLock: 1.78 ns/op  (~7x faster)
//   - RWMutex Read (go-mutexasserts):  0.26 ns/op  (TryLock cannot detect read locks)
//
// Parallel Performance:
//   - Mutex (go-mutexasserts):  0.028 ns/op  vs  TryLock: 0.159 ns/op  (~6x faster)
//   - RWMutex Write (go-mutexasserts): 0.025 ns/op  vs  TryLock: 0.173 ns/op  (~7x faster)
//   - RWMutex Read (go-mutexasserts):  0.024 ns/op
//
// Conclusion: go-mutexasserts is significantly faster because it uses atomic operations
// to inspect lock state without attempting lock acquisition, while TryLock must actually
// attempt to acquire the lock. Additionally, TryLock cannot distinguish between unlocked
// and read-locked states for RWMutex, making it unsuitable for RWMutexRLocked assertions.

// tryLockBasedMutexLocked is a TryLock-based implementation for comparison
func tryLockBasedMutexLocked(m *locking.Mutex) {
	if m.TryLock() {
		m.Unlock()
		panic("mutex not locked")
	}
}

// tryLockBasedRWMutexLocked is a TryLock-based implementation for comparison
func tryLockBasedRWMutexLocked(m *locking.RWMutex) {
	if m.TryLock() {
		m.Unlock()
		panic("rwmutex not locked")
	}
}

// Benchmark go-mutexasserts approach for Mutex
func BenchmarkMutexLocked_GoMutexAsserts(b *testing.B) {
	m := &locking.Mutex{}
	m.Lock()
	defer m.Unlock()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MutexLocked(m)
	}
}

// Benchmark TryLock-based approach for Mutex
func BenchmarkMutexLocked_TryLock(b *testing.B) {
	m := &locking.Mutex{}
	m.Lock()
	defer m.Unlock()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tryLockBasedMutexLocked(m)
	}
}

// Benchmark go-mutexasserts approach for RWMutex write lock
func BenchmarkRWMutexLocked_GoMutexAsserts(b *testing.B) {
	m := &locking.RWMutex{}
	m.Lock()
	defer m.Unlock()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RWMutexLocked(m)
	}
}

// Benchmark TryLock-based approach for RWMutex write lock
func BenchmarkRWMutexLocked_TryLock(b *testing.B) {
	m := &locking.RWMutex{}
	m.Lock()
	defer m.Unlock()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tryLockBasedRWMutexLocked(m)
	}
}

// Benchmark go-mutexasserts approach for RWMutex read lock
func BenchmarkRWMutexRLocked_GoMutexAsserts(b *testing.B) {
	m := &locking.RWMutex{}
	m.RLock()
	defer m.RUnlock()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RWMutexRLocked(m)
	}
}

// Benchmark parallel contention scenario for Mutex
func BenchmarkMutexLocked_Parallel_GoMutexAsserts(b *testing.B) {
	m := &locking.Mutex{}
	m.Lock()
	defer m.Unlock()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			MutexLocked(m)
		}
	})
}

// Benchmark parallel contention scenario for Mutex with TryLock
func BenchmarkMutexLocked_Parallel_TryLock(b *testing.B) {
	m := &locking.Mutex{}
	m.Lock()
	defer m.Unlock()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tryLockBasedMutexLocked(m)
		}
	})
}

// Benchmark parallel contention scenario for RWMutex write lock
func BenchmarkRWMutexLocked_Parallel_GoMutexAsserts(b *testing.B) {
	m := &locking.RWMutex{}
	m.Lock()
	defer m.Unlock()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			RWMutexLocked(m)
		}
	})
}

// Benchmark parallel contention scenario for RWMutex write lock with TryLock
func BenchmarkRWMutexLocked_Parallel_TryLock(b *testing.B) {
	m := &locking.RWMutex{}
	m.Lock()
	defer m.Unlock()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tryLockBasedRWMutexLocked(m)
		}
	})
}

// Benchmark parallel contention scenario for RWMutex read lock
func BenchmarkRWMutexRLocked_Parallel_GoMutexAsserts(b *testing.B) {
	m := &locking.RWMutex{}
	m.RLock()
	defer m.RUnlock()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			RWMutexRLocked(m)
		}
	})
}
