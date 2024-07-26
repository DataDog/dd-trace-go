// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"sync"
	"unsafe"
)

type meta struct {
	value string
}

type metric struct {
	magicNumber int64
	value       float64
}

// tag is a key-value pair with a sibling pointer to the next tag.
// This is the foundational block of the linked list that spanTags
// relies on.
//
// It's a generic type but the allowed types must have a size of 16 bytes.
// This is because the linked list uses unsafe.Pointer to link the tags,
// and we need to introduce a magic number for any value that is not string
// to identify it from the memory pointer
type tag[T metric | meta] struct {
	key     string
	value   T
	sibling unsafe.Pointer
}

// tagPool is a linked list of tags that are not in use.
// It's used to recycle tags and avoid unnecessary allocations.
//
// It's a better suit than sync.Pool because the tags can be linked
// together and we can avoid the overhead of sync.Pool's more complex logic.
type tagPool struct {
	mu   sync.Mutex
	tail unsafe.Pointer
}

// Get returns a tag's unsafe.Pointer from the pool.
func (tp *tagPool) Get() unsafe.Pointer {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	if tp.tail == nil {
		return unsafe.Pointer(&tag[meta]{})
	}
	ptr := tp.tail
	tt := (*tag[meta])(ptr)
	tp.tail = tt.sibling
	tt.sibling = nil
	return ptr
}

// Put puts a tag's unsafe.Pointer back into the pool.
func (tp *tagPool) Put(ptr unsafe.Pointer) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	tt := (*tag[meta])(ptr)
	tt.sibling = tp.tail
	tp.tail = ptr
}

var tagsPool = &tagPool{}

// getTagFromPool returns a tag from the pool, casting it to the desired type.
func getTagFromPool[T meta | metric]() *tag[T] {
	ptr := tagsPool.Get()
	return (*tag[T])(ptr)
}

// putTagToPool puts a tag back into the pool.
func putTagToPool[T meta | metric](tt *tag[T]) {
	var zero T
	tt.key = ""
	tt.value = zero
	tt.sibling = nil
	tagsPool.Put(unsafe.Pointer(tt))
}

// spanTags is an append-only linked list of tags.
// Its usage assumes the following:
// - it works under a locked span.
// - pool is pre-allocated to a sensible size, taking into account that the tracer may be used concurrently.
type spanTags struct {
	head unsafe.Pointer
	tail unsafe.Pointer
}

// Head returns the first tag in the linked list.
func (st *spanTags) Head() *tag[meta] {
	// This is safe because we know that all tags have the same memory layout.
	return (*tag[meta])(st.head)
}

// Tail returns the last tag in the linked list.
func (st *spanTags) Tail() *tag[meta] {
	// This is safe because we know that all tags have the same memory layout.
	return (*tag[meta])(st.tail)
}

// AppendMeta appends a string tag to the linked list.
func (st *spanTags) AppendMeta(key string, value string) {
	tt := getTagFromPool[meta]()
	tt.key = key
	tt.value.value = value
	ptr := unsafe.Pointer(tt)
	st.updateTail(ptr)
}

// AppendMetric appends a float64 tag to the linked list.
func (st *spanTags) AppendMetric(key string, value float64) {
	tt := getTagFromPool[metric]()
	tt.key = key
	tt.value.value = value
	ptr := unsafe.Pointer(tt)
	st.updateTail(ptr)
}

func (st *spanTags) updateTail(ptr unsafe.Pointer) {
	if st.head == nil {
		st.head = ptr
		st.tail = ptr
		return
	}
	tail := (*tag[meta])(st.tail)
	tail.sibling = ptr
	st.tail = ptr
}

// Reset releases all tags in the linked list back to the pool.
func (st *spanTags) Reset() {
	tt := st.Head()
	for {
		nt := tt.sibling
		putTagToPool(tt)
		if nt == nil {
			break
		}
		tt = (*tag[meta])(nt)
	}
	st.head = nil
	st.tail = nil
}
