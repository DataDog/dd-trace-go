// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package extensions provides an interface for using optional features.
//
// Features such as C allocation profiling might require cgo, unsafe code, and
// external non-Go dependencies which might not be desirable for typical users.
// The main profiler package should not import any package implementing such
// features directly as doing so may have undesired side effects.  This package
// provides a bridge between the implementation of such optional features and
// the main profiler package.
package extensions

import (
	"sync"

	"github.com/google/pprof/profile"
)

// CAllocationProfiler is the interface for profiling allocations done through
// the standard malloc/calloc/realloc APIs.
//
// A CAllocationProfiler implementation is not necessarily safe to use from
// multiple goroutines concurrently.
type CAllocationProfiler interface {
	// Start begins sampling C allocations at the given rate, in bytes.
	// There will be an average of one sample for every rate bytes
	// allocated.
	Start(rate int)
	// Stop cancels ongoing C allocation profiling and returns the resulting
	// profile. The profile will have the correct sample types such that it
	// can be merged with the Go heap profile. Returns a non-nil error if
	// any part of the profiling failed.
	Stop() (*profile.Profile, error)
}

var (
	mu                  sync.Mutex
	cAllocationProfiler CAllocationProfiler
)

// GetCAllocationProfiler returns the currently registered C allocation
// profiler, if one is registered.
func GetCAllocationProfiler() (impl CAllocationProfiler, registered bool) {
	mu.Lock()
	defer mu.Unlock()
	if cAllocationProfiler == nil {
		return nil, false
	}
	return cAllocationProfiler, true
}

// SetCAllocationProfiler registers a C allocation profiler implementation.
func SetCAllocationProfiler(c CAllocationProfiler) {
	mu.Lock()
	defer mu.Unlock()
	cAllocationProfiler = c
}
