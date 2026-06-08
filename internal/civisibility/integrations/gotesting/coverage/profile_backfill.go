// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/filebitmap"
)

type orderedCoverProfile struct {
	lines []orderedCoverProfileLine
}

type orderedCoverProfileLine struct {
	raw      string
	fileName string
	block    *coverageBlock
}

type profileBackfillResult struct {
	matchedFiles          int
	unmatchedBackendFiles int
	matchedBlocks         int
	updatedBlocks         int
	totalStatements       int
	coveredStmts          int
}

func parseOrderedCoverProfile(filename string) (*orderedCoverProfile, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	profile := &orderedCoverProfile{}
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	headerSeen := false
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		profileLine := orderedCoverProfileLine{raw: line}
		trimmed := strings.TrimSpace(line)
		if lineNumber == 1 {
			if !validCoverageProfileHeader(trimmed) {
				return nil, fmt.Errorf("invalid coverage profile header")
			}
			headerSeen = true
			profile.lines = append(profile.lines, profileLine)
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			profile.lines = append(profile.lines, profileLine)
			continue
		}
		if strings.HasPrefix(trimmed, "mode:") {
			return nil, fmt.Errorf("unexpected coverage profile header on line %d", lineNumber)
		}
		block, fileName, err := parseCoverageLine(line)
		if err != nil {
			return nil, fmt.Errorf("invalid coverage profile line %d: %w", lineNumber, err)
		}
		profileLine.fileName = fileName
		profileLine.block = &block
		profile.lines = append(profile.lines, profileLine)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !headerSeen {
		return nil, fmt.Errorf("missing coverage profile header")
	}
	return profile, nil
}

func validCoverageProfileHeader(line string) bool {
	switch line {
	case "mode: set", "mode: count", "mode: atomic":
		return true
	default:
		return false
	}
}

func parseCoverageLine(line string) (coverageBlock, string, error) {
	fileName, blockInfo, err := splitCoverageProfileLine(line)
	if err != nil {
		return coverageBlock{}, "", err
	}
	infoParts := strings.Fields(blockInfo)
	if len(infoParts) != 3 {
		return coverageBlock{}, "", fmt.Errorf("expected three block fields")
	}

	startEnd := strings.Split(infoParts[0], ",")
	if len(startEnd) != 2 {
		return coverageBlock{}, "", fmt.Errorf("invalid block range")
	}

	startPos := strings.Split(startEnd[0], ".")
	endPos := strings.Split(startEnd[1], ".")
	if len(startPos) != 2 || len(endPos) != 2 {
		return coverageBlock{}, "", fmt.Errorf("invalid block position")
	}

	startLine, err1 := strconv.Atoi(startPos[0])
	startCol, err2 := strconv.Atoi(startPos[1])
	endLine, err3 := strconv.Atoi(endPos[0])
	endCol, err4 := strconv.Atoi(endPos[1])
	numStmt, err5 := strconv.Atoi(infoParts[1])
	count, err6 := strconv.Atoi(infoParts[2])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || err6 != nil {
		return coverageBlock{}, "", fmt.Errorf("invalid block number")
	}
	if startLine <= 0 || startCol <= 0 || endLine <= 0 || endCol <= 0 || numStmt < 0 || count < 0 {
		return coverageBlock{}, "", fmt.Errorf("invalid negative or non-positive block value")
	}
	if endLine < startLine || (endLine == startLine && endCol < startCol) {
		return coverageBlock{}, "", fmt.Errorf("invalid block ordering")
	}

	return coverageBlock{
		startLine: startLine,
		startCol:  startCol,
		endLine:   endLine,
		endCol:    endCol,
		numStmt:   numStmt,
		count:     count,
	}, fileName, nil
}

func splitCoverageProfileLine(line string) (string, string, error) {
	separator := strings.LastIndex(line, ":")
	if separator <= 0 || strings.TrimSpace(line[:separator]) == "" {
		return "", "", fmt.Errorf("missing file name")
	}
	return line[:separator], line[separator+1:], nil
}

func (p *orderedCoverProfile) applyBackfill(backendCoverage map[string]*filebitmap.FileBitmap) profileBackfillResult {
	result := profileBackfillResult{}
	matchedProfileFiles := map[string]struct{}{}
	backendFilesWithMatchedBlocks := map[string]struct{}{}

	for idx := range p.lines {
		line := &p.lines[idx]
		if line.block == nil {
			continue
		}

		result.totalStatements += line.block.numStmt
		if line.block.count > 0 {
			result.coveredStmts += line.block.numStmt
		}

		backendFile, bitmap, fileMatched := backfillBitmapForProfileFile(line.fileName, backendCoverage)
		if !fileMatched {
			continue
		}
		matchedProfileFiles[line.fileName] = struct{}{}
		if !bitmap.IntersectsLineRange(line.block.startLine, line.block.endLine) {
			continue
		}

		result.matchedBlocks++
		backendFilesWithMatchedBlocks[backendFile] = struct{}{}
		if line.block.count == 0 {
			line.block.count = 1
			result.coveredStmts += line.block.numStmt
			result.updatedBlocks++
		}
	}

	result.matchedFiles = len(matchedProfileFiles)
	result.unmatchedBackendFiles = unmatchedActiveBackendFiles(backendCoverage, backendFilesWithMatchedBlocks)
	return result
}

