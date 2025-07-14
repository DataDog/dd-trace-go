// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package impactedtests

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/filebitmap"
	logger "github.com/DataDog/dd-trace-go/v2/internal/log"
)

type (
	// fileWithBitmap represents a file with its coverage bitmap.
	fileWithBitmap struct {
		file   string // file path
		bitmap []byte // coverage bitmap
	}

	// ImpactedTestAnalyzer is a struct that holds information about impacted tests.
	ImpactedTestAnalyzer struct {
		modifiedFiles    []fileWithBitmap
		currentCommitSha string
		baseCommitSha    string
	}

	// lineRange represents a tuple of start and end line numbers.
	lineRange struct {
		start int
		end   int
	}
)

// Precompiled regex for diff header and line changes.
// Adjust these patterns to match the actual output of "git diff".
// Example: diff --git a/file.txt b/file.txt
var diffHeaderRegex = regexp.MustCompile(`^diff --git a\/(?P<fileA>.+) b\/(?P<fileB>.+)`)

// Example: @@ -1,2 +3,4 @@
// This regex captures "start" and "count" (if available) from the new file's diff.
var lineChangeRegex = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(?P<start>\d+)(?:,(?P<count>\d+))? @@`)

// NewImpactedTestAnalyzer creates a new instance of ImpactedTestAnalyzer.
func NewImpactedTestAnalyzer() (*ImpactedTestAnalyzer, error) {
	ciTags := utils.GetCITags()

	// Get the current commit SHA
	currentCommitSha := ciTags[constants.GitHeadCommit]
	if currentCommitSha == "" {
		currentCommitSha = ciTags[constants.GitCommitSHA]
	}
	if currentCommitSha == "" {
		return nil, fmt.Errorf("civisibility.ImpactedTests: current commit is empty")
	}

	// Get the base commit SHA
	baseCommitSha := ciTags[constants.GitPrBaseCommit]
	if baseCommitSha == "" {
		baseCommitSha = ciTags[constants.GitPrBaseBranch]
	}

	// If we don't have the base commit from the tags, then let's try to calculate it using the git CLI
	if baseCommitSha == "" {
		var err error
		baseCommitSha, err = utils.GetBaseBranchSha("") // empty string triggers auto-detection
		if err != nil {
			logger.Debug("civisibility.ImpactedTests: Failed to get base commit SHA from git CLI: %s", err.Error())
			// Don't fail here - we might be on a base branch or in a scenario where
			// base branch detection isn't possible. Return an analyzer with no modified files.
		}
	}

	// Extract the modified files
	var modifiedFiles []fileWithBitmap

	// Check if the base commit SHA is available
	if len(baseCommitSha) > 0 {
		logger.Debug("civisibility.ImpactedTests: PR detected. Retrieving diff lines from Git CLI from BaseCommit %s", baseCommitSha)
		modifiedFiles = getGitDiffFrom(baseCommitSha, currentCommitSha)
	}

	// If we still don't have modified files, initialize with empty slice instead of failing
	if modifiedFiles == nil {
		logger.Debug("civisibility.ImpactedTests: No modified files found - initializing with empty list")
		modifiedFiles = []fileWithBitmap{}
	}

	logger.Debug("civisibility.ImpactedTests: loaded [from: %s to %s]: %v", baseCommitSha, currentCommitSha, modifiedFiles) //nolint:gocritic // File list debug logging
	return &ImpactedTestAnalyzer{
		modifiedFiles:    modifiedFiles,
		currentCommitSha: currentCommitSha,
		baseCommitSha:    baseCommitSha,
	}, nil
}

// IsImpacted checks if a test is impacted based on the modified files and their line ranges.
func (a *ImpactedTestAnalyzer) IsImpacted(testName string, sourceFile string, startLine int, endLine int) bool {
	if len(a.modifiedFiles) == 0 {
		return false
	}

	// Has the test been modified?
	modified := false

	// Get the test impact information
	testFiles := getTestImpactInfo(sourceFile, startLine, endLine)
	if len(testFiles) == 0 {
		return false
	}

	for _, testFile := range testFiles {
		if testFile == nil || testFile.file == "" {
			continue
		}

		modifiedFileIndex := slices.IndexFunc(a.modifiedFiles, func(file fileWithBitmap) bool {
			if file.file == "" {
				return false
			}
			return strings.HasSuffix(testFile.file, file.file)
		})
		if modifiedFileIndex >= 0 {
			modifiedFile := a.modifiedFiles[modifiedFileIndex]
			logger.Debug("civisibility.ImpactedTests: DiffFile found: %s...", modifiedFile.file)
			if testFile.bitmap == nil || modifiedFile.bitmap == nil {
				logger.Debug("civisibility.ImpactedTests: No line info found")
				modified = true
				break
			}

			testFileBitmap := filebitmap.NewFileBitmapFromBytes(testFile.bitmap)
			modifiedFileBitmap := filebitmap.NewFileBitmapFromBytes(modifiedFile.bitmap)

			if testFileBitmap.IntersectsWith(modifiedFileBitmap) {
				logger.Debug("civisibility.ImpactedTests: Intersecting lines. Marking test %s as modified.", testName)
				modified = true
				break
			}
		}
	}

	return modified
}

// getGitDiffFrom retrieves the diff files and lines from the Git CLI.
func getGitDiffFrom(baseCommitSha string, currentCommitSha string) []fileWithBitmap {
	var modifiedFiles []fileWithBitmap

	// Milestone 1.5 : Retrieve diff files and lines from Git Diff CLI
	output, err := utils.GetGitDiff(baseCommitSha, currentCommitSha)
	if err != nil {
		logger.Debug("civisibility.ImpactedTests: Failed to get diff files from Git CLI: %s", err.Error())
	} else if output != "" {
		modifiedFiles = parseGitDiffOutput(output)
	} else {
		logger.Debug("civisibility.ImpactedTests: No diff files found from Git CLI")
	}
	return modifiedFiles
}

// getTestImpactInfo returns the test impact information based on the tags.
func getTestImpactInfo(sourceFile string, startLine int, endLine int) []*fileWithBitmap {
	result := make([]*fileWithBitmap, 0)
	if sourceFile == "" {
		return result
	}

	// Milestone 1: Return only the test definition file
	file := &fileWithBitmap{file: sourceFile}
	result = append(result, file)

	// Milestone 1.5: Return the test definition lines
	if startLine == 0 || endLine == 0 {
		return result
	}

	bitmap := filebitmap.FromActiveRange(startLine, endLine)
	file.bitmap = bitmap.GetBuffer()

	return result
}

// parseGitDiffOutput parses the git diff output to extract modified files and their changed lines.
func parseGitDiffOutput(output string) []fileWithBitmap {
	var fileChanges []fileWithBitmap
	var currentFile *fileWithBitmap
	var modifiedLines []lineRange

	// Split output into lines (ignoring empty lines)
	lines := splitLines(output)
	for _, line := range lines {

		// Check for the start of a new file diff
		if headerMatch := diffHeaderRegex.FindStringSubmatch(line); headerMatch != nil {
			// If there's a file in process, finalize it before iniciar uno nuevo
			if currentFile != nil {
				currentFile.bitmap = toFileBitmap(modifiedLines)
				fileChanges = append(fileChanges, *currentFile)
				// Clear the modified lines for the new file
				modifiedLines = modifiedLines[:0]
			}

			// Extract file path from the named group "file"
			filePath := ""
			for i, name := range diffHeaderRegex.SubexpNames() {
				if name == "fileB" {
					filePath = headerMatch[i]
					break
				}
			}
			currentFile = &fileWithBitmap{file: filePath}
			continue
		}

		// Check for the line change marker (e.g., @@ -1,2 +3,4 @@)
		if lineChangeMatch := lineChangeRegex.FindStringSubmatch(line); lineChangeMatch != nil {
			startLineStr := ""
			countStr := ""
			for i, name := range lineChangeRegex.SubexpNames() {
				if name == "start" {
					startLineStr = lineChangeMatch[i]
				}
				if name == "count" {
					countStr = lineChangeMatch[i]
				}
			}
			startLine, err := strconv.Atoi(startLineStr)
			if err != nil {
				// In case of error, we skip the line
				continue
			}
			lineCount := 0
			if countStr != "" {
				lineCount, err = strconv.Atoi(countStr)
				if err != nil {
					lineCount = 0
				}
				if lineCount > 0 {
					// Adjust the line count to account for the start line
					lineCount = lineCount - 1
				}
			}

			// Add the range
			if startLine > 0 {
				modifiedLines = append(modifiedLines, lineRange{start: startLine, end: startLine + lineCount})
			}
			continue
		}
	}
	if currentFile != nil {
		currentFile.bitmap = toFileBitmap(modifiedLines)
		fileChanges = append(fileChanges, *currentFile)
	}

	return fileChanges
}

// splitLines splits the text into lines, ignoring empty lines.
func splitLines(text string) []string {
	rawLines := strings.Split(text, "\n")
	var lines []string
	for _, line := range rawLines {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// toFileBitmap converts a slice of modified line ranges into a bitmap (as a byte slice).
func toFileBitmap(modifiedLines []lineRange) []byte {
	if len(modifiedLines) == 0 {
		return nil
	}
	// Get the maximum count from the last range's end value.
	maxCount := modifiedLines[len(modifiedLines)-1].end
	bitmap := filebitmap.FromLineCount(maxCount)
	// Mark all lines in the ranges as modified.
	for _, r := range modifiedLines {
		// Note: This marks lines from r.start to r.end inclusive.
		for i := r.start; i <= r.end; i++ {
			bitmap.Set(i)
		}
	}
	return bitmap.GetBuffer()
}
