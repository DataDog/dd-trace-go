// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"os"
	"path/filepath"
	"testing"
)

// loadFixture reads a testdata fixture file and panics if it cannot be read,
// so benchmark failures are obvious rather than silent.
func loadFixture(tb testing.TB, name string) []byte {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		tb.Fatalf("loadFixture %q: %v", name, err)
	}
	return data
}

func BenchmarkParseCrashDump_Panic(b *testing.B) {
	dump := loadFixture(b, "panic_simple.txt")
	b.SetBytes(int64(len(dump)))
	b.ResetTimer()
	for b.Loop() {
		_ = parseCrashDump(dump)
	}
}

func BenchmarkParseCrashDump_ConcurrentMapWrite(b *testing.B) {
	dump := loadFixture(b, "concurrent_map_write.txt")
	b.SetBytes(int64(len(dump)))
	b.ResetTimer()
	for b.Loop() {
		_ = parseCrashDump(dump)
	}
}

func BenchmarkParseCrashDump_SIGSEGV(b *testing.B) {
	dump := loadFixture(b, "sigsegv.txt")
	b.SetBytes(int64(len(dump)))
	b.ResetTimer()
	for b.Loop() {
		_ = parseCrashDump(dump)
	}
}
