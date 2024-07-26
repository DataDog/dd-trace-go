// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"testing"
)

func TestSpanTagsSet(t *testing.T) {
	var st spanTags
	st.append("key1", "value1")
	st.append("key2", 0.1)
	st.append("key3", 1)
	st.append("key2", "value2")
	if st.head == nil {
		t.Fatal()
	}
	if st.head.key != "key1" {
		t.Fatal()
	}
	if st.head.value != "value1" {
		t.Fatal()
	}
	if st.tail == nil {
		t.Fatal()
	}
	if st.tail.key != "key2" {
		t.Fatal()
	}
	if st.tail.value != "value2" {
		t.Fatal()
	}
}

func TestSpanTagsReset(t *testing.T) {
	var (
		st    spanTags
		elems = 100
	)
	for i := 0; i < elems; i++ {
		st.append("key", "value")
	}
	tags := make([]*tag, elems)
	tt := st.head
	for i := range tags {
		tags[i] = tt
		tt = tt.sibling
	}
	st.reset()
	if st.head != nil {
		t.Fatal("head not nil")
	}
	if st.tail != nil {
		t.Fatal("tail not nil")
	}
	for i, tag := range tags {
		if tag.key != "" {
			t.Fatalf("key not empty at %d", i)
		}
		if tag.value != nil {
			t.Fatalf("value not nil at %d", i)
		}
	}
}

func BenchmarkSpanTagsSet(b *testing.B) {
	var st spanTags
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.append("key", "value")
		st.reset()
	}
}

func BenchmarkSpanTagsSetPreallocated(b *testing.B) {
	var st spanTags
	for i := 0; i < b.N; i++ {
		tagsPool.Put(&tag{})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.append("key", "value")
	}
}
