// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package impactedtests

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

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

	// ImpactedTestAnalyzer is a struct that holds information about impacted tests. It caches
	// prepared diff data and impacted decisions, so analyzers should be used by pointer and must
	// not be copied after first use.
	ImpactedTestAnalyzer struct {
		modifiedFiles []fileWithBitmap

		// modifiedFilesOnce protects preparation of modifiedFiles. modifiedFiles remains the
		// source of truth until this preparation runs; after that, preparedModifiedFiles is the
		// analyzer-local snapshot used by IsImpacted.
		modifiedFilesOnce sync.Once

		// preparedModifiedFiles preserves the original modifiedFiles order because impacted-test
		// matching is suffix-based and first-match-wins. Bitmap byte slices are referenced, not
		// deep-copied, because diff data is immutable after analyzer creation in production.
		preparedModifiedFiles []preparedModifiedFile

		// decisionCache stores per-analyzer impacted decisions. testName is intentionally not part
		// of the cache key because it only affects debug logging, not the decision.
		decisionCache sync.Map

		currentCommitSha string
		baseCommitSha    string
	}

	// preparedModifiedFile is the per-analyzer representation used by IsImpacted after modified
	// file preparation has run. The bitmap field shares the original immutable bitmap bytes.
	preparedModifiedFile struct {
		file   string
		bitmap []byte
	}

	// impactCacheKey identifies an impacted-test decision for one source range.
	impactCacheKey struct {
		sourceFile string
		startLine  int
		endLine    int
	}

	// impactCacheEntry stores a cached impacted-test decision plus enough context to replay
	// observable debug log fragments on cache hits.
	impactCacheEntry struct {
		impacted    bool
		matchedFile string
		reason      impactDecisionReason
	}

	// impactDecisionReason explains which branch produced a cached impacted-test decision.
	impactDecisionReason uint8

	// lineRange represents a tuple of start and end line numbers.
	lineRange struct {
		start int
		end   int
	}
)

const (
	// impactDecisionNoMatch means either no file matched or a matched file had no intersecting lines.
	impactDecisionNoMatch impactDecisionReason = iota
	// impactDecisionNoLineInfo means the file matched but either side lacked usable line details.
	impactDecisionNoLineInfo
	// impactDecisionLineIntersection means the test range intersects modified lines.
	impactDecisionLineIntersection
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
	analyzer := &ImpactedTestAnalyzer{
		modifiedFiles:    modifiedFiles,
		currentCommitSha: currentCommitSha,
		baseCommitSha:    baseCommitSha,
	}
	analyzer.getPreparedModifiedFiles()
	return analyzer, nil
}

