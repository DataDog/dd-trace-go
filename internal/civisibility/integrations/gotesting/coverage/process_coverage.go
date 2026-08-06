// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/telemetry"
)

// ProcessTestCoverageFile is the serializable per-test coverage produced by an
// isolated first-attempt process.
type ProcessTestCoverageFile struct {
	Name   string `json:"name"`
	Bitmap []byte `json:"bitmap,omitempty"`
}

// ProcessTestCoverage owns one serialized runtime-coverage delta.
type ProcessTestCoverage struct {
	testFile string
	before   map[string][]coverageBlock
	active   bool
}

var processTestCoverageMu sync.Mutex

// BeginProcessTestCoverage starts a serialized coverage delta when count-based
// runtime coverage is available.
func BeginProcessTestCoverage(testFile string) *ProcessTestCoverage {
	if !CanCollect() {
		return nil
	}
	processTestCoverageMu.Lock()
	before, err := processRuntimeCoverageSnapshot()
	if err != nil {
		processTestCoverageMu.Unlock()
		return nil
	}
	return &ProcessTestCoverage{
		testFile: utils.GetRelativePathFromCITagsSourceRoot(testFile),
		before:   before,
		active:   true,
	}
}

// Finish returns the coverage delta and releases the serialized collector.
func (c *ProcessTestCoverage) Finish() []ProcessTestCoverageFile {
	if c == nil || !c.active {
		return nil
	}
	c.active = false
	defer processTestCoverageMu.Unlock()
	after, err := processRuntimeCoverageSnapshot()
	if err != nil {
		return nil
	}
	covered := getFilesCovered(c.testFile, c.before, after)
	files := make([]ProcessTestCoverageFile, 0, len(covered))
	for _, file := range covered {
		files = append(files, ProcessTestCoverageFile{Name: file.name, Bitmap: append([]byte(nil), file.bitmap...)})
	}
	return files
}

func processRuntimeCoverageSnapshot() (map[string][]coverageBlock, error) {
	file, err := os.CreateTemp("", "dd-process-coverage-*.out")
	if err != nil {
		return nil, err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	defer func() { _ = os.Remove(path) }()
	if _, err := tearDown(path, ""); err != nil {
		return nil, err
	}
	return parseCoverProfile(path)
}

// WriteProcessCoverageProfile writes the isolated process aggregate profile.
func WriteProcessCoverageProfile(path string) error {
	if !CanComputeCoverageProfile() {
		return nil
	}
	processTestCoverageMu.Lock()
	defer processTestCoverageMu.Unlock()
	_, err := tearDown(path, "")
	return err
}

// MergeProcessCoverageProfile merges an isolated process profile into the
// parent runtime snapshot used for Go and CI Visibility aggregate coverage.
func MergeProcessCoverageProfile(childPath string) error {
	if !CanComputeCoverageProfile() {
		return nil
	}
	processTestCoverageMu.Lock()
	defer processTestCoverageMu.Unlock()
	parentSnapshot, err := RuntimeCoverageSnapshot()
	if err != nil {
		return err
	}
	parent, err := parseOrderedCoverProfile(parentSnapshot.path)
	if err != nil {
		return err
	}
	child, err := parseOrderedCoverProfile(childPath)
	if err != nil {
		return err
	}
	if err := mergeProcessCoverageProfiles(parent, child); err != nil {
		return err
	}
	return parent.writeAtomic(parentSnapshot.path)
}

func mergeProcessCoverageProfiles(parent, child *orderedCoverProfile) error {
	if parent == nil || child == nil {
		return errors.New("coverage profile missing")
	}
	if len(parent.lines) == 0 || len(child.lines) == 0 || strings.TrimSpace(parent.lines[0].raw) != strings.TrimSpace(child.lines[0].raw) {
		return errors.New("coverage profile modes differ")
	}
	mode := strings.TrimSpace(strings.TrimPrefix(parent.lines[0].raw, "mode:"))
	byBlock := make(map[string]*coverageBlock)
	for idx := range parent.lines {
		line := &parent.lines[idx]
		if line.block != nil {
			byBlock[processCoverageBlockKey(line.fileName, line.block)] = line.block
		}
	}
	for idx := range child.lines {
		line := &child.lines[idx]
		if line.block == nil {
			continue
		}
		block := byBlock[processCoverageBlockKey(line.fileName, line.block)]
		if block == nil {
			copyLine := *line
			copyBlock := *line.block
			copyLine.block = &copyBlock
			parent.lines = append(parent.lines, copyLine)
			byBlock[processCoverageBlockKey(line.fileName, line.block)] = copyLine.block
			continue
		}
		if mode == "set" {
			block.count = max(block.count, line.block.count)
		} else if line.block.count > math.MaxInt-block.count {
			block.count = math.MaxInt
		} else {
			block.count += line.block.count
		}
	}
	return nil
}

func processCoverageBlockKey(fileName string, block *coverageBlock) string {
	return fmt.Sprintf("%s:%d.%d,%d.%d %d", fileName, block.startLine, block.startCol, block.endLine, block.endCol, block.numStmt)
}

// SubmitProcessTestCoverage attaches child-produced coverage to the parent test
// event identifiers and queues it on the existing coverage writer.
func SubmitProcessTestCoverage(sessionID, suiteID, testID uint64, files []ProcessTestCoverageFile) {
	if !CanCollectPerTestCoverage() || covWriter == nil || len(files) == 0 {
		return
	}
	covered := make([]coveredFile, 0, len(files))
	for _, file := range files {
		covered = append(covered, coveredFile{name: file.Name, bitmap: append([]byte(nil), file.Bitmap...)})
	}
	telemetry.CodeCoverageStarted(testFramework, telemetry.DefaultCoverageLibraryType)
	covWriter.add(&testCoverage{sessionID: sessionID, suiteID: suiteID, testID: testID, filesCovered: covered})
	telemetry.CodeCoverageFinished(testFramework, telemetry.DefaultCoverageLibraryType)
}
