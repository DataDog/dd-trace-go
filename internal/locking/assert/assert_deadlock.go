// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build deadlock
// +build deadlock

package assert

import (
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

// When building with deadlock detection, these assertion functions are no-ops.
//
// Rationale:
// While these assertions verify lock state invariants (that locks are held at specific
// points in code), they are incompatible with go-deadlock's implementation. Attempting
// to use TryLock on an already-held lock triggers go-deadlock's recursive locking
// detection, causing false positives and test failures.
//
// The go-deadlock library and TryLock-based assertions serve complementary purposes:
// - go-deadlock: Detects deadlocks, lock ordering violations, and potential races
// - TryLock assertions: Verify lock state invariants at specific code points
//
// However, go-deadlock's aggressive detection conflicts with the TryLock mechanism used
// by these assertions. We maintain two separate test modes to ensure comprehensive coverage:
// - Default/debug builds: TryLock-based assertions verify lock state invariants
// - Deadlock builds: go-deadlock detects deadlocks and ordering issues
//
// This dual-mode approach provides thorough lock validation without false positives.

func MutexLocked(m locking.TryLocker) {
	// No-op: go-deadlock provides comprehensive runtime verification
}

func RWMutexRLocked(m locking.TryRLocker) {
	// No-op: go-deadlock provides comprehensive runtime verification
}
