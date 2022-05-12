#include <stddef.h>
#include <stdint.h>

#define MAXIMUM 256

struct range {
        uintptr_t low;
        uintptr_t high;
};

typedef struct {
        size_t count;
        struct range ranges[MAXIMUM];
} cgo_heap_profiler_malloc_ranges;

static cgo_heap_profiler_malloc_ranges ranges = {
        .count=0,
};

// cgo_heap_profiler_malloc_mark_unsafe marks program counters between low and
// high as containing a function which is not safe to profile.
void cgo_heap_profiler_malloc_mark_unsafe(uintptr_t low, uintptr_t high) {
        if (ranges.count == MAXIMUM) {
                return;
        }
        struct range range = {
                .low=low,
                .high=high,
        };
        ranges.ranges[ranges.count++] = range;
}

// cgo_heap_profiler_malloc_check checks if the program counter is in a
// call where malloc is not safe to profile
int cgo_heap_profiler_malloc_check_unsafe(uintptr_t pc) {
        for (size_t i = 0; i < ranges.count; i++) {
                if ((ranges.ranges[i].low <= pc) && (ranges.ranges[i].high >= pc)) {
                        return 1;
                }
        }
        return 0;
}