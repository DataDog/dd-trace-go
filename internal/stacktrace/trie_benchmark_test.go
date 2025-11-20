// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stacktrace

import (
	"runtime"
	"strings"
	"testing"
)

var (
	// Use the actual generated third-party libraries as our test corpus
	benchmarkPrefixes = generatedThirdPartyLibraries()

	// Test strings that would be checked against prefixes in real usage
	benchmarkTestStrings = []string{
		// Real-world examples that should match
		"cloud.google.com/go/storage/internal",
		"github.com/aws/aws-sdk-go/service/s3",
		"github.com/gorilla/mux/middleware",
		"github.com/stretchr/testify/assert",
		"go.uber.org/zap/zapcore",
		"google.golang.org/grpc/codes",
		"gopkg.in/yaml.v2/internal",
		"k8s.io/api/core/v1",
		"github.com/prometheus/client_golang/prometheus",
		"github.com/sirupsen/logrus/hooks",

		// Examples that should NOT match (customer/internal code)
		"github.com/mycompany/internal/service",
		"example.com/myapp/handler",
		"mydomain.com/service/auth",
		"company.internal/package",

		// Standard library (should not match)
		"fmt",
		"net/http",
		"context",
		"encoding/json",
		"os",
		"io",
		"strings",
		"time",

		// Runtime/testing (should not match)
		"main.main",
		"runtime.main",
		"testing.tRunner",
		"runtime.goexit",
	}
)

// linearPrefixMatcher implements the original linear search approach
type linearPrefixMatcher struct {
	prefixes []string
}

func newLinearPrefixMatcher(prefixes []string) *linearPrefixMatcher {
	return &linearPrefixMatcher{prefixes: prefixes}
}

func (l *linearPrefixMatcher) HasPrefix(s string) bool {
	for _, prefix := range l.prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// Benchmark linear search vs trie for bulk operations

func BenchmarkLinearSearch_BulkLookups(b *testing.B) {
	matcher := newLinearPrefixMatcher(benchmarkPrefixes)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, testStr := range benchmarkTestStrings {
			_ = matcher.HasPrefix(testStr)
		}
	}
}

func BenchmarkSegmentTrie_BulkLookups(b *testing.B) {
	trie := newSegmentPrefixTrie()
	trie.InsertAll(benchmarkPrefixes)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, testStr := range benchmarkTestStrings {
			_ = trie.HasPrefix(testStr)
		}
	}
}

// Single lookup benchmarks - best case (early match)

func BenchmarkLinearSearch_SingleLookup_EarlyMatch(b *testing.B) {
	matcher := newLinearPrefixMatcher(benchmarkPrefixes)
	// First prefix in the generated list should match early
	testStr := "cloud.google.com/go/storage/internal"
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = matcher.HasPrefix(testStr)
	}
}

func BenchmarkSegmentTrie_SingleLookup_EarlyMatch(b *testing.B) {
	trie := newSegmentPrefixTrie()
	trie.InsertAll(benchmarkPrefixes)
	testStr := "cloud.google.com/go/storage/internal"
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = trie.HasPrefix(testStr)
	}
}

// Single lookup benchmarks - worst case (no match)

func BenchmarkLinearSearch_SingleLookup_NoMatch(b *testing.B) {
	matcher := newLinearPrefixMatcher(benchmarkPrefixes)
	// String that won't match any prefix (worst case for linear search)
	testStr := "example.com/mycompany/internal/service"
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = matcher.HasPrefix(testStr)
	}
}

func BenchmarkSegmentTrie_SingleLookup_NoMatch(b *testing.B) {
	trie := newSegmentPrefixTrie()
	trie.InsertAll(benchmarkPrefixes)
	testStr := "example.com/mycompany/internal/service"
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = trie.HasPrefix(testStr)
	}
}

// Construction/initialization benchmarks

func BenchmarkLinearSearch_Construction(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = newLinearPrefixMatcher(benchmarkPrefixes)
	}
}

func BenchmarkSegmentTrie_Construction(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		trie := newSegmentPrefixTrie()
		trie.InsertAll(benchmarkPrefixes)
	}
}

// Memory allocation benchmarks for lookup operations (should be zero for both)

func BenchmarkLinearSearch_LookupAllocations(b *testing.B) {
	matcher := newLinearPrefixMatcher(benchmarkPrefixes)
	testStr := "github.com/test/package/internal"
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = matcher.HasPrefix(testStr)
	}
}

func BenchmarkSegmentTrie_LookupAllocations(b *testing.B) {
	trie := newSegmentPrefixTrie()
	trie.InsertAll(benchmarkPrefixes)
	testStr := "github.com/test/package/internal"
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = trie.HasPrefix(testStr)
	}
}

// Data structure memory overhead comparison
// This measures the memory cost of the data structure itself, not lookup allocations

func BenchmarkDataStructureMemoryOverhead(b *testing.B) {
	b.Run("LinearSearch_DataStructure", func(b *testing.B) {
		var m1, m2 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m1)

		matcher := newLinearPrefixMatcher(benchmarkPrefixes)
		_ = matcher // prevent optimization

		runtime.GC()
		runtime.ReadMemStats(&m2)
		b.ReportMetric(float64(m2.Alloc-m1.Alloc), "bytes_overhead")
		b.Logf("Linear search data structure overhead: %d bytes for %d prefixes",
			m2.Alloc-m1.Alloc, len(benchmarkPrefixes))
	})

	b.Run("SegmentTrie_DataStructure", func(b *testing.B) {
		var m1, m2 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m1)

		trie := newSegmentPrefixTrie()
		trie.InsertAll(benchmarkPrefixes)

		runtime.GC()
		runtime.ReadMemStats(&m2)
		b.ReportMetric(float64(m2.Alloc-m1.Alloc), "bytes_overhead")
		b.Logf("Segment trie data structure overhead: %d bytes for %d prefixes",
			m2.Alloc-m1.Alloc, len(benchmarkPrefixes))
	})
}

// Test correctness - ensure all implementations return the same results
func TestImplementationConsistency(t *testing.T) {
	linear := newLinearPrefixMatcher(benchmarkPrefixes)
	segmentTrie := newSegmentPrefixTrie()
	segmentTrie.InsertAll(benchmarkPrefixes)

	for _, testStr := range benchmarkTestStrings {
		linearResult := linear.HasPrefix(testStr)
		segmentTrieResult := segmentTrie.HasPrefix(testStr)

		if linearResult != segmentTrieResult {
			t.Errorf("Linear vs Segment Trie mismatch for %q: linear=%v, segmentTrie=%v",
				testStr, linearResult, segmentTrieResult)
		}
	}

	t.Logf("Tested %d prefixes against %d test strings - all implementations consistent",
		len(benchmarkPrefixes), len(benchmarkTestStrings))
}

// Benchmark the actual current implementation (using the optimized trie)
func BenchmarkCurrentImplementation_isKnownThirdPartyLibrary(b *testing.B) {
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, testStr := range benchmarkTestStrings {
			_ = isKnownThirdPartyLibrary(testStr)
		}
	}
}

func BenchmarkCurrentImplementation_SingleLookup(b *testing.B) {
	testStr := "cloud.google.com/go/storage/internal"
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = isKnownThirdPartyLibrary(testStr)
	}
}

func BenchmarkCurrentImplementation_SingleLookup_NoMatch(b *testing.B) {
	testStr := "example.com/mycompany/internal/service"
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = isKnownThirdPartyLibrary(testStr)
	}
}
