// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package coverage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanCollect(t *testing.T) {
	tests := []struct {
		mode     string
		expected bool
	}{
		{"", false},
		{"count", true},
		{"atomic", true},
		{"set", false},
	}

	for _, test := range tests {
		mode = test.mode
		result := CanCollect()
		if result != test.expected {
			t.Errorf("CanCollect() with mode=%q returned %v, expected %v", test.mode, result, test.expected)
		}
	}
}

func TestGetCoverage(t *testing.T) {
	// Mock environment
	mode = "count"
	temporaryDir = t.TempDir()

	// Mock tearDown function
	tearDown = func(coverprofile string, _ string) (string, error) {
		// Create a dummy coverage file
		f, err := os.Create(coverprofile)
		if err != nil {
			return "", err
		}
		defer f.Close()
		f.WriteString("mode: count\n")
		f.WriteString("github.com/example/project/file.go:1.1,1.10 1 1\n")
		return "", nil
	}

	coverage := GetCoverage()
	if coverage != 1.0 {
		t.Errorf("Expected coverage to be 1.0, got %v", coverage)
	}
}

func TestNewTestCoverage(t *testing.T) {
	sessionID := uint64(1)
	moduleID := uint64(2)
	suiteID := uint64(3)
	testID := uint64(4)
	testFile := "/path/to/testfile.go"

	tc := NewTestCoverage(sessionID, moduleID, suiteID, testID, testFile)
	if tc == nil {
		t.Fatal("NewTestCoverage returned nil")
	}

	tcv, ok := tc.(*testCoverage)
	if !ok {
		t.Fatal("NewTestCoverage did not return *testCoverage")
	}

	if tcv.sessionID != sessionID {
		t.Errorf("Expected sessionID %d, got %d", sessionID, tcv.sessionID)
	}
	if tcv.moduleID != moduleID {
		t.Errorf("Expected moduleID %d, got %d", moduleID, tcv.moduleID)
	}
	if tcv.suiteID != suiteID {
		t.Errorf("Expected suiteID %d, got %d", suiteID, tcv.suiteID)
	}
	if tcv.testID != testID {
		t.Errorf("Expected testID %d, got %d", testID, tcv.testID)
	}
	if !strings.Contains(tcv.testFile, testFile) {
		t.Errorf("Expected testFile %s, got %s", testFile, tcv.testFile)
	}
}

func TestParseCoverProfile(t *testing.T) {
	content := `mode: count
github.com/example/project/file1.go:10.12,12.3 2 1
github.com/example/project/file2.go:20.5,22.10 1 0
`

	tempFile, err := os.CreateTemp("", "coverage_profile")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = tempFile.WriteString(content)
	if err != nil {
		t.Fatal(err)
	}

	data, err := parseCoverProfile(tempFile.Name())
	if err != nil {
		t.Fatalf("parseCoverProfile returned error: %v", err)
	}

	if len(data) != 2 {
		t.Errorf("Expected 2 files in coverage data, got %d", len(data))
	}

	blocks1, ok := data["github.com/example/project/file1.go"]
	if !ok || len(blocks1) != 1 {
		t.Errorf("Expected one block for file1.go, got %d", len(blocks1))
	} else {
		block := blocks1[0]
		if block.startLine != 10 || block.startCol != 12 ||
			block.endLine != 12 || block.endCol != 3 ||
			block.numStmt != 2 || block.count != 1 {
			t.Errorf("Block data for file1.go does not match expected values")
		}
	}

	blocks2, ok := data["github.com/example/project/file2.go"]
	if !ok || len(blocks2) != 1 {
		t.Errorf("Expected one block for file2.go, got %d", len(blocks2))
	} else {
		block := blocks2[0]
		if block.startLine != 20 || block.startCol != 5 ||
			block.endLine != 22 || block.endCol != 10 ||
			block.numStmt != 1 || block.count != 0 {
			t.Errorf("Block data for file2.go does not match expected values")
		}
	}
}

