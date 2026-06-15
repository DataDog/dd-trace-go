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
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/filebitmap"
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

	if got := coveredFileNames(filesCovered); !slices.Equal(got, []string{"testfile.go", "file1.go", "file3.go"}) {
		t.Fatalf("unexpected covered file order: got %v", got)
	}
	assertCoveredFile(t, filesCovered, "testfile.go", nil)
	assertCoveredFile(t, filesCovered, "file1.go", filebitmap.FromActiveRange(1, 1).ToArray())
	assertCoveredFile(t, filesCovered, "file3.go", filebitmap.FromActiveRange(3, 3).ToArray())
}

func TestGetFilesCoveredBuildsBitmapForMultipleCoveredBlocks(t *testing.T) {
	before := map[string][]coverageBlock{
		"pkg/lib.go": {
			{startLine: 2, startCol: 1, endLine: 3, endCol: 10, numStmt: 1, count: 0},
			{startLine: 8, startCol: 1, endLine: 8, endCol: 10, numStmt: 1, count: 0},
			{startLine: 12, startCol: 1, endLine: 13, endCol: 10, numStmt: 1, count: 7},
		},
	}
	after := map[string][]coverageBlock{
		"pkg/lib.go": {
			{startLine: 2, startCol: 1, endLine: 3, endCol: 10, numStmt: 1, count: 1},
			{startLine: 8, startCol: 1, endLine: 8, endCol: 10, numStmt: 1, count: 1},
			{startLine: 12, startCol: 1, endLine: 13, endCol: 10, numStmt: 1, count: 7},
		},
	}

	want := filebitmap.FromLineCount(8)
	for _, line := range []int{2, 3, 8} {
		want.Set(line)
	}

	filesCovered := getFilesCovered("pkg/lib_test.go", before, after)
	assertCoveredFile(t, filesCovered, "pkg/lib_test.go", nil)
	assertCoveredFile(t, filesCovered, "pkg/lib.go", want.ToArray())
}

func TestGetFilesCoveredSkipsZeroAndNegativeDeltas(t *testing.T) {
	before := map[string][]coverageBlock{
		"pkg/lib.go": {
			{startLine: 1, startCol: 1, endLine: 1, endCol: 10, numStmt: 1, count: 2},
			{startLine: 2, startCol: 1, endLine: 2, endCol: 10, numStmt: 1, count: 3},
		},
	}
	after := map[string][]coverageBlock{
		"pkg/lib.go": {
			{startLine: 1, startCol: 1, endLine: 1, endCol: 10, numStmt: 1, count: 2},
			{startLine: 2, startCol: 1, endLine: 2, endCol: 10, numStmt: 1, count: 1},
		},
	}

	filesCovered := getFilesCovered("pkg/lib_test.go", before, after)
	if len(filesCovered) != 1 {
		t.Fatalf("expected only the test file entry, got %#v", filesCovered)
	}
	assertCoveredFile(t, filesCovered, "pkg/lib_test.go", nil)
}

func TestGetFilesCoveredUsesFileBitmapByteOrder(t *testing.T) {
	before := map[string][]coverageBlock{}
	after := map[string][]coverageBlock{
		"pkg/lib.go": {
			{startLine: 1, startCol: 1, endLine: 1, endCol: 10, numStmt: 1, count: 1},
			{startLine: 8, startCol: 1, endLine: 8, endCol: 10, numStmt: 1, count: 1},
		},
	}

	filesCovered := getFilesCovered("pkg/lib_test.go", before, after)
	assertCoveredFile(t, filesCovered, "pkg/lib.go", []byte{0x81})
}

func assertCoveredFile(t *testing.T, files []coveredFile, name string, bitmap []byte) {
	t.Helper()
	for _, file := range files {
		if file.name != name {
			continue
		}
		if !slices.Equal(file.bitmap, bitmap) {
			t.Fatalf("covered file %s bitmap mismatch: got %08b want %08b", name, file.bitmap, bitmap)
		}
		return
	}
	t.Fatalf("covered file %s not found in %#v", name, files)
}

func coveredFileNames(files []coveredFile) []string {
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, file.name)
	}
	return names
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
	coverageUploadEnabled = true

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
	coverageUploadEnabled = true

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

