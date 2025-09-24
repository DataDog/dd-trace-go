// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stacktrace

import (
	"sync"
	"testing"
)

func TestPrefixTrie_Basic(t *testing.T) {
	trie := newPrefixTrie()

	// Test empty trie
	if trie.HasPrefix("test") {
		t.Error("Empty trie should not have any prefixes")
	}
	if trie.Size() != 0 {
		t.Errorf("Empty trie size should be 0, got %d", trie.Size())
	}

	// Test single insertion
	trie.Insert("github.com")
	if !trie.HasPrefix("github.com/user/repo") {
		t.Error("Should match prefix")
	}
	if !trie.HasPrefix("github.com") {
		t.Error("Should match exact string")
	}
	if trie.HasPrefix("github.co") {
		t.Error("Should not match partial prefix")
	}
	if trie.Size() != 1 {
		t.Errorf("Expected size 1, got %d", trie.Size())
	}
}

func TestPrefixTrie_MultiplePrefixes(t *testing.T) {
	trie := newPrefixTrie()
	prefixes := []string{
		"github.com/DataDog",
		"go.uber.org",
		"google.golang.org",
		"gopkg.in",
	}

	for _, prefix := range prefixes {
		trie.Insert(prefix)
	}

	testCases := []struct {
		input    string
		expected bool
	}{
		{"github.com/DataDog/dd-trace-go", true},
		{"github.com/DataDog", true},
		{"go.uber.org/zap", true},
		{"google.golang.org/grpc", true},
		{"gopkg.in/yaml.v2", true},
		{"github.com/other/repo", false},
		{"go.uber", false},
		{"google.golang", false},
		{"unknown.com/package", false},
		{"", false},
	}

	for _, tc := range testCases {
		if result := trie.HasPrefix(tc.input); result != tc.expected {
			t.Errorf("HasPrefix(%q) = %v, expected %v", tc.input, result, tc.expected)
		}
	}

	if trie.Size() != len(prefixes) {
		t.Errorf("Expected size %d, got %d", len(prefixes), trie.Size())
	}
}

func TestPrefixTrie_OverlappingPrefixes(t *testing.T) {
	trie := newPrefixTrie()

	// Insert overlapping prefixes
	trie.Insert("github.com")
	trie.Insert("github.com/DataDog")
	trie.Insert("github.com/DataDog/dd-trace-go")

	testCases := []struct {
		input    string
		expected bool
	}{
		{"github.com/user/repo", true},              // matches "github.com"
		{"github.com/DataDog/other", true},          // matches "github.com/DataDog"
		{"github.com/DataDog/dd-trace-go/v2", true}, // matches "github.com/DataDog/dd-trace-go"
		{"github.com", true},                        // exact match
		{"github.co", false},                        // partial match
	}

	for _, tc := range testCases {
		if result := trie.HasPrefix(tc.input); result != tc.expected {
			t.Errorf("HasPrefix(%q) = %v, expected %v", tc.input, result, tc.expected)
		}
	}

	if trie.Size() != 3 {
		t.Errorf("Expected size 3, got %d", trie.Size())
	}
}

func TestPrefixTrie_InsertAll(t *testing.T) {
	trie := newPrefixTrie()
	prefixes := []string{
		"github.com/DataDog",
		"go.uber.org",
		"google.golang.org",
		"gopkg.in",
		"", // empty string should be ignored
	}

	trie.InsertAll(prefixes)

	// Should have inserted 4 prefixes (empty string ignored)
	if trie.Size() != 4 {
		t.Errorf("Expected size 4, got %d", trie.Size())
	}

	// Test all prefixes work
	if !trie.HasPrefix("github.com/DataDog/dd-trace-go") {
		t.Error("Should match github.com/DataDog prefix")
	}
	if !trie.HasPrefix("go.uber.org/zap") {
		t.Error("Should match go.uber.org prefix")
	}
	if !trie.HasPrefix("google.golang.org/grpc") {
		t.Error("Should match google.golang.org prefix")
	}
	if !trie.HasPrefix("gopkg.in/yaml.v2") {
		t.Error("Should match gopkg.in prefix")
	}
}

func TestPrefixTrie_EmptyInputs(t *testing.T) {
	trie := newPrefixTrie()

	// Insert empty string should be ignored
	trie.Insert("")
	if trie.Size() != 0 {
		t.Error("Empty string insertion should be ignored")
	}

	// HasPrefix with empty string should return false
	if trie.HasPrefix("") {
		t.Error("Empty string should not match any prefix")
	}

	trie.Insert("test")
	if trie.HasPrefix("") {
		t.Error("Empty string should not match any prefix even when trie has data")
	}
}

