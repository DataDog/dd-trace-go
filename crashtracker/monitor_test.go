// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bytes"
	"testing"
)

// TestReadCrashDumpExactlyAtLimitIsNotTruncated proves an input whose true
// size exactly equals maxCrashDumpSize -- a genuine EOF within budget, not a
// cut-off -- is not misreported as truncated.
func TestReadCrashDumpExactlyAtLimitIsNotTruncated(t *testing.T) {
	data := bytes.Repeat([]byte{'a'}, maxCrashDumpSize)

	got, truncated, err := readCrashDump(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("readCrashDump returned unexpected error: %v", err)
	}
	if truncated {
		t.Error("truncated = true, want false for input exactly at the cap")
	}
	if len(got) != maxCrashDumpSize {
		t.Errorf("len(data) = %d, want %d", len(got), maxCrashDumpSize)
	}
}

// TestReadCrashDumpDetectsTruncationAfterValidFrame proves truncation is
// detected even when the cut lands right after what looks like a complete,
// valid frame (a trailing newline at the boundary) -- the case
// parseCrashDump's own scanner-error path cannot catch, since nothing about
// the visible bytes is syntactically wrong.
func TestReadCrashDumpDetectsTruncationAfterValidFrame(t *testing.T) {
	data := bytes.Repeat([]byte{'a'}, maxCrashDumpSize+1)
	data[maxCrashDumpSize-1] = '\n' // the last byte that survives the cut

	got, truncated, err := readCrashDump(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("readCrashDump returned unexpected error: %v", err)
	}
	if !truncated {
		t.Error("truncated = false, want true for input one byte over the cap")
	}
	if len(got) != maxCrashDumpSize {
		t.Errorf("len(data) = %d, want %d", len(got), maxCrashDumpSize)
	}
}

// TestReadCrashDumpDetectsTruncationMidFrame proves the same detection holds
// when the cut instead lands in the middle of a line, with no newline
// anywhere near the boundary. readCrashDump's check is a pure byte count, so
// this and the after-a-valid-frame case above must behave identically; both
// are asserted so that isn't merely assumed.
func TestReadCrashDumpDetectsTruncationMidFrame(t *testing.T) {
	data := bytes.Repeat([]byte{'a'}, maxCrashDumpSize+1)

	got, truncated, err := readCrashDump(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("readCrashDump returned unexpected error: %v", err)
	}
	if !truncated {
		t.Error("truncated = false, want true for input one byte over the cap")
	}
	if len(got) != maxCrashDumpSize {
		t.Errorf("len(data) = %d, want %d", len(got), maxCrashDumpSize)
	}
}