func TestFinalizeBackfillRewritesOnlyZeroCountMatchingBlocks(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	tempDir := t.TempDir()
	profilePath := filepath.Join(tempDir, "coverage.out")
	content := `mode: count
# preserved comment
github.com/example/project/lib/lib.go:2.1,2.10 1 0
github.com/example/project/lib/lib.go:3.1,3.10 2 7
github.com/example/project/app/app.go:4.1,4.10 1 0
`
	if err := os.WriteFile(profilePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	mode = "count"
	modulePath = "github.com/example/project"
	tearDown = func(_, _ string) (string, error) {
		return "", fmt.Errorf("tearDown should not run when coverprofile exists")
	}
	runtimeSnapshot = &runtimeCoverageSnapshot{path: profilePath}
	ConfigureBackfill(BackfillInput{
		BackendCoverage: map[string]*filebitmap.FileBitmap{
			"lib/lib.go": filebitmap.FromActiveRange(2, 2),
		},
		ActualSkips: 1,
	})

	result := FinalizeBackfill()
	if result.Reason != "" {
		t.Fatalf("unexpected reason: %s", result.Reason)
	}
	if !result.Applied {
		t.Fatal("expected backfill to be applied")
	}
	if result.Coverage != 0.75 {
		t.Fatalf("expected corrected coverage 0.75, got %v", result.Coverage)
	}

	updated, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	expected := `mode: count
# preserved comment
github.com/example/project/lib/lib.go:2.1,2.10 1 1
github.com/example/project/lib/lib.go:3.1,3.10 2 7
github.com/example/project/app/app.go:4.1,4.10 1 0
`
	if string(updated) != expected {
		t.Fatalf("unexpected profile contents:\n%s", string(updated))
	}
}

func TestFinalizeBackfillMatchesNestedSemanticImportModulePaths(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	for _, version := range []string{"v2", "v10"} {
		t.Run(version, func(t *testing.T) {
			ResetForTesting()
			profilePath := filepath.Join(t.TempDir(), "coverage.out")
			profileLine := "github.com/example/project/" + version + "/internal/civisibility/integrations/gotesting/fixtures/itrbackfill/orchestrion/lib/lib.go:9.19,13.2 3 0"
			if err := os.WriteFile(profilePath, []byte("mode: count\n"+profileLine+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			mode = "count"
			modulePath = "github.com/example/project/" + version + "/internal/civisibility/integrations/gotesting/fixtures/itrbackfill/orchestrion"
			tearDown = func(_, _ string) (string, error) { return "", nil }
			runtimeSnapshot = &runtimeCoverageSnapshot{path: profilePath}
			ConfigureBackfill(BackfillInput{
				BackendCoverage: map[string]*filebitmap.FileBitmap{
					"internal/civisibility/integrations/gotesting/fixtures/itrbackfill/orchestrion/lib/lib.go": filebitmap.FromActiveRange(9, 13),
				},
				ActualSkips: 1,
			})

			result := FinalizeBackfill()
			if result.Reason != "" {
				t.Fatalf("unexpected reason: %s", result.Reason)
			}
			if !result.Applied {
				t.Fatal("expected nested module path backfill to be applied")
			}

			updated, err := os.ReadFile(profilePath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(updated), profileLine[:strings.LastIndex(profileLine, " ")]+" 1") {
				t.Fatalf("expected profile count to be backfilled, got:\n%s", string(updated))
			}
		})
	}
}

func TestModuleRepositoryRelativePrefixRejectsLeadingZeroSemanticImportVersion(t *testing.T) {
	if got := moduleRepositoryRelativePrefix("github.com/example/project/v02/internal/package"); got != "" {
		t.Fatalf("expected v02 not to be treated as a semantic import version, got %q", got)
	}
}

func TestPreflightBackfillDoesNotEmitRuntimeCoverage(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	moduleDir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(moduleDir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "lib", "lib.go"), []byte("package lib\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mode = "count"
	modulePath = "github.com/example/project"
	tearDownCalls := 0
	tearDown = func(_, _ string) (string, error) {
		tearDownCalls++
		return "", nil
	}

	result := PreflightBackfill(BackfillInput{
		BackendCoverage: map[string]*filebitmap.FileBitmap{
			"lib/lib.go": filebitmap.FromActiveRange(2, 2),
		},
	})
	if result.Reason != "" {
		t.Fatalf("unexpected reason: %s", result.Reason)
	}
	if tearDownCalls != 0 {
		t.Fatalf("preflight must not emit a runtime coverage profile, got %d tearDown calls", tearDownCalls)
	}
	if runtimeSnapshot != nil {
		t.Fatal("preflight must not set the final runtime snapshot")
	}
}

func TestPreflightBackfillFailsClosedOnMissingSourceFile(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	moduleDir = t.TempDir()
	mode = "count"
	modulePath = "github.com/example/project"
	tearDown = func(_, _ string) (string, error) {
		t.Fatal("preflight must not emit a runtime coverage profile")
		return "", nil
	}

	result := PreflightBackfill(BackfillInput{
		BackendCoverage: map[string]*filebitmap.FileBitmap{
			"pkg/missing.go": filebitmap.FromActiveRange(2, 2),
		},
	})
	if result.Reason != "coverage paths unmatched" {
		t.Fatalf("expected unmatched reason, got %q", result.Reason)
	}
	if result.UnmatchedBackendFiles != 1 {
		t.Fatalf("expected one unmatched backend file, got %d", result.UnmatchedBackendFiles)
	}
}

func TestFinalizeBackfillIgnoresBackendFilesOutsideLocalProfile(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	profilePath := filepath.Join(t.TempDir(), "coverage.out")
	original := "mode: count\npkg/matched.go:2.1,2.10 1 0\n"
	if err := os.WriteFile(profilePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	mode = "count"
	tearDown = func(_, _ string) (string, error) { return "", nil }
	runtimeSnapshot = &runtimeCoverageSnapshot{path: profilePath}
	ConfigureBackfill(BackfillInput{
		BackendCoverage: map[string]*filebitmap.FileBitmap{
			"pkg/matched.go":             filebitmap.FromActiveRange(2, 2),
			"other-process/unmatched.go": filebitmap.FromActiveRange(3, 3),
		},
		ActualSkips: 1,
	})

	result := FinalizeBackfill()
	if result.Reason != "" {
		t.Fatalf("unexpected reason: %q", result.Reason)
	}
	if result.MatchedBlocks != 1 {
		t.Fatalf("expected one matched block, got %d", result.MatchedBlocks)
	}
	if result.UnmatchedBackendFiles != 0 {
		t.Fatalf("expected unrelated backend files to be ignored, got %d unmatched", result.UnmatchedBackendFiles)
	}
	updated, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "pkg/matched.go:2.1,2.10 1 1") {
		t.Fatalf("expected matched profile block to be backfilled, got:\n%s", string(updated))
	}
}

func TestFinalizeBackfillRejectsInvalidCoverageProfile(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	profilePath := filepath.Join(t.TempDir(), "coverage.out")
	if err := os.WriteFile(profilePath, []byte("mode: count\npkg/file.go:2.1,2.10 1 0 extra\npkg/other.go:3.1,3.10 1 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mode = "count"
	tearDown = func(_, _ string) (string, error) { return "", nil }
	runtimeSnapshot = &runtimeCoverageSnapshot{path: profilePath}
	ConfigureBackfill(BackfillInput{
		BackendCoverage: map[string]*filebitmap.FileBitmap{
			"pkg/file.go": filebitmap.FromActiveRange(2, 2),
		},
		ActualSkips: 1,
	})

	result := FinalizeBackfill()
	if result.Reason != "coverage profile invalid" {
		t.Fatalf("expected invalid profile reason, got %q", result.Reason)
	}
	updated, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	expected := "mode: count\npkg/file.go:2.1,2.10 1 0 extra\npkg/other.go:3.1,3.10 1 0\n"
	if string(updated) != expected {
		t.Fatalf("profile should not have changed:\n%s", string(updated))
	}
}

func TestFinalizeBackfillFailsClosedOnLocalCoverageLineMismatch(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	profilePath := filepath.Join(t.TempDir(), "coverage.out")
	original := "mode: count\npkg/matched.go:2.1,2.10 1 0\n"
	if err := os.WriteFile(profilePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	mode = "count"
	tearDown = func(_, _ string) (string, error) { return "", nil }
	runtimeSnapshot = &runtimeCoverageSnapshot{path: profilePath}
	ConfigureBackfill(BackfillInput{
		BackendCoverage: map[string]*filebitmap.FileBitmap{
			"pkg/matched.go": filebitmap.FromActiveRange(3, 3),
		},
		ActualSkips: 1,
	})

	result := FinalizeBackfill()
	if result.Reason != "coverage paths unmatched" {
		t.Fatalf("expected unmatched reason, got %q", result.Reason)
	}
	if result.MatchedBlocks != 0 {
		t.Fatalf("expected no matched blocks before fail-closed, got %d", result.MatchedBlocks)
	}
	if result.UnmatchedBackendFiles != 1 {
		t.Fatalf("expected one unmatched backend file, got %d", result.UnmatchedBackendFiles)
	}
	updated, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != original {
		t.Fatalf("profile should not have changed:\n%s", string(updated))
	}
}

func TestFinalizeBackfillRewritesAtomicProfile(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	profilePath := filepath.Join(t.TempDir(), "coverage.out")
	content := `mode: atomic
github.com/example/project/lib/lib.go:2.1,2.10 1 0
github.com/example/project/lib/lib.go:3.1,3.10 1 4
`
	if err := os.WriteFile(profilePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	mode = "atomic"
	modulePath = "github.com/example/project"
	tearDown = func(_, _ string) (string, error) { return "", nil }
	runtimeSnapshot = &runtimeCoverageSnapshot{path: profilePath}
	ConfigureBackfill(BackfillInput{
		BackendCoverage: map[string]*filebitmap.FileBitmap{
			"lib/lib.go": filebitmap.FromActiveRange(2, 3),
		},
		ActualSkips: 1,
	})

	result := FinalizeBackfill()
	if result.Reason != "" {
		t.Fatalf("unexpected reason: %s", result.Reason)
	}
	if !result.Applied {
		t.Fatal("expected atomic profile backfill to be applied")
	}
	updated, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	expected := `mode: atomic
github.com/example/project/lib/lib.go:2.1,2.10 1 1
github.com/example/project/lib/lib.go:3.1,3.10 1 4
`
	if string(updated) != expected {
		t.Fatalf("unexpected profile contents:\n%s", string(updated))
	}
}

func TestFinalizeBackfillAllowsZeroStatementBlocks(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	profilePath := filepath.Join(t.TempDir(), "coverage.out")
	content := `mode: count
pkg/file.go:3.15,4.2 0 0
pkg/file.go:6.1,6.10 1 0
`
	if err := os.WriteFile(profilePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	mode = "count"
	tearDown = func(_, _ string) (string, error) { return "", nil }
	runtimeSnapshot = &runtimeCoverageSnapshot{path: profilePath}
	ConfigureBackfill(BackfillInput{
		BackendCoverage: map[string]*filebitmap.FileBitmap{
			"pkg/file.go": filebitmap.FromActiveRange(6, 6),
		},
		ActualSkips: 1,
	})

	result := FinalizeBackfill()
	if result.Reason != "" {
		t.Fatalf("unexpected reason: %s", result.Reason)
	}
	if result.Coverage != 1 {
		t.Fatalf("expected full statement coverage, got %v", result.Coverage)
	}
	updated, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	expected := `mode: count
pkg/file.go:3.15,4.2 0 0
pkg/file.go:6.1,6.10 1 1
`
	if string(updated) != expected {
		t.Fatalf("unexpected profile contents:\n%s", string(updated))
	}
}

func TestFinalizeBackfillParsesProfilePathsWithColon(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	profilePath := filepath.Join(t.TempDir(), "coverage.out")
	content := "mode: count\nC:/work/repo/pkg/file.go:2.1,2.10 1 0\n"
	if err := os.WriteFile(profilePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	mode = "count"
	tearDown = func(_, _ string) (string, error) { return "", nil }
	runtimeSnapshot = &runtimeCoverageSnapshot{path: profilePath}
	ConfigureBackfill(BackfillInput{
		BackendCoverage: map[string]*filebitmap.FileBitmap{
			"C:/work/repo/pkg/file.go": filebitmap.FromActiveRange(2, 2),
		},
		ActualSkips: 1,
	})

	result := FinalizeBackfill()
	if result.Reason != "" {
		t.Fatalf("unexpected reason: %s", result.Reason)
	}
	updated, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != "mode: count\nC:/work/repo/pkg/file.go:2.1,2.10 1 1\n" {
		t.Fatalf("unexpected profile contents:\n%s", string(updated))
	}
}

func TestFinalizeBackfillFailsClosedWhenCoverageDoesNotMatchProfile(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	profilePath := filepath.Join(t.TempDir(), "coverage.out")
	if err := os.WriteFile(profilePath, []byte("mode: count\npkg/file.go:2.1,2.10 1 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mode = "count"
	tearDown = func(_, _ string) (string, error) { return "", nil }
	runtimeSnapshot = &runtimeCoverageSnapshot{path: profilePath}
	ConfigureBackfill(BackfillInput{
		BackendCoverage: map[string]*filebitmap.FileBitmap{
			"pkg/other.go": filebitmap.FromActiveRange(2, 2),
		},
		ActualSkips: 1,
	})

	result := FinalizeBackfill()
	if result.Reason != "coverage paths unmatched" {
		t.Fatalf("expected unmatched reason, got %q", result.Reason)
	}
	updated, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != "mode: count\npkg/file.go:2.1,2.10 1 0\n" {
		t.Fatalf("profile should not have changed: %s", string(updated))
	}
}

func TestFinalizeBackfillAllowsMatchingAlreadyCoveredBlocks(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	profilePath := filepath.Join(t.TempDir(), "coverage.out")
	if err := os.WriteFile(profilePath, []byte("mode: count\npkg/file.go:2.1,2.10 1 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mode = "count"
	tearDown = func(_, _ string) (string, error) { return "", nil }
	runtimeSnapshot = &runtimeCoverageSnapshot{path: profilePath}
	ConfigureBackfill(BackfillInput{
		BackendCoverage: map[string]*filebitmap.FileBitmap{
			"pkg/file.go": filebitmap.FromActiveRange(2, 2),
		},
		ActualSkips: 1,
	})

	result := FinalizeBackfill()
	if result.Reason != "" {
		t.Fatalf("unexpected reason: %s", result.Reason)
	}
	if result.Applied {
		t.Fatal("backfill should not rewrite already covered blocks")
	}
	if result.Coverage != 1 {
		t.Fatalf("expected full coverage, got %v", result.Coverage)
	}
}
