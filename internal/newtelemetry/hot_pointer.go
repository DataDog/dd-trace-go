// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package newtelemetry

import (
	"sync"
)

// hotPointer is a type that allows for atomic swapping of a value while keeping the other on standby to prevent allocations.
type hotPointer[T any] struct {
	// value is the current value that can be extracted.
	value *T
	// standby is the value that will be swapped in.
	standby *T
	// writeMu is used to lock the value.
	writeMu sync.Mutex
	// swapMu is used to lock the swap.
	swapMu sync.Mutex
}

// Lock take the lock and return the current value
func (hp *hotPointer[T]) Lock() *T {
	hp.writeMu.Lock()
	return hp.value
}

// StandbyValue returns the standby value WITHOUT locking. Which means it cannot be used concurrently.
func (hp *hotPointer[T]) StandbyValue() *T {
	return hp.standby
}

// Swap swaps the current value with the standby value and return the standby value using the lock.
// the value returned is NOT protected by the lock.
func (hp *hotPointer[T]) Swap() *T {
	hp.Lock()
	defer hp.Unlock()
	hp.value, hp.standby = hp.standby, hp.value
	return hp.standby
}