func unmatchedActiveBackendFiles(backendCoverage map[string]*filebitmap.FileBitmap, backendFilesWithMatchedBlocks map[string]struct{}) int {
	unmatched := 0
	for backendFile, bitmap := range backendCoverage {
		if bitmap == nil || !bitmap.HasActiveBits() {
			continue
		}
		if _, ok := backendFilesWithMatchedBlocks[backendFile]; !ok {
			unmatched++
		}
	}
	return unmatched
}

func backfillBitmapForProfileFile(profileFile string, backendCoverage map[string]*filebitmap.FileBitmap) (string, *filebitmap.FileBitmap, bool) {
	for _, candidate := range coveragePathCandidates(profileFile) {
		if bitmap, ok := backendCoverage[candidate]; ok {
			return candidate, bitmap, true
		}
	}
	return "", nil, false
}

func coveragePathCandidates(profileFile string) []string {
	candidates := make([]string, 0, 4)
	addCandidate := func(value string) {
		value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
		if value == "" {
			return
		}
		value = strings.TrimPrefix(path.Clean(value), "./")
		if value == "." || value == "" {
			return
		}
		if slices.Contains(candidates, value) {
			return
		}
		candidates = append(candidates, value)
	}

	addCandidate(profileFile)
	resolved := utils.ResolveSourceFilePathFromCITags(profileFile)
	addCandidate(resolved.RelativePath)
	if modulePath != "" {
		if suffix, ok := strings.CutPrefix(strings.ReplaceAll(profileFile, "\\", "/"), strings.TrimSuffix(modulePath, "/")+"/"); ok {
			addCandidate(suffix)
			if moduleRepoPrefix := moduleRepositoryRelativePrefix(modulePath); moduleRepoPrefix != "" {
				addCandidate(moduleRepoPrefix + "/" + suffix)
			}
		}
	}
	if modulePath != "" && moduleDir != "" {
		if suffix, ok := strings.CutPrefix(strings.ReplaceAll(profileFile, "\\", "/"), strings.TrimSuffix(modulePath, "/")); ok {
			moduleFile := filepath.Join(moduleDir, filepath.FromSlash(strings.TrimPrefix(suffix, "/")))
			addCandidate(moduleFile)
			resolvedModuleFile := utils.ResolveSourceFilePathFromCITags(moduleFile)
			addCandidate(resolvedModuleFile.RelativePath)
		}
	}
	if moduleDir != "" {
		if rel, err := filepath.Rel(moduleDir, profileFile); err == nil {
			addCandidate(rel)
		}
	}
	return candidates
}

func moduleRepositoryRelativePrefix(module string) string {
	module = strings.Trim(path.Clean(strings.ReplaceAll(module, "\\", "/")), "/")
	if module == "." || module == "" {
		return ""
	}
	segments := strings.Split(module, "/")
	for idx, segment := range segments {
		if !isSemanticImportVersionSegment(segment) {
			continue
		}
		if idx+1 >= len(segments) {
			return ""
		}
		return strings.Join(segments[idx+1:], "/")
	}
	return ""
}

func isSemanticImportVersionSegment(segment string) bool {
	if len(segment) < 2 || segment[0] != 'v' || segment[1] < '2' || segment[1] > '9' {
		return false
	}
	for _, char := range segment[2:] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func (p *orderedCoverProfile) writeAtomic(filename string) error {
	tmp, err := os.CreateTemp(filepath.Dir(filename), filepath.Base(filename)+".tmp.")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpName)
		}
	}()

	writer := bufio.NewWriter(tmp)
	for _, line := range p.lines {
		if line.block == nil {
			if _, err := fmt.Fprintln(writer, line.raw); err != nil {
				_ = tmp.Close()
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(
			writer,
			"%s:%d.%d,%d.%d %d %d\n",
			line.fileName,
			line.block.startLine,
			line.block.startCol,
			line.block.endLine,
			line.block.endCol,
			line.block.numStmt,
			line.block.count,
		); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filename); err != nil {
		return err
	}
	removeTmp = false
	return nil
}
