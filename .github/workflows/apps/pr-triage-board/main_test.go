// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import "testing"

func TestDedupeStable(t *testing.T) {
	// Non-adjacent duplicates must go too, unlike slices.Compact.
	got := dedupeStable([]string{"o/r", "a/b", "o/r", "a/b", "c/d", "o/r"})
	want := []string{"o/r", "a/b", "c/d"}
	if len(got) != len(want) {
		t.Fatalf("dedupeStable() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dedupeStable() = %v, want %v", got, want)
		}
	}
}
