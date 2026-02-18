// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build debug && !deadlock
// +build debug,!deadlock

package assert

import (
	"github.com/trailofbits/go-mutexasserts"

	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

func MutexLocked(m *locking.Mutex) {
	mutexasserts.AssertMutexLocked(m)
}

func RWMutexLocked(m *locking.RWMutex) {
	mutexasserts.AssertRWMutexLocked(m)
}

func RWMutexRLocked(m *locking.RWMutex) {
	// A write lock also satisfies the read lock requirement.
	// In debug builds, AssertRWMutexRLocked calls os.Exit(1) on failure,
	// so we first check both boolean conditions before falling through
	// to the assertion (which provides the failure message).
	if mutexasserts.RWMutexRLocked(m) || mutexasserts.RWMutexLocked(m) {
		return
	}
	mutexasserts.AssertRWMutexRLocked(m)
}
