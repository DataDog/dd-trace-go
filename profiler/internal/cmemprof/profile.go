// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

//go:build cgo
// +build cgo

// Package cmemprof profiles C memory allocations (malloc, calloc, realloc, etc.)
//
// Importing this package in a program will replace malloc, calloc, and realloc
// with wrappers which will sample allocations and record them to a profile.
//
// To use this package:
//
//	f, _ := os.Create("cmem.pprof")
//	var profiler cmemprof.Profile
//	profiler.Start(500)
//	// ... do allocations
//	profile, err := profiler.Stop()
//
// Building this package on Linux requires a non-standard linker flag to wrap
// the alloaction functions. For Go versions < 1.15, cgo won't allow the flag so
// it has to be explicitly allowed by setting the following environment variable
// when building a program that uses this package:
//
//	export CGO_LDFLAGS_ALLOW="-Wl,--wrap=.*"
package cmemprof

/*
#cgo CFLAGS: -g -O2 -fno-omit-frame-pointer
#cgo linux LDFLAGS: -pthread
#cgo linux LDFLAGS: -Wl,--wrap=calloc
#cgo linux LDFLAGS: -Wl,--wrap=malloc
#cgo linux LDFLAGS: -Wl,--wrap=realloc
#cgo linux LDFLAGS: -Wl,--wrap=valloc
#cgo linux LDFLAGS: -Wl,--wrap=aligned_alloc
#cgo linux LDFLAGS: -Wl,--wrap=posix_memalign
#cgo darwin LDFLAGS: -ldl -pthread
#include <stdint.h> // for uintptr_t

#include "profiler.h"
*/
import "C"

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/pprof/profile"

	"gopkg.in/DataDog/dd-trace-go.v1/profiler/internal/extensions"
)

func init() {
	extensions.SetCAllocationProfiler(new(Profile))
}

// DefaultSamplingRate is the sampling rate, in bytes allocated, which will be
// used if a profile is started with Profile.SampleRate == 0
const DefaultSamplingRate = 2 * 1024 * 1024 // 2 MB

type callStack [32]uintptr

type allocationEvent struct {
	// bytes is the total number of bytes allocated
	bytes uint
	// count is the number of times this event has been observed
	count int
	// frames is the number of calls in the call stack. callStack is a
	// fixed-size array for use as a map key, but the actuall call stack
	// doesn't necessarily have len(callStack) frames
	frames int
}

// Profile provides access to a C memory allocation profiler based on
// instrumenting malloc, calloc, and realloc.
type Profile struct {
	mu      sync.Mutex
	active  bool
	samples map[callStack]*allocationEvent

	// t fires periodically to trigger collectExtra
	t *time.Timer
	// extra holds allocation call stacks which can't be collected by
	// calling into Go
	extra [2048]C.struct_stack_buffer

	// SamplingRate is the value, in bytes, such that an average of one
	// sample will be recorded for every SamplingRate bytes allocated.  An
	// allocation of N bytes will be recorded with probability min(1, N /
	// SamplingRate).
	SamplingRate int
}

var activeProfile atomic.Value

// Start begins profiling C memory allocations.
func (c *Profile) Start(rate int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active {
		return
	}
	c.active = true
	// We blow away the samples from the previous round of profiling so that
	// the final profile returned by Stop only has the allocations between
	// Stop and the preceding call to Start
	//
	// TODO: can we make this more efficient and not have to re-build
	// everything from scratch for each round of profiling?
	c.samples = make(map[callStack]*allocationEvent)
	if rate == 0 {
		rate = DefaultSamplingRate
	}
	if c.t == nil {
		c.t = time.AfterFunc(100*time.Millisecond, c.collectExtra)
	} else {
		c.t.Reset(100 * time.Millisecond)
	}
	c.SamplingRate = rate
	activeProfile.Store(c)
	C.cgo_heap_profiler_set_sampling_rate(C.size_t(rate))
}

func (c *Profile) collectExtra() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active {
		return
	}
	n := C.cgo_heap_profiler_read_stack_traces(&c.extra[0], C.int(len(c.extra)))
	for _, sample := range c.extra[:n] {
		var pcs callStack
		var frames int
		for i, pc := range sample.pcs {
			if pc == 0 {
				break
			}
			frames++
			pcs[i] = uintptr(pc)
		}
		c.insertUnlocked(pcs, frames, uint(sample.size))
	}
	c.t.Reset(100 * time.Millisecond)
}

func (c *Profile) insert(pcs callStack, frames int, size uint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.insertUnlocked(pcs, frames, size)
}

func (c *Profile) insertUnlocked(pcs callStack, frames int, size uint) {
	event := c.samples[pcs]
	if event == nil {
		event = new(allocationEvent)
		event.frames = frames
		c.samples[pcs] = event
	}
	rate := uint(c.SamplingRate)
	if size >= rate {
		event.bytes += size
		event.count++
	} else {
		// The allocation was sample with probability p = size / rate.
		// So we assume there were actually (1 / p) similar allocations
		// for a total size of (1 / p) * size = rate
		event.bytes += rate
		event.count += int(float64(rate) / float64(size))
	}
}

// Stop cancels memory profiling and waits for the profile to be written to the
// io.Writer passed to Start. Returns any error from writing the profile.
func (c *Profile) Stop() (*profile.Profile, error) {
	C.cgo_heap_profiler_set_sampling_rate(0)
	c.collectExtra()
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active {
		return nil, fmt.Errorf("profiling isn't started")
	}
	c.active = false
	c.t.Stop()
	p := c.build()
	err := p.CheckValid()
	if err != nil {
		return nil, fmt.Errorf("bad profile: %s", err)
	}
	return p, nil
}
