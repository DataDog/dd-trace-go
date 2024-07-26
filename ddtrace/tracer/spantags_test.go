// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"testing"
	"unsafe"
)

func TestSpanTagsSet(t *testing.T) {
	var st spanTags
	st.AppendMeta("key1", "value1")
	st.appendMetric("key2", 0.1)
	st.appendMetric("key3", 1)
	st.AppendMeta("key2", "value2")
	head := st.Head()
	if head == nil {
		t.Fatal()
	}
	if head.key != "key1" {
		t.Fatal()
	}
	if head.value.value != "value1" {
		t.Fatal()
	}
	tail := st.Tail()
	if tail == nil {
		t.Fatal()
	}
	if tail.key != "key2" {
		t.Fatal()
	}
	if tail.value.value != "value2" {
		t.Fatal()
	}
}

func TestSpanTagsReset(t *testing.T) {
	var (
		st    spanTags
		elems = 100
	)
	for i := 0; i < elems; i++ {
		st.AppendMeta("key", "value")
	}
	tags := make([]*tag[meta], elems)
	tt := st.Head()
	for i := range tags {
		tags[i] = tt
		tt = (*tag[meta])(tt.sibling)
	}
	st.reset()
	head := st.Head()
	if head != nil {
		t.Fatal("head not nil")
	}
	tail := st.Tail()
	if tail != nil {
		t.Fatal("tail not nil")
	}
	for i, tag := range tags {
		if tag.key != "" {
			t.Fatalf("key not empty at %d", i)
		}
		if tag.value.value != "" {
			t.Fatalf("value not nil at %d", i)
		}
	}
}

func BenchmarkSpanTagsSet(b *testing.B) {
	var st spanTags
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.AppendMeta("key", "value")
		st.reset()
	}
}

func BenchmarkSpanTagsSetPreallocated(b *testing.B) {
	var st spanTags
	for i := 0; i < b.N; i++ {
		tagsPool.Put(unsafe.Pointer(&tag[meta]{}))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.AppendMeta("key", "value")
	}
}
