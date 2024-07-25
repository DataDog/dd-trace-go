// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import "sync"

type tag struct {
	key     string
	value   any // This adds an overhead of 8 bytes when storing a float64.
	sibling *tag
}

type tagPool struct {
	mu   sync.Mutex
	tail *tag
}

var tagsPool = &tagPool{}

func (tp *tagPool) Get() *tag {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	if tp.tail == nil {
		return &tag{}
	}
	tt := tp.tail
	tp.tail = tt.sibling
	tt.sibling = nil
	return tt
}

func (tp *tagPool) Put(tt *tag) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	tt.sibling = tp.tail
	tp.tail = tt
}

func getTagFromPool() *tag {
	return tagsPool.Get()
}

func putTagToPool(tt *tag) {
	tt.key = ""
	tt.value = nil
	tt.sibling = nil
	tagsPool.Put(tt)
}

// spanTags is an append-only linked list of tags.
// Its usage assumes the following:
// - only works under a locked span.
// - pool is pre-allocated to a sensible size, taking into account that the tracer may be used concurrently.
type spanTags struct {
	head *tag
	tail *tag
}

func (st *spanTags) append(key string, value any) {
	tt := getTagFromPool()
	tt.key = key
	tt.value = value
	if st.head == nil {
		st.head = tt
		st.tail = tt
		return
	}
	tail := st.tail
	tail.sibling = tt
	st.tail = tt
}

func (st *spanTags) reset() {
	tt := st.head
	for {
		nt := tt.sibling
		putTagToPool(tt)
		if nt == nil {
			break
		}
		tt = nt
	}
	st.head = nil
	st.tail = nil
}
