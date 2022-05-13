// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include <pthread.h>

#include "profiler.h"

// sampling_rate is the portion of allocations to sample.
atomic_int sampling_rate;

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

// profile_allocation_checked is similar to profile_allocation, but has a
// fallback for frame pointer unwinding if the provided return address points to
// a function where profiling by calling into Go is unsafe (see
// unsafe_malloc_marker.go). The return address must be passed in this way
// because __builtin_return_address(n) may crash for n > 0. This means we can't
// check the return address anywhere deeper in the call stack and must check in
// the malloc wrapper.
void profile_allocation_checked(size_t size, void *ret_addr) {
	size_t rate = atomic_load_explicit(&sampling_rate, memory_order_relaxed);
	if (rate == 0) {
		return;
	}
	if (should_sample(rate, size) != 0) {
		if (cgo_heap_profiler_malloc_check_unsafe((uintptr_t) ret_addr) == 1) {
			if (safe_fpunwind == 1) {
				fpunwind(__builtin_frame_address(0), size);
			}
			return;
		}
		recordAllocationSample(size);
	}
}


void cgo_heap_profiler_set_sampling_rate(int hz) {
	if (hz <= 0) {
		hz = 0;
	}
	return atomic_store(&sampling_rate, hz);
}

struct stack_buffers {
	pthread_mutex_t mu;
	struct stack_buffer buffers[2048];
	int cursor;
};

static struct stack_buffers stack_buffers = {
	.mu=PTHREAD_MUTEX_INITIALIZER,
};

static void fpunwind(void *pc, size_t size) {
	int n = 0;
	void **fp = pc;
	uintptr_t buf[32] = {0};
	while ((fp != NULL) && (n < 32)) {
		void *pc = *((void **)((void*)fp+8));
		if (pc != NULL) {
			buf[n++] = (uintptr_t) pc;
		}
		fp = *(fp);
	}
	pthread_mutex_lock(&stack_buffers.mu);
	int i = stack_buffers.cursor;
	memcpy(stack_buffers.buffers[i].pcs, buf, 32*sizeof(uintptr_t));
	stack_buffers.buffers[i].size = size;
	stack_buffers.buffers[i].active = 1;
	stack_buffers.cursor = (i + 1) % 2048;
	pthread_mutex_unlock(&stack_buffers.mu);
}

int cgo_heap_profiler_read_stack_traces(struct stack_buffer *buffers, int max) {
	int n = 0;
	pthread_mutex_lock(&stack_buffers.mu);
	for (int i = 0; i < 2048; i++) {
		struct stack_buffer *b = &stack_buffers.buffers[i];
		if (b->active == 0) {
			continue;
		}
		b->active = 0;
		memcpy(&buffers[n++], b, sizeof(struct stack_buffer));
		if (n == max) {
			break;
		}
	}
	pthread_mutex_unlock(&stack_buffers.mu);
	return n;
}