func TestPrefixTrie_Clear(t *testing.T) {
	trie := newPrefixTrie()
	prefixes := []string{"github.com", "go.uber.org", "google.golang.org"}

	trie.InsertAll(prefixes)
	if trie.Size() != 3 {
		t.Error("Expected 3 prefixes after insertion")
	}

	trie.Clear()
	if trie.Size() != 0 {
		t.Error("Expected 0 prefixes after clear")
	}

	if trie.HasPrefix("github.com/user/repo") {
		t.Error("Should not match any prefix after clear")
	}
}

func TestPrefixTrie_UnicodeSupport(t *testing.T) {
	trie := newPrefixTrie()

	// Test Unicode characters in prefixes
	trie.Insert("测试.com")
	trie.Insert("例え.jp")

	if !trie.HasPrefix("测试.com/path") {
		t.Error("Should match Unicode prefix")
	}
	if !trie.HasPrefix("例え.jp/パッケージ") {
		t.Error("Should match Unicode prefix")
	}
	if trie.HasPrefix("测试.co") {
		t.Error("Should not match partial Unicode prefix")
	}
}

func TestPrefixTrie_Concurrency(t *testing.T) {
	trie := newPrefixTrie()
	prefixes := make([]string, 100)
	for i := range prefixes {
		prefixes[i] = "prefix" + string(rune('0'+i%10))
	}

	// Insert all prefixes
	trie.InsertAll(prefixes[:10]) // Insert first 10 unique prefixes

	// Run concurrent reads
	var wg sync.WaitGroup
	numReaders := 10
	readsPerReader := 1000

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			for j := 0; j < readsPerReader; j++ {
				testStr := "prefix" + string(rune('0'+j%10)) + "/test"
				if !trie.HasPrefix(testStr) {
					t.Errorf("Reader %d: Expected to find prefix for %s", readerID, testStr)
					return
				}
			}
		}(i)
	}

	wg.Wait()
}

func TestPrefixTrie_LargePrefixSet(t *testing.T) {
	trie := newPrefixTrie()

	// Create a large set of prefixes similar to the real-world case
	prefixes := []string{
		"cloud.google.com/go",
		"github.com/aws/aws-sdk-go",
		"github.com/gorilla/mux",
		"github.com/stretchr/testify",
		"go.uber.org/zap",
		"google.golang.org/grpc",
		"gopkg.in/yaml.v2",
		"k8s.io/api",
		"k8s.io/client-go",
	}

	for i := 0; i < 100; i++ {
		for _, prefix := range prefixes {
			trie.Insert(prefix + string(rune(i)))
		}
	}

	// Test that lookups still work correctly with many prefixes
	if !trie.HasPrefix("cloud.google.com/go" + string(rune(50)) + "/compute") {
		t.Error("Should find prefix in large trie")
	}
	if trie.HasPrefix("unknown.com/package") {
		t.Error("Should not find non-existent prefix in large trie")
	}

	expectedSize := len(prefixes) * 100
	if trie.Size() != expectedSize {
		t.Errorf("Expected size %d, got %d", expectedSize, trie.Size())
	}
}

func TestPrefixTrie_RealWorldLibraries(t *testing.T) {
	trie := newPrefixTrie()

	// Use some real third-party library prefixes from the generated list
	realPrefixes := []string{
		"cloud.google.com/go",
		"github.com/aws/aws-sdk-go",
		"github.com/gorilla/mux",
		"github.com/stretchr/testify",
		"go.uber.org/zap",
		"google.golang.org/grpc",
		"gopkg.in/yaml.v2",
		"k8s.io/api",
	}

	trie.InsertAll(realPrefixes)

	testCases := []struct {
		input    string
		expected bool
	}{
		{"cloud.google.com/go/storage", true},
		{"github.com/aws/aws-sdk-go/service/s3", true},
		{"github.com/gorilla/mux", true},
		{"github.com/stretchr/testify/assert", true},
		{"go.uber.org/zap/zapcore", true},
		{"google.golang.org/grpc/codes", true},
		{"gopkg.in/yaml.v2", true},
		{"k8s.io/api/core/v1", true},
		{"github.com/unknown/package", false},
		{"golang.org/x/crypto", false},
		{"fmt", false},
	}

	for _, tc := range testCases {
		if result := trie.HasPrefix(tc.input); result != tc.expected {
			t.Errorf("HasPrefix(%q) = %v, expected %v", tc.input, result, tc.expected)
		}
	}
}

// Test the segment-based trie implementation

