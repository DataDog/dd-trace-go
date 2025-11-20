// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stacktrace

import (
	"testing"
)

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
