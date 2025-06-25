// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package impactedtests

import (
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/filebitmap"
	"github.com/stretchr/testify/assert"
)

// dummyTestSpan is a dummy implementation of the TestSpan interface for testing purposes.
type dummyTestSpan struct {
	tags map[string]interface{}
}

// AsMap returns the tags map.
func (d *dummyTestSpan) AsMap() map[string]interface{} {
	return d.tags
}

// SetTag sets a tag value.
func (d *dummyTestSpan) SetTag(key string, value any) {
	d.tags[key] = value
}

// newDummyTestSpan creates a new dummyTestSpan.
func newDummyTestSpan() *dummyTestSpan {
	return &dummyTestSpan{tags: make(map[string]interface{})}
}

// TestParseGitDiffOutput tests the parseGitDiffOutput function.
func TestParseGitDiffOutput(t *testing.T) {
	// Sample git diff output with two file diffs.
	diffOutput := `diff --git a/file1.txt b/file1.txt
@@ -1,2 +3,4 @@
context line 1
context line 2
diff --git a/file2.txt b/file2.txt
@@ -10,1 +20,2 @@
another context line`

	files := parseGitDiffOutput(diffOutput)
	if len(files) != 2 {
		t.Fatalf("Expected 2 files, got %d", len(files))
	}

	// Check that file names are correctly extracted.
	if !strings.HasSuffix(files[0].file, "file1.txt") {
		t.Errorf("Expected first file to be file1.txt, got %s", files[0].file)
	}
	if !strings.HasSuffix(files[1].file, "file2.txt") {
		t.Errorf("Expected second file to be file2.txt, got %s", files[1].file)
	}

	// Check that bitmap is not nil for files with changes.
	if files[0].bitmap == nil {
		t.Error("Expected bitmap for file1.txt to be non-nil")
	}
	if files[1].bitmap == nil {
		t.Error("Expected bitmap for file2.txt to be non-nil")
	}
	files0Bitmap := filebitmap.NewFileBitmapFromBytes(files[0].bitmap).String()
	if files0Bitmap != "00111100" {
		t.Errorf("Expected bitmap for file1.txt to be 00111100, got %s", files0Bitmap)
	}

	files1Bitmap := filebitmap.NewFileBitmapFromBytes(files[1].bitmap).String()
	if files1Bitmap != "000000000000000000011000" {
		t.Errorf("Expected bitmap for file1.txt to be 000000000000000000011000, got %s", files1Bitmap)
	}
}

// TestSplitLines tests the splitLines function.
func TestSplitLines(t *testing.T) {
	input := "line1\n\nline2\n  \nline3\n"
	lines := splitLines(input)
	expected := []string{"line1", "line2", "line3"}
	if len(lines) != len(expected) {
		t.Fatalf("Expected %d lines, got %d", len(expected), len(lines))
	}
	for i, line := range lines {
		if line != expected[i] {
			t.Errorf("Expected line %d to be %q, got %q", i, expected[i], line)
		}
	}
}

// TestGetTestImpactInfo tests the getTestImpactInfo method of tagsMap.
func TestGetTestImpactInfo(t *testing.T) {
	// Create a tagsMap with the necessary tags.
	testFile := "testfile.go"
	startLine := 5
	endLine := 10

	files := getTestImpactInfo(testFile, startLine, endLine)
	if len(files) != 1 {
		t.Fatalf("Expected 1 file from getTestImpactInfo, got %d", len(files))
	}
	if files[0].file != testFile {
		t.Errorf("Expected file %q, got %q", testFile, files[0].file)
	}
	if files[0].bitmap == nil {
		t.Errorf("Expected non-nil bitmap for file %q", testFile)
	}
}

// TestProcessImpactedTest tests the ProcessImpactedTest method of ImpactedTestAnalyzer.
func TestProcessImpactedTest(t *testing.T) {
	// Create a dummy TestSpan with the necessary test source tags.
	span := newDummyTestSpan()
	testFile := "testfile.go"
	startLine := 5
	endLine := 10
	span.tags[constants.TestSourceFile] = testFile
	span.tags[constants.TestSourceStartLine] = startLine
	span.tags[constants.TestSourceEndLine] = endLine

	// Create a bitmap for the test file simulating a modified file.
	testBitmap := filebitmap.FromActiveRange(startLine, endLine)
	testBitmapBuffer := testBitmap.GetBuffer()

	// Create an ImpactedTestAnalyzer with modifiedFiles including the test file.
	modifiedFile := &fileWithBitmap{
		file:   testFile,
		bitmap: testBitmapBuffer,
	}
	analyzer := &ImpactedTestAnalyzer{
		modifiedFiles:    []fileWithBitmap{*modifiedFile},
		currentCommitSha: "dummyCurrentSha",
		baseCommitSha:    "dummyBaseSha",
	}

	// Process the impacted test.
	isImpacted := analyzer.IsImpacted("test", testFile, startLine, endLine)

	// Verify that the test is marked as impacted.
	if !isImpacted {
		t.Error("Expected to be impacted")
	}
}

// TestNewImpactedTestAnalyzerWithNoBaseBranch tests that the analyzer handles the case where no base branch can be found gracefully.
func TestNewImpactedTestAnalyzerWithNoBaseBranch(t *testing.T) {
	// This test verifies that the analyzer can be created even when base branch detection fails
	// According to the updated algorithm, this should not cause the analyzer creation to fail

	analyzer, err := NewImpactedTestAnalyzer()

	// The analyzer should be created successfully even if no base branch is found
	if err != nil {
		// If there's an error, it should only be due to missing current commit, not missing base
		if !strings.Contains(err.Error(), "current commit is empty") {
			t.Errorf("Unexpected error creating analyzer: %v", err)
		}
		return // Skip the rest if we can't get current commit
	}

	// Analyzer should be valid
	assert.NotNil(t, analyzer, "Analyzer should not be nil")
	assert.NotNil(t, analyzer.modifiedFiles, "ModifiedFiles should not be nil (can be empty)")

	// The modified files can be empty if no base branch was found
	t.Logf("Analyzer created with %d modified files", len(analyzer.modifiedFiles))

	// Test that IsImpacted works correctly with empty modified files
	isImpacted := analyzer.IsImpacted("test", "testfile.go", 1, 10)
	assert.False(t, isImpacted, "Should not be impacted when no modified files are present")
}
