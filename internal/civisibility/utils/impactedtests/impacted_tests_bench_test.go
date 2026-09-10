// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package impactedtests

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/filebitmap"
)

// newBenchmarkImpactedTestAnalyzer creates a representative analyzer with
// several modified files so benchmarks exercise the same suffix scan used in
// production.
func newBenchmarkImpactedTestAnalyzer() *ImpactedTestAnalyzer {
	return &ImpactedTestAnalyzer{
		modifiedFiles: []fileWithBitmap{
			{
				file:   "pkg/first/first_test.go",
				bitmap: filebitmap.FromActiveRange(20, 30).GetBuffer(),
			},
			{
				file:   "pkg/second/second_test.go",
				bitmap: filebitmap.FromActiveRange(100, 120).GetBuffer(),
			},
			{
				file:   "pkg/third/third_test.go",
				bitmap: filebitmap.FromActiveRange(200, 240).GetBuffer(),
			},
			{
				file: "pkg/no_line_info/no_line_info_test.go",
			},
		},
		currentCommitSha: "benchmark-current-sha",
		baseCommitSha:    "benchmark-base-sha",
	}
}

// BenchmarkIsImpactedRepeatedSameRange measures repeated calls for the same
// source file and line range, which is the case optimized by decision caching.
func BenchmarkIsImpactedRepeatedSameRange(b *testing.B) {
	analyzer := newBenchmarkImpactedTestAnalyzer()
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = analyzer.IsImpacted("BenchmarkTest", "/workspace/pkg/second/second_test.go", 110, 115)
	}
}

// BenchmarkIsImpactedRepeatedDistinctRanges measures calls that hit the same
// modified file while cycling through a small working set of source ranges.
func BenchmarkIsImpactedRepeatedDistinctRanges(b *testing.B) {
	analyzer := newBenchmarkImpactedTestAnalyzer()
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		startLine := 90 + (i % 40)
		_ = analyzer.IsImpacted("BenchmarkTest", "/workspace/pkg/second/second_test.go", startLine, startLine+5)
	}
}

// BenchmarkIsImpactedColdDistinctRanges measures calls that use a different
// range on every iteration so the decision cache cannot reuse prior results.
func BenchmarkIsImpactedColdDistinctRanges(b *testing.B) {
	analyzer := newBenchmarkImpactedTestAnalyzer()
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		startLine := 10_000 + (i * 10)
		_ = analyzer.IsImpacted("BenchmarkTest", "/workspace/pkg/second/second_test.go", startLine, startLine+5)
	}
}

// BenchmarkIsImpactedRepeatedMiss measures repeated calls for a source file
// that does not match any modified file.
func BenchmarkIsImpactedRepeatedMiss(b *testing.B) {
	analyzer := newBenchmarkImpactedTestAnalyzer()
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = analyzer.IsImpacted("BenchmarkTest", "/workspace/pkg/missing/missing_test.go", 10, 20)
	}
}

// BenchmarkIsImpactedNoLineInfo measures the file-level impacted path used when
// either the test or modified file has no usable line information.
func BenchmarkIsImpactedNoLineInfo(b *testing.B) {
	analyzer := newBenchmarkImpactedTestAnalyzer()
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = analyzer.IsImpacted("BenchmarkTest", "/workspace/pkg/no_line_info/no_line_info_test.go", 0, 0)
	}
}

// BenchmarkBitmapIntersectsLineRange measures the allocation-free bitmap range
// check used by the optimized IsImpacted implementation.
func BenchmarkBitmapIntersectsLineRange(b *testing.B) {
	bitmap := filebitmap.FromActiveRange(100, 120).GetBuffer()
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = bitmapIntersectsLineRange(bitmap, 110, 115)
	}
}