func TestSegmentPrefixTrie_Basic(t *testing.T) {
	trie := newSegmentPrefixTrie()

	// Test empty trie
	if trie.HasPrefix("test") {
		t.Error("Empty trie should not have any prefixes")
	}
	if trie.Size() != 0 {
		t.Errorf("Empty trie size should be 0, got %d", trie.Size())
	}

	// Test single insertion
	trie.Insert("github.com")
	if !trie.HasPrefix("github.com/user/repo") {
		t.Error("Should match prefix")
	}
	if !trie.HasPrefix("github.com") {
		t.Error("Should match exact string")
	}
	if trie.HasPrefix("github.co") {
		t.Error("Should not match partial prefix")
	}
	if trie.Size() != 1 {
		t.Errorf("Expected size 1, got %d", trie.Size())
	}
}

func TestSegmentPrefixTrie_PathSegments(t *testing.T) {
	trie := newSegmentPrefixTrie()
	prefixes := []string{
		"github.com/DataDog",
		"github.com/gorilla",
		"cloud.google.com/go",
		"go.uber.org/zap",
	}

	for _, prefix := range prefixes {
		trie.Insert(prefix)
	}

	testCases := []struct {
		input    string
		expected bool
	}{
		{"github.com/DataDog/dd-trace-go", true},
		{"github.com/DataDog", true},
		{"github.com/gorilla/mux", true},
		{"cloud.google.com/go/storage", true},
		{"go.uber.org/zap/zapcore", true},
		{"github.com/other/repo", false},
		{"github.com", false}, // partial segment match
		{"cloud.google.com", false},
		{"unknown.com/package", false},
		{"", false},
	}

	for _, tc := range testCases {
		if result := trie.HasPrefix(tc.input); result != tc.expected {
			t.Errorf("HasPrefix(%q) = %v, expected %v", tc.input, result, tc.expected)
		}
	}

	if trie.Size() != len(prefixes) {
		t.Errorf("Expected size %d, got %d", len(prefixes), trie.Size())
	}
}

func TestSegmentPrefixTrie_RealWorldLibraries(t *testing.T) {
	trie := newSegmentPrefixTrie()

	realPrefixes := []string{
		"cloud.google.com/go",
		"github.com/aws/aws-sdk-go",
		"github.com/gorilla/mux",
		"github.com/stretchr/testify",
		"go.uber.org/zap",
		"google.golang.org/grpc",
		"gopkg.in/yaml.v2",
		"k8s.io/api",
	}

	trie.InsertAll(realPrefixes)

	testCases := []struct {
		input    string
		expected bool
	}{
		{"cloud.google.com/go/storage", true},
		{"github.com/aws/aws-sdk-go/service/s3", true},
		{"github.com/gorilla/mux", true},
		{"github.com/stretchr/testify/assert", true},
		{"go.uber.org/zap/zapcore", true},
		{"google.golang.org/grpc/codes", true},
		{"gopkg.in/yaml.v2", true},
		{"k8s.io/api/core/v1", true},
		{"github.com/unknown/package", false},
		{"golang.org/x/crypto", false},
		{"fmt", false},
	}

	for _, tc := range testCases {
		if result := trie.HasPrefix(tc.input); result != tc.expected {
			t.Errorf("HasPrefix(%q) = %v, expected %v", tc.input, result, tc.expected)
		}
	}
}

// Compare segment trie with character trie for consistency
func TestSegmentVsCharacterTrieConsistency(t *testing.T) {
	charTrie := newPrefixTrie()
	segmentTrie := newSegmentPrefixTrie()

	realPrefixes := []string{
		"cloud.google.com/go",
		"github.com/aws/aws-sdk-go",
		"github.com/gorilla/mux",
		"github.com/stretchr/testify",
		"go.uber.org/zap",
		"google.golang.org/grpc",
		"gopkg.in/yaml.v2",
		"k8s.io/api",
	}

	charTrie.InsertAll(realPrefixes)
	segmentTrie.InsertAll(realPrefixes)

	testStrings := []string{
		"cloud.google.com/go/storage/internal",
		"github.com/aws/aws-sdk-go/service/s3",
		"github.com/gorilla/mux/middleware",
		"github.com/stretchr/testify/assert",
		"go.uber.org/zap/zapcore",
		"google.golang.org/grpc/codes",
		"gopkg.in/yaml.v2/internal",
		"k8s.io/api/core/v1",
		"github.com/unknown/package",
		"golang.org/x/crypto",
		"fmt",
		"net/http",
		"example.com/myapp/handler",
	}

	for _, testStr := range testStrings {
		charResult := charTrie.HasPrefix(testStr)
		segmentResult := segmentTrie.HasPrefix(testStr)

		if charResult != segmentResult {
			t.Errorf("Character vs Segment trie mismatch for %q: char=%v, segment=%v",
				testStr, charResult, segmentResult)
		}
	}

	if charTrie.Size() != segmentTrie.Size() {
		t.Errorf("Size mismatch: char=%d, segment=%d", charTrie.Size(), segmentTrie.Size())
	}
}
