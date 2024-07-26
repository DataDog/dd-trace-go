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

type tag[T metric | meta] struct {
	key     string
	value   T // This adds an overhead of 8 bytes when storing a float64.
	sibling unsafe.Pointer
}

type tagPool struct {
	mu   sync.Mutex
	tail unsafe.Pointer
}

var tagsPool = &tagPool{}

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

func (tp *tagPool) Put(ptr unsafe.Pointer) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	tt := (*tag[meta])(ptr)
	tt.sibling = tp.tail
	tp.tail = ptr
}

func getTagFromPool[T meta | metric]() *tag[T] {
	ptr := tagsPool.Get()
	return (*tag[T])(ptr)
}

func putTagToPool[T meta | metric](tt *tag[T]) {
	var zero T
	tt.key = ""
	tt.value = zero
	tt.sibling = nil
	tagsPool.Put(unsafe.Pointer(tt))
}

// spanTags is an append-only linked list of tags.
// Its usage assumes the following:
// - only works under a locked span.
// - pool is pre-allocated to a sensible size, taking into account that the tracer may be used concurrently.
type spanTags struct {
	head unsafe.Pointer
	tail unsafe.Pointer
}

func (st *spanTags) Head() *tag[meta] {
	return (*tag[meta])(st.head)
}

func (st *spanTags) Tail() *tag[meta] {
	return (*tag[meta])(st.tail)
}

func (st *spanTags) AppendMeta(key string, value string) {
	tt := getTagFromPool[meta]()
	tt.key = key
	tt.value.value = value
	ptr := unsafe.Pointer(tt)
	st.updateTail(ptr)
}

func (st *spanTags) appendMetric(key string, value float64) {
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

func (st *spanTags) reset() {
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
