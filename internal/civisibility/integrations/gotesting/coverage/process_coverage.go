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
	"path/filepath"
	"strings"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// ProcessTestCoverageFile is the serializable per-test coverage produced by an
// isolated first-attempt process.
type ProcessTestCoverageFile struct {
	Name   string `json:"name"`
	Bitmap []byte `json:"bitmap,omitempty"`
}

// ProcessTestCoverage owns one runtime-coverage delta. Callers must serialize
// covered test bodies because the runtime counters are process-global.
type ProcessTestCoverage struct {
	coverage     *testCoverage
	temporaryDir string
	active       bool
}

var processTestCoverageMu sync.Mutex

// BeginProcessTestCoverage starts a coverage delta when count-based runtime
// coverage is available. The caller must serialize the covered test body.
func BeginProcessTestCoverage(testFile string) *ProcessTestCoverage {
	if !CanCollect() {
		return nil
	}
	processTestCoverageMu.Lock()
	defer processTestCoverageMu.Unlock()
	temporaryDir, err := os.MkdirTemp("", "dd-process-coverage-*")
	if err != nil {
		log.Debug("civisibility.cov: error creating process coverage directory: %s", err.Error())
		telemetry.CodeCoverageErrors()
		return nil
	}
	collector := &testCoverage{
		testFile:             utils.GetRelativePathFromCITagsSourceRoot(testFile),
		preCoverageFilename:  filepath.Join(temporaryDir, "pre.out"),
		postCoverageFilename: filepath.Join(temporaryDir, "post.out"),
	}
	if err := collector.collectCoverageBeforeTestExecution(); err != nil {
		log.Debug("civisibility.cov: error getting process coverage file: %s", err.Error())
		telemetry.CodeCoverageErrors()
		_ = os.RemoveAll(temporaryDir)
		return nil
	}
	return &ProcessTestCoverage{coverage: collector, temporaryDir: temporaryDir, active: true}
}

// Finish returns the coverage delta and releases the collector resources.
func (c *ProcessTestCoverage) Finish() []ProcessTestCoverageFile {
	if c == nil || !c.active {
		return nil
	}
	c.active = false
	processTestCoverageMu.Lock()
	defer processTestCoverageMu.Unlock()
	defer func() { _ = os.RemoveAll(c.temporaryDir) }()
	if err := c.coverage.collectCoverageAfterTestExecution(); err != nil {
		return nil
	}
	if !c.coverage.loadCoverageData() {
		return nil
	}
	files := make([]ProcessTestCoverageFile, 0, len(c.coverage.filesCovered))
	for _, file := range c.coverage.filesCovered {
		files = append(files, ProcessTestCoverageFile{Name: file.name, Bitmap: file.bitmap})
	}
	return files
}

// ProcessCoverageProfile owns the aggregate coverage snapshot captured before
// an isolated workload starts.
type ProcessCoverageProfile struct {
	before *orderedCoverProfile
}

// BeginProcessCoverageProfile captures the aggregate coverage present before
// an isolated workload starts.
func BeginProcessCoverageProfile() (*ProcessCoverageProfile, error) {
	if !CanComputeCoverageProfile() {
		return nil, nil
	}
	processTestCoverageMu.Lock()
	defer processTestCoverageMu.Unlock()
	before, err := snapshotProcessCoverageProfile()
	if err != nil {
		return nil, err
	}
	return &ProcessCoverageProfile{before: before}, nil
}

// WriteDelta writes only coverage added since BeginProcessCoverageProfile.
func (p *ProcessCoverageProfile) WriteDelta(path string) error {
	if p == nil {
		return nil
	}
	processTestCoverageMu.Lock()
	defer processTestCoverageMu.Unlock()
	after, err := snapshotProcessCoverageProfile()
	if err != nil {
		return err
	}
	if err := subtractProcessCoverageProfiles(p.before, after); err != nil {
		return err
	}
	return after.writeAtomic(path)
}

func snapshotProcessCoverageProfile() (*orderedCoverProfile, error) {
	temporaryDir, err := os.MkdirTemp("", "dd-process-profile-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(temporaryDir) }()
	path := filepath.Join(temporaryDir, "coverage.out")
	if _, err := tearDown(path, ""); err != nil {
		return nil, err
	}
	return parseOrderedCoverProfile(path)
}

func subtractProcessCoverageProfiles(before, after *orderedCoverProfile) error {
	if before == nil || after == nil {
		return errors.New("coverage profile missing")
	}
	if len(before.lines) == 0 || len(after.lines) == 0 || strings.TrimSpace(before.lines[0].raw) != strings.TrimSpace(after.lines[0].raw) {
		return errors.New("coverage profile modes differ")
	}
	mode := strings.TrimSpace(strings.TrimPrefix(after.lines[0].raw, "mode:"))
	beforeByBlock := make(map[string]int)
	for idx := range before.lines {
		line := &before.lines[idx]
		if line.block != nil {
			beforeByBlock[processCoverageBlockKey(line.fileName, line.block)] = line.block.count
		}
	}
	for idx := range after.lines {
		line := &after.lines[idx]
		if line.block == nil {
			continue
		}
		beforeCount := beforeByBlock[processCoverageBlockKey(line.fileName, line.block)]
		if mode == "set" {
			if line.block.count <= beforeCount {
				line.block.count = 0
			}
		} else {
			line.block.count = max(line.block.count-beforeCount, 0)
		}
	}
	return nil
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
		covered = append(covered, coveredFile{name: file.Name, bitmap: file.Bitmap})
	}
	telemetry.CodeCoverageStarted(testFramework, telemetry.DefaultCoverageLibraryType)
	(&testCoverage{sessionID: sessionID, suiteID: suiteID, testID: testID, filesCovered: covered}).submitCoverageData()
}
