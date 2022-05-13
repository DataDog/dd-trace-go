// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package testallocator

/*
#include <stdlib.h>

void *side_effect;

void doAlloc(size_t size) {
	// have some observable side effect of malloc so that it doesn't get
	// optimized away
	void *p = malloc(size);
	side_effect = p;
	free(p);
}
*/
import "C"

// DoAllocGo does an allocation with C.malloc, which is special because cgo
// creates a wrapper that panics on NULL returns from malloc and lets 0-byte
// allocations succeed.
//go:noinline
func DoAllocGo(size int) {
	p := C.malloc(C.size_t(size))
	C.free(p)
}

// DoAllocC does an allocation purely in C without invoking cgo's malloc
// wrapper.
//go:noinline
func DoAllocC(size int) {
	C.doAlloc(C.size_t(size))
}

// DoCalloc invokes calloc from Go, which should *not* be get a special wrapper.
//go:noinline
func DoCalloc(size int) {
	p := C.calloc(C.size_t(size), 1)
	C.free(p)
}