// IsImpacted checks if a test is impacted based on the modified files and their line ranges.
func (a *ImpactedTestAnalyzer) IsImpacted(testName string, sourceFile string, startLine int, endLine int) bool {
	if sourceFile == "" {
		return false
	}

	modifiedFiles := a.getPreparedModifiedFiles()
	if len(modifiedFiles) == 0 {
		return false
	}
	validateImpactedLineRange(startLine, endLine)

	cacheKey := impactCacheKey{
		sourceFile: sourceFile,
		startLine:  startLine,
		endLine:    endLine,
	}
	if cached, ok := a.decisionCache.Load(cacheKey); ok {
		entry := cached.(impactCacheEntry)
		logImpactDecision(testName, entry)
		return entry.impacted
	}

	for _, modifiedFile := range modifiedFiles {
		if modifiedFile.file == "" {
			continue
		}

		if strings.HasSuffix(sourceFile, modifiedFile.file) {
			logDiffFileFound(modifiedFile.file)
			if startLine == 0 || endLine == 0 || modifiedFile.bitmap == nil {
				logNoLineInfo()
				entry := impactCacheEntry{
					impacted:    true,
					matchedFile: modifiedFile.file,
					reason:      impactDecisionNoLineInfo,
				}
				a.decisionCache.Store(cacheKey, entry)
				return true
			}

			if bitmapIntersectsLineRange(modifiedFile.bitmap, startLine, endLine) {
				logLineIntersection(testName)
				entry := impactCacheEntry{
					impacted:    true,
					matchedFile: modifiedFile.file,
					reason:      impactDecisionLineIntersection,
				}
				a.decisionCache.Store(cacheKey, entry)
				return true
			}

			entry := impactCacheEntry{
				impacted:    false,
				matchedFile: modifiedFile.file,
				reason:      impactDecisionNoMatch,
			}
			a.decisionCache.Store(cacheKey, entry)
			return false
		}
	}

	a.decisionCache.Store(cacheKey, impactCacheEntry{
		impacted: false,
		reason:   impactDecisionNoMatch,
	})
	return false
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

// prepareModifiedFiles creates the analyzer-local modified-file snapshot used by IsImpacted.
func prepareModifiedFiles(files []fileWithBitmap) []preparedModifiedFile {
	prepared := make([]preparedModifiedFile, len(files))
	for i, file := range files {
		prepared[i] = preparedModifiedFile{
			file:   file.file,
			bitmap: file.bitmap,
		}
	}
	return prepared
}

// getPreparedModifiedFiles returns the analyzer-local modified-file snapshot.
func (a *ImpactedTestAnalyzer) getPreparedModifiedFiles() []preparedModifiedFile {
	a.modifiedFilesOnce.Do(func() {
		a.preparedModifiedFiles = prepareModifiedFiles(a.modifiedFiles)
	})
	return a.preparedModifiedFiles
}

// logImpactDecision replays the debug log fragments that are observable from IsImpacted.
func logImpactDecision(testName string, entry impactCacheEntry) {
	if entry.matchedFile != "" {
		logDiffFileFound(entry.matchedFile)
	}
	switch entry.reason {
	case impactDecisionNoLineInfo:
		logNoLineInfo()
	case impactDecisionLineIntersection:
		logLineIntersection(testName)
	}
}

// logDiffFileFound emits the existing diff-match debug log without allocating when debug logs
// are disabled.
func logDiffFileFound(file string) {
	if logger.DebugEnabled() {
		logger.Debug("civisibility.ImpactedTests: DiffFile found: %s...", file)
	}
}

// logNoLineInfo emits the existing missing-line-info debug log without allocating when debug
// logs are disabled.
func logNoLineInfo() {
	if logger.DebugEnabled() {
		logger.Debug("civisibility.ImpactedTests: No line info found")
	}
}

// logLineIntersection emits the existing line-intersection debug log without allocating when
// debug logs are disabled.
func logLineIntersection(testName string) {
	if logger.DebugEnabled() {
		logger.Debug("civisibility.ImpactedTests: Intersecting lines. Marking test %s as modified.", testName)
	}
}

// bitmapIntersectsLineRange reports whether bitmap has any active bit in the inclusive
// 1-indexed line range. It uses the same MSB-first byte layout as filebitmap.Set.
func bitmapIntersectsLineRange(bitmap []byte, startLine int, endLine int) bool {
	if startLine == 0 || endLine == 0 {
		return false
	}
	validateImpactedLineRange(startLine, endLine)
	if len(bitmap) == 0 {
		return false
	}

	maxLine := len(bitmap) * 8
	if startLine > maxLine {
		return false
	}
	if endLine > maxLine {
		endLine = maxLine
	}

	startBit := startLine - 1
	endBit := endLine - 1
	startByte := startBit / 8
	endByte := endBit / 8
	startOffset := startBit % 8
	endOffset := endBit % 8

	firstMask := byte(0xff >> startOffset)
	lastMask := byte(0xff << (7 - endOffset))
	if startByte == endByte {
		return bitmap[startByte]&(firstMask&lastMask) != 0
	}

	if bitmap[startByte]&firstMask != 0 {
		return true
	}
	for i := startByte + 1; i < endByte; i++ {
		if bitmap[i] != 0 {
			return true
		}
	}
	return bitmap[endByte]&lastMask != 0
}

// validateImpactedLineRange preserves the range validation behavior previously
// provided by filebitmap.FromActiveRange for non-zero line ranges.
func validateImpactedLineRange(startLine int, endLine int) {
	if startLine != 0 && endLine != 0 && (startLine <= 0 || endLine < startLine) {
		panic("Invalid range")
	}
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
