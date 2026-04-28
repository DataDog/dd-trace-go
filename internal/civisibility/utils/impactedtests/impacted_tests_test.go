// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package impactedtests

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/filebitmap"
	ddlog "github.com/DataDog/dd-trace-go/v2/internal/log"
)

// dummyTestSpan is a dummy implementation of the TestSpan interface for testing purposes.
type dummyTestSpan struct {
	tags map[string]any
}

// AsMap returns the tags map.
func (d *dummyTestSpan) AsMap() map[string]any {
	return d.tags
}

// SetTag sets a tag value.
func (d *dummyTestSpan) SetTag(key string, value any) {
	d.tags[key] = value
}

// newDummyTestSpan creates a new dummyTestSpan.
func newDummyTestSpan() *dummyTestSpan {
	return &dummyTestSpan{tags: make(map[string]any)}
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

func TestIsImpactedDecisionCases(t *testing.T) {
	tests := []struct {
		name          string
		modifiedFiles []fileWithBitmap
		sourceFile    string
		startLine     int
		endLine       int
		want          bool
	}{
		{
			name: "matching file plus intersecting range returns true",
			modifiedFiles: []fileWithBitmap{
				{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(10, 20).GetBuffer()},
			},
			sourceFile: "/workspace/pkg/source_test.go",
			startLine:  15,
			endLine:    18,
			want:       true,
		},
		{
			name: "matching file plus non-intersecting range returns false",
			modifiedFiles: []fileWithBitmap{
				{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(10, 20).GetBuffer()},
			},
			sourceFile: "/workspace/pkg/source_test.go",
			startLine:  30,
			endLine:    40,
			want:       false,
		},
		{
			name: "no matching file returns false",
			modifiedFiles: []fileWithBitmap{
				{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(10, 20).GetBuffer()},
			},
			sourceFile: "/workspace/pkg/other_test.go",
			startLine:  15,
			endLine:    18,
			want:       false,
		},
		{
			name:          "empty modified files returns false",
			modifiedFiles: []fileWithBitmap{},
			sourceFile:    "/workspace/pkg/source_test.go",
			startLine:     15,
			endLine:       18,
			want:          false,
		},
		{
			name: "empty source file returns false",
			modifiedFiles: []fileWithBitmap{
				{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(10, 20).GetBuffer()},
			},
			sourceFile: "",
			startLine:  15,
			endLine:    18,
			want:       false,
		},
		{
			name: "zero start line returns true when file matches",
			modifiedFiles: []fileWithBitmap{
				{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(10, 20).GetBuffer()},
			},
			sourceFile: "/workspace/pkg/source_test.go",
			startLine:  0,
			endLine:    18,
			want:       true,
		},
		{
			name: "zero end line returns true when file matches",
			modifiedFiles: []fileWithBitmap{
				{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(10, 20).GetBuffer()},
			},
			sourceFile: "/workspace/pkg/source_test.go",
			startLine:  15,
			endLine:    0,
			want:       true,
		},
		{
			name: "nil modified bitmap returns true when file matches",
			modifiedFiles: []fileWithBitmap{
				{file: "pkg/source_test.go"},
			},
			sourceFile: "/workspace/pkg/source_test.go",
			startLine:  15,
			endLine:    18,
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analyzer := &ImpactedTestAnalyzer{modifiedFiles: tt.modifiedFiles}
			got := analyzer.IsImpacted("test", tt.sourceFile, tt.startLine, tt.endLine)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsImpactedInvalidNonZeroRangePanicsBeforeFileMatch(t *testing.T) {
	analyzer := &ImpactedTestAnalyzer{
		modifiedFiles: []fileWithBitmap{
			{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(10, 20).GetBuffer()},
		},
	}

	assert.PanicsWithValue(t, "Invalid range", func() {
		_ = analyzer.IsImpacted("test", "/workspace/pkg/does_not_match_test.go", 20, 10)
	})
}

func TestIsImpactedFirstMatchWins(t *testing.T) {
	t.Run("first suffix match intersects and second does not", func(t *testing.T) {
		analyzer := &ImpactedTestAnalyzer{
			modifiedFiles: []fileWithBitmap{
				{file: "foo_test.go", bitmap: filebitmap.FromActiveRange(10, 11).GetBuffer()},
				{file: "pkg/foo_test.go", bitmap: filebitmap.FromActiveRange(1, 2).GetBuffer()},
			},
		}

		assert.True(t, analyzer.IsImpacted("test", "/workspace/pkg/foo_test.go", 10, 11))
	})

	t.Run("first suffix match does not intersect and second would intersect", func(t *testing.T) {
		analyzer := &ImpactedTestAnalyzer{
			modifiedFiles: []fileWithBitmap{
				{file: "foo_test.go", bitmap: filebitmap.FromActiveRange(1, 2).GetBuffer()},
				{file: "pkg/foo_test.go", bitmap: filebitmap.FromActiveRange(10, 11).GetBuffer()},
			},
		}

		assert.False(t, analyzer.IsImpacted("test", "/workspace/pkg/foo_test.go", 10, 11))
	})
}

func TestIsImpactedDecisionCacheKeys(t *testing.T) {
	analyzer := &ImpactedTestAnalyzer{
		modifiedFiles: []fileWithBitmap{
			{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(10, 20).GetBuffer()},
			{file: "pkg/other_test.go", bitmap: filebitmap.FromActiveRange(30, 40).GetBuffer()},
		},
	}

	assert.True(t, analyzer.IsImpacted("test", "/workspace/pkg/source_test.go", 12, 14))
	assert.True(t, analyzer.IsImpacted("renamed test", "/workspace/pkg/source_test.go", 12, 14))
	assert.True(t, analyzer.IsImpacted("test", "/workspace/pkg/other_test.go", 32, 34))
	assert.False(t, analyzer.IsImpacted("test", "/workspace/pkg/source_test.go", 1, 2))
	assert.True(t, analyzer.IsImpacted("test", "/workspace/pkg/source_test.go", 20, 20))

	_, sameKeyCached := analyzer.decisionCache.Load(impactCacheKey{sourceFile: "/workspace/pkg/source_test.go", startLine: 12, endLine: 14})
	_, differentFileCached := analyzer.decisionCache.Load(impactCacheKey{sourceFile: "/workspace/pkg/other_test.go", startLine: 32, endLine: 34})
	_, differentStartCached := analyzer.decisionCache.Load(impactCacheKey{sourceFile: "/workspace/pkg/source_test.go", startLine: 1, endLine: 2})
	_, differentEndCached := analyzer.decisionCache.Load(impactCacheKey{sourceFile: "/workspace/pkg/source_test.go", startLine: 20, endLine: 20})

	assert.True(t, sameKeyCached)
	assert.True(t, differentFileCached)
	assert.True(t, differentStartCached)
	assert.True(t, differentEndCached)
}

func TestIsImpactedDecisionCacheStability(t *testing.T) {
	t.Run("positive cached result survives modified files mutation", func(t *testing.T) {
		analyzer := &ImpactedTestAnalyzer{
			modifiedFiles: []fileWithBitmap{
				{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(10, 20).GetBuffer()},
			},
		}

		assert.True(t, analyzer.IsImpacted("test", "/workspace/pkg/source_test.go", 12, 14))
		analyzer.modifiedFiles = []fileWithBitmap{
			{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(1, 2).GetBuffer()},
		}
		assert.True(t, analyzer.IsImpacted("test", "/workspace/pkg/source_test.go", 12, 14))
	})

	t.Run("negative cached result survives modified files mutation", func(t *testing.T) {
		analyzer := &ImpactedTestAnalyzer{
			modifiedFiles: []fileWithBitmap{
				{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(1, 2).GetBuffer()},
			},
		}

		assert.False(t, analyzer.IsImpacted("test", "/workspace/pkg/source_test.go", 12, 14))
		analyzer.modifiedFiles = []fileWithBitmap{
			{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(10, 20).GetBuffer()},
		}
		assert.False(t, analyzer.IsImpacted("test", "/workspace/pkg/source_test.go", 12, 14))
	})
}

func TestIsImpactedPreparedFilesFreezeAfterFirstUse(t *testing.T) {
	analyzer := &ImpactedTestAnalyzer{
		modifiedFiles: []fileWithBitmap{
			{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(1, 2).GetBuffer()},
		},
	}

	assert.False(t, analyzer.IsImpacted("test", "/workspace/pkg/source_test.go", 12, 14))
	analyzer.modifiedFiles = []fileWithBitmap{
		{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(10, 20).GetBuffer()},
	}
	assert.False(t, analyzer.IsImpacted("test", "/workspace/pkg/source_test.go", 15, 16))

	freshAnalyzer := &ImpactedTestAnalyzer{
		modifiedFiles: []fileWithBitmap{
			{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(10, 20).GetBuffer()},
		},
	}
	assert.True(t, freshAnalyzer.IsImpacted("test", "/workspace/pkg/source_test.go", 15, 16))
}

func TestIsImpactedEmptySourceDoesNotPrepareModifiedFiles(t *testing.T) {
	analyzer := &ImpactedTestAnalyzer{
		modifiedFiles: []fileWithBitmap{
			{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(1, 2).GetBuffer()},
		},
	}

	assert.False(t, analyzer.IsImpacted("test", "", 12, 14))
	analyzer.modifiedFiles = []fileWithBitmap{
		{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(10, 20).GetBuffer()},
	}

	assert.True(t, analyzer.IsImpacted("test", "/workspace/pkg/source_test.go", 15, 16))
}

func TestIsImpactedDebugLogsReplayOnCacheHit(t *testing.T) {
	oldLevel := ddlog.GetLevel()
	ddlog.SetLevel(ddlog.LevelDebug)
	defer ddlog.SetLevel(oldLevel)

	recordLogger := new(ddlog.RecordLogger)
	defer ddlog.UseLogger(recordLogger)()

	analyzer := &ImpactedTestAnalyzer{
		modifiedFiles: []fileWithBitmap{
			{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(10, 20).GetBuffer()},
			{file: "pkg/no_line_info_test.go"},
		},
	}

	assert.True(t, analyzer.IsImpacted("first test", "/workspace/pkg/source_test.go", 12, 14))
	assert.True(t, analyzer.IsImpacted("second test", "/workspace/pkg/source_test.go", 12, 14))
	assert.True(t, analyzer.IsImpacted("no line first", "/workspace/pkg/no_line_info_test.go", 0, 0))
	assert.True(t, analyzer.IsImpacted("no line second", "/workspace/pkg/no_line_info_test.go", 0, 0))

	logs := recordLogger.Logs()
	assert.Equal(t, 4, countLogsContaining(logs, "DiffFile found"))
	assert.Equal(t, 2, countLogsContaining(logs, "Intersecting lines"))
	assert.Equal(t, 2, countLogsContaining(logs, "No line info found"))
	assert.True(t, hasLogContaining(logs, "Marking test first test as modified"))
	assert.True(t, hasLogContaining(logs, "Marking test second test as modified"))
}

func TestBitmapIntersectsLineRange(t *testing.T) {
	tests := []struct {
		name      string
		bitmap    []byte
		startLine int
		endLine   int
		want      bool
	}{
		{
			name:      "first bit in a byte",
			bitmap:    []byte{0b10000000},
			startLine: 1,
			endLine:   1,
			want:      true,
		},
		{
			name:      "middle bit in a byte",
			bitmap:    []byte{0b00010000},
			startLine: 4,
			endLine:   4,
			want:      true,
		},
		{
			name:      "last bit in a byte",
			bitmap:    []byte{0b00000001},
			startLine: 8,
			endLine:   8,
			want:      true,
		},
		{
			name:      "range crossing byte boundaries",
			bitmap:    []byte{0b00000001, 0b10000000},
			startLine: 8,
			endLine:   9,
			want:      true,
		},
		{
			name:      "range before active bits",
			bitmap:    []byte{0b00000001},
			startLine: 1,
			endLine:   7,
			want:      false,
		},
		{
			name:      "range after bitmap length",
			bitmap:    []byte{0b11111111},
			startLine: 9,
			endLine:   12,
			want:      false,
		},
		{
			name:      "range partly beyond bitmap length",
			bitmap:    []byte{0b00000001},
			startLine: 8,
			endLine:   12,
			want:      true,
		},
		{
			name:      "empty non-nil bitmap",
			bitmap:    []byte{},
			startLine: 1,
			endLine:   8,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bitmapIntersectsLineRange(tt.bitmap, tt.startLine, tt.endLine)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBitmapIntersectsLineRangeMatchesFileBitmapIntersection(t *testing.T) {
	modifiedBitmaps := []struct {
		name   string
		bitmap []byte
	}{
		{name: "empty bitmap", bitmap: []byte{}},
		{name: "single active bit", bitmap: filebitmap.FromActiveRange(4, 4).GetBuffer()},
		{name: "multiple active bits in one byte", bitmap: filebitmap.FromActiveRange(2, 6).GetBuffer()},
		{name: "range crosses byte boundary", bitmap: filebitmap.FromActiveRange(7, 10).GetBuffer()},
		{name: "sparse manual bitmap", bitmap: []byte{0b10000001, 0b00010000, 0b00000001}},
	}
	testRanges := []lineRange{
		{start: 1, end: 1},
		{start: 1, end: 8},
		{start: 4, end: 4},
		{start: 5, end: 9},
		{start: 8, end: 12},
		{start: 11, end: 20},
		{start: 21, end: 24},
		{start: 25, end: 40},
	}

	for _, modifiedBitmap := range modifiedBitmaps {
		t.Run(modifiedBitmap.name, func(t *testing.T) {
			for _, testRange := range testRanges {
				testBitmap := filebitmap.FromActiveRange(testRange.start, testRange.end)
				modifiedFileBitmap := filebitmap.NewFileBitmapFromBytes(modifiedBitmap.bitmap)
				want := testBitmap.IntersectsWith(modifiedFileBitmap)

				got := bitmapIntersectsLineRange(modifiedBitmap.bitmap, testRange.start, testRange.end)
				assert.Equal(t, want, got, "range %d-%d", testRange.start, testRange.end)
			}
		})
	}
}

func TestBitmapIntersectsLineRangeInvalidRangesPanic(t *testing.T) {
	tests := []struct {
		name      string
		startLine int
		endLine   int
	}{
		{name: "negative start", startLine: -1, endLine: 5},
		{name: "start after end", startLine: 10, endLine: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.PanicsWithValue(t, "Invalid range", func() {
				_ = bitmapIntersectsLineRange([]byte{0xff}, tt.startLine, tt.endLine)
			})
		})
	}
}

func TestBitmapIntersectsLineRangeAllocs(t *testing.T) {
	bitmap := filebitmap.FromActiveRange(100, 200).GetBuffer()
	allocs := testing.AllocsPerRun(1000, func() {
		_ = bitmapIntersectsLineRange(bitmap, 150, 160)
	})
	assert.Zero(t, allocs)
}

func TestIsImpactedConcurrentAccess(t *testing.T) {
	analyzer := &ImpactedTestAnalyzer{
		modifiedFiles: []fileWithBitmap{
			{file: "pkg/source_test.go", bitmap: filebitmap.FromActiveRange(10, 20).GetBuffer()},
			{file: "pkg/other_test.go", bitmap: filebitmap.FromActiveRange(30, 40).GetBuffer()},
			{file: "pkg/no_line_info_test.go"},
		},
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Go(func() {
			assert.True(t, analyzer.IsImpacted("test", "/workspace/pkg/source_test.go", 12, 14))
			assert.False(t, analyzer.IsImpacted("test", "/workspace/pkg/source_test.go", 1, 2))
			assert.True(t, analyzer.IsImpacted("test", "/workspace/pkg/other_test.go", 35, 36))
			assert.True(t, analyzer.IsImpacted("test", "/workspace/pkg/no_line_info_test.go", 0, 0))
			assert.False(t, analyzer.IsImpacted("test", "/workspace/pkg/missing_test.go", 1, 2))
		})
	}
	wg.Wait()
}

func countLogsContaining(logs []string, fragment string) int {
	count := 0
	for _, log := range logs {
		if strings.Contains(log, fragment) {
			count++
		}
	}
	return count
}

func hasLogContaining(logs []string, fragment string) bool {
	return countLogsContaining(logs, fragment) > 0
}
