#ifndef PROFILER_H
#define PROFILER_H
#include <stddef.h>
#include <stdint.h>

// cgo_heap_profiler_set_sampling_rate configures profiling to capture 1/hz of
// allocations, and returns the previous rate. If hz <= 0, then sampling is
// disabled.
void cgo_heap_profiler_set_sampling_rate(int hz);

int cgo_heap_profiler_malloc_check_unsafe(uintptr_t pc);
void cgo_heap_profiler_malloc_mark_unsafe(uintptr_t low, uintptr_t high);

struct stack_buffer {
	uintptr_t pcs[32];
	size_t size;
	int active;
};

int cgo_heap_profiler_read_stack_traces(struct stack_buffer *buffers, int max);

#endif
