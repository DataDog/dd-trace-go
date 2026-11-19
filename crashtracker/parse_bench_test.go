// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadFixture reads a testdata fixture file and panics if it cannot be read,
// so benchmark failures are obvious rather than silent.
func loadFixture(tb testing.TB, name string) []byte {
	tb.Helper() // surfaces failures at the call site, not inside this helper
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		tb.Fatalf("loadFixture %q: %v", name, err)
	}
	return data
}

// benchmarkParseCrashDump times parseCrashDump alone. Building the dump is
// the caller's job and happens before the loop, so it is not timed.
func benchmarkParseCrashDump(b *testing.B, dump []byte) {
	b.Helper()
	b.SetBytes(int64(len(dump)))
	b.ReportAllocs()
	for b.Loop() {
		_ = parseCrashDump(dump)
	}
}

func BenchmarkParseCrashDump_Panic(b *testing.B) {
	benchmarkParseCrashDump(b, loadFixture(b, "panic_simple.txt"))
}

func BenchmarkParseCrashDump_ConcurrentMapWrite(b *testing.B) {
	benchmarkParseCrashDump(b, loadFixture(b, "concurrent_map_write.txt"))
}

func BenchmarkParseCrashDump_SIGSEGV(b *testing.B) {
	benchmarkParseCrashDump(b, loadFixture(b, "sigsegv.txt"))
}

// BenchmarkParseCrashDump_ManyGoroutines parses a near-maximum-size dump of
// many shallow goroutines, the shape that stresses the thread cap.
func BenchmarkParseCrashDump_ManyGoroutines(b *testing.B) {
	dump, _ := manyGoroutinesDump(manyGoroutinesDumpSize)
	benchmarkParseCrashDump(b, dump)
}

// BenchmarkParseCrashDump_DeepStack parses one goroutine with 5000 frames,
// the shape of a stack-overflow crash, which stresses the per-thread cap.
func BenchmarkParseCrashDump_DeepStack(b *testing.B) {
	const frames = 5000
	var dump strings.Builder
	dump.WriteString("fatal error: stack overflow\n\ngoroutine 1 [running]:\n")
	for i := range frames {
		fmt.Fprintf(&dump, "main.recurse(0x%x)\n\t/tmp/crashdemo/main.go:10 +0x34\n", i)
	}
	benchmarkParseCrashDump(b, []byte(dump.String()))
}
