// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build !deadlock
// +build !deadlock

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
	mutexasserts.AssertRWMutexRLocked(m)
}
