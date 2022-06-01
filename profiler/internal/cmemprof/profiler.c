// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

#define _GNU_SOURCE
#include <dlfcn.h>
#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include <pthread.h>

#include "profiler.h"
#include "profiler_internal.h"

// sampling_rate is the portion of allocations to sample.
atomic_size_t sampling_rate;

__thread uint64_t rng_state;

static uint64_t rng_state_advance(uint64_t seed) {
	while (seed == 0) {
		// TODO: Initialize this better? Reading from /dev/urandom might
		// be a steep price to add to every new thread, but rand() is
		// really not a good source of randomness (and doesn't even
		// necessarily return 32 bits)
		uint64_t lo = rand();
		uint64_t hi = rand();
		seed = (hi << 32) | lo;
	}
	// xorshift RNG
	uint64_t x = seed;
	x ^= x << 13;
	x ^= x >> 17;
	x ^= x << 5;
	return x;
}

static int should_sample(size_t rate, size_t size) {
	if (rate == 1) {
		return 1;
	}
	if (size > rate) {
		return 1;
	}
	rng_state = rng_state_advance(rng_state);
	uint64_t check = rng_state % rate;
	return check <= size;
}

extern void recordAllocationSample(size_t size);
static void fpunwind(void *pc, size_t size);

static int safe_fpunwind = 0;

void cgo_heap_profiler_mark_fpunwind_safe(void) {
	safe_fpunwind = 1;
}

void profile_allocation(size_t size) {
	size_t rate = atomic_load_explicit(&sampling_rate, memory_order_relaxed);
	if (rate == 0) {
		return;
	}
	if (should_sample(rate, size) != 0) {
		recordAllocationSample(size);
	}
}

// is_unsafe_call checks whether the given return address is inside a function
// from which it is unsafe to call back into Go code.
//
// There are two such functions:
//
// * x_cgo_thread_start 
//      This function creates a new OS thread for cgo programs.  Calling back
//      into Go requires an "m" (OS thread) with a corresponding "g" (goroutine)
//      to run the Go code. But x_cgo_thread_start creates an "m" and calls
//      malloc (meaning it might be profiled) before the "m" has a "g". This
//      causes a crash.
// * C.malloc
//      The cgo tool generates wrapper functions for Go programs to call C code.
//      Since malloc is required by other cgo builtins like C.CString need to
//      call malloc internally, cgo generates a special hand-written wapper for
//      malloc. This wrapper lacks checks for whether the calling goroutine's 
//      stack has grown, which can happen if the C code calls back into Go. This
//      check is needed so the C function's return value can be returned on the
//      goroutine's stack. If the stack grows and malloc's return value goes in
//      the wrong place, the program can crash.
static int is_unsafe_call(void *ret_addr) {
	// TODO: Cache this whole check?
	Dl_info info = {0};
	if (dladdr(ret_addr, &info) == 0) {
		return 0;
	}
	const char *s = info.dli_sname;
	cgo_heap_profiler_debug("checking symbol %p: function %s", ret_addr, s);
	if (s == NULL) {
		return 0;
	}
	if (strcmp(s, "x_cgo_thread_start") == 0) {
		cgo_heap_profiler_debug("function %s is unsafe", s);
		return 1;
	}
        // Each package which calls C.malloc gets its own malloc wrapper. The
        // symbol will have a name like "_cgo_<PREFIX>_Cfunc__Cmalloc", where
        // <PREFIX> is a unique value for each package. We just look for the
        // suffix.
	int n = strlen(s);
	const char *suffix = "_Cfunc__Cmalloc";
	const int m = strlen(suffix);
	if (n < m) {
		return 0;
	}
	if (strcmp(s + (n - m), suffix) == 0) {
		cgo_heap_profiler_debug("function %s is unsafe", s);
		return 1;
	}
	return 0;
}

// profile_allocation_checked is similar to profile_allocation, but has a
// fallback for frame pointer unwinding if the provided return address points to
// a function where profiling by calling into Go is unsafe. (See is_unsafe_call)
// The return address must be passed in this way because
// __builtin_return_address(n) may crash for n > 0. This means we can't check
// the return address anywhere deeper in the call stack and must check in the
// malloc wrapper.
void profile_allocation_checked(size_t size, void *ret_addr) {
	size_t rate = atomic_load_explicit(&sampling_rate, memory_order_relaxed);
	if (rate == 0) {
		return;
	}

	if (should_sample(rate, size) != 0) {
		if (is_unsafe_call(ret_addr)) {
			return;
		}
		recordAllocationSample(size);
	}
}


void cgo_heap_profiler_set_sampling_rate(size_t hz) {
	if (hz <= 0) {
		hz = 0;
	}
	return atomic_store(&sampling_rate, hz);
}