func TestGetCoverageStatementsInfo(t *testing.T) {
	content := `mode: count
github.com/example/project/file1.go:10.12,12.3 2 1
github.com/example/project/file2.go:20.5,22.10 1 0
`

	tempFile, err := os.CreateTemp("", "coverage_profile")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = tempFile.WriteString(content)
	if err != nil {
		t.Fatal(err)
	}

	total, covered, err := getCoverageStatementsInfo(tempFile.Name())
	if err != nil {
		t.Fatalf("getCoverageStatementsInfo returned error: %v", err)
	}

	if total != 3 {
		t.Errorf("Expected total statements to be 3, got %d", total)
	}
	if covered != 2 {
		t.Errorf("Expected covered statements to be 2, got %d", covered)
	}
}

func TestGetFilesCovered(t *testing.T) {
	before := map[string][]coverageBlock{
		"file1.go": {
			{startLine: 1, startCol: 0, endLine: 1, endCol: 10, numStmt: 1, count: 0},
		},
		"file2.go": {
			{startLine: 2, startCol: 0, endLine: 2, endCol: 10, numStmt: 1, count: 0},
		},
	}

	after := map[string][]coverageBlock{
		"file1.go": {
			{startLine: 1, startCol: 0, endLine: 1, endCol: 10, numStmt: 1, count: 1},
		},
		"file2.go": {
			{startLine: 2, startCol: 0, endLine: 2, endCol: 10, numStmt: 1, count: 0},
		},
		"file3.go": {
			{startLine: 3, startCol: 0, endLine: 3, endCol: 10, numStmt: 1, count: 1},
		},
	}

	testFile := "testfile.go"
	filesCovered := getFilesCovered(testFile, before, after)
	expectedFiles := []string{"testfile.go", "file1.go", "file3.go"}

	if len(filesCovered) != len(expectedFiles) {
		t.Errorf("Expected %d files covered, got %d", len(expectedFiles), len(filesCovered))
	}

	for _, expectedFile := range expectedFiles {
		found := false
		for _, file := range filesCovered {
			if file == expectedFile {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected file %s to be in covered files", expectedFile)
		}
	}
}

func TestCollectCoverageBeforeTestExecution(t *testing.T) {
	// Mock environment
	tempDir := t.TempDir()
	temporaryDir = tempDir

	// Mock tearDown function
	tearDown = func(coverprofile string, _ string) (string, error) {
		// Create a dummy coverage file
		f, err := os.Create(coverprofile)
		if err != nil {
			return "", err
		}
		defer f.Close()
		f.WriteString("mode: count\n")
		return "", nil
	}

	mode = "count"

	tc := &testCoverage{
		moduleID: 1,
		suiteID:  2,
		testID:   3,
	}

	tc.CollectCoverageBeforeTestExecution()

	if tc.preCoverageFilename == "" {
		t.Error("preCoverageFilename is empty after CollectCoverageBeforeTestExecution")
	} else {
		if _, err := os.Stat(tc.preCoverageFilename); os.IsNotExist(err) {
			t.Errorf("Expected preCoverageFilename %s to exist", tc.preCoverageFilename)
		}
	}
}

func TestCollectCoverageAfterTestExecution(t *testing.T) {
	// Mock environment
	tempDir := t.TempDir()
	temporaryDir = tempDir

	// Mock tearDown function
	tearDown = func(coverprofile string, _ string) (string, error) {
		// Create a dummy coverage file
		f, err := os.Create(coverprofile)
		if err != nil {
			return "", err
		}
		defer f.Close()
		f.WriteString("mode: count\n")
		return "", nil
	}
	covWriter = newCoverageWriter()
	covWriter.client = &MockClient{SendCoveragePayloadFunc: func(_ io.Reader) error {
		return fmt.Errorf("mock error")
	},
	}

	mode = "count"

	tc := &testCoverage{
		moduleID:            1,
		suiteID:             2,
		testID:              3,
		preCoverageFilename: filepath.Join(tempDir, "pre.out"),
	}

	// Create a dummy pre-coverage file
	f, err := os.Create(tc.preCoverageFilename)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("mode: count\n")
	f.Close()

	err = tc.getCoverageData()
	if err != nil {
		t.Errorf("getCoverageData returned error: %v", err)
	}

	if tc.postCoverageFilename == "" {
		t.Error("postCoverageFilename is empty after CollectCoverageAfterTestExecution")
	} else {
		if _, err := os.Stat(tc.postCoverageFilename); os.IsNotExist(err) {
			t.Errorf("Expected postCoverageFilename %s to exist", tc.postCoverageFilename)
		}
	}
}
