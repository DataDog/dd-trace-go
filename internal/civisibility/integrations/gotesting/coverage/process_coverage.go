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

// ProcessTestCoverageFile is a serializable per-test coverage delta produced
// by an isolated test process.
type ProcessTestCoverageFile struct {
	Name   string `json:"name"`
	Bitmap []byte `json:"bitmap,omitempty"`
}

// ProcessTestCoverage owns one runtime-coverage delta.
type ProcessTestCoverage struct {
	testFile     string
	coverage     *testCoverage
	temporaryDir string
	files        []ProcessTestCoverageFile
	active       bool
}

var (
	processCoverageMu           sync.Mutex
	processAggregateCoverage    *orderedCoverProfile
	processAggregateCoverageErr error
)

// BeginProcessTestCoverage starts a coverage delta. Because Go's runtime
// coverage counters are process-global, callers must serialize covered test
// bodies or discard deltas whose bodies overlap.
func BeginProcessTestCoverage(testFile string) *ProcessTestCoverage {
	if !CanCollect() {
		return nil
	}
	collector := &ProcessTestCoverage{testFile: testFile}
	if !collector.resume() {
		return nil
	}
	return collector
}

// Pause finishes the active coverage segment. A later Resume starts a new
// baseline so coverage collected while the test is suspended is excluded.
func (c *ProcessTestCoverage) Pause() {
	if c == nil || !c.active {
		return
	}
	c.active = false
	processCoverageMu.Lock()
	defer processCoverageMu.Unlock()
	defer func() { _ = os.RemoveAll(c.temporaryDir) }()
	if err := c.coverage.collectCoverageAfterTestExecution(); err != nil || !c.coverage.loadCoverageData() {
		return
	}
	c.files = mergeProcessTestCoverageFiles(c.files, c.coverage.filesCovered)
}

// Resume starts another coverage segment after a suspended test resumes.
func (c *ProcessTestCoverage) Resume() {
	if c != nil {
		c.resume()
	}
}

func (c *ProcessTestCoverage) resume() bool {
	if c.active {
		return true
	}
	processCoverageMu.Lock()
	defer processCoverageMu.Unlock()
	temporaryDir, err := os.MkdirTemp("", "dd-process-coverage-*")
	if err != nil {
		log.Debug("civisibility.cov: error creating process coverage directory: %s", err.Error())
		telemetry.CodeCoverageErrors()
		return false
	}
	collector := &testCoverage{
		testFile:             utils.GetRelativePathFromCITagsSourceRoot(c.testFile),
		preCoverageFilename:  filepath.Join(temporaryDir, "pre.out"),
		postCoverageFilename: filepath.Join(temporaryDir, "post.out"),
	}
	if err := collector.collectCoverageBeforeTestExecution(); err != nil {
		log.Debug("civisibility.cov: error getting process coverage file: %s", err.Error())
		telemetry.CodeCoverageErrors()
		_ = os.RemoveAll(temporaryDir)
		return false
	}
	c.coverage = collector
	c.temporaryDir = temporaryDir
	c.active = true
	return true
}

// Finish returns the coverage delta and releases the collector resources.
func (c *ProcessTestCoverage) Finish() []ProcessTestCoverageFile {
	if c == nil {
		return nil
	}
	c.Pause()
	files := c.files
	c.files = nil
	return files
}

func mergeProcessTestCoverageFiles(dst []ProcessTestCoverageFile, src []coveredFile) []ProcessTestCoverageFile {
	indexes := make(map[string]int, len(dst)+len(src))
	for idx := range dst {
		indexes[dst[idx].Name] = idx
	}
	for _, file := range src {
		idx, found := indexes[file.name]
		if !found {
			indexes[file.name] = len(dst)
			dst = append(dst, ProcessTestCoverageFile{Name: file.name, Bitmap: file.bitmap})
			continue
		}
		bitmap := &dst[idx].Bitmap
		if missing := len(file.bitmap) - len(*bitmap); missing > 0 {
			*bitmap = append(*bitmap, make([]byte, missing)...)
		}
		for idx, value := range file.bitmap {
			(*bitmap)[idx] |= value
		}
	}
	return dst
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
	processCoverageMu.Lock()
	defer processCoverageMu.Unlock()
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
	processCoverageMu.Lock()
	defer processCoverageMu.Unlock()
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

// MergeProcessCoverageProfile validates and accumulates an isolated process
// profile. The parent runtime profile is not touched until M.Run has finished;
// taking its snapshot while tests are still running would lose later coverage.
func MergeProcessCoverageProfile(childPath string) error {
	if !CanComputeCoverageProfile() {
		return nil
	}
	processCoverageMu.Lock()
	defer processCoverageMu.Unlock()
	if processAggregateCoverageErr != nil {
		return processAggregateCoverageErr
	}
	child, err := parseOrderedCoverProfile(childPath)
	if err != nil {
		processAggregateCoverageErr = err
		return err
	}
	if processAggregateCoverage == nil {
		processAggregateCoverage = child
		return nil
	}
	if err := mergeProcessCoverageProfiles(processAggregateCoverage, child); err != nil {
		processAggregateCoverageErr = err
		return err
	}
	return nil
}

// FinalizeProcessCoverageProfiles merges all isolated coverage once, after
// testing.M.Run has produced the parent's final profile. This also updates an
// explicit -coverprofile because RuntimeCoverageSnapshot selects that file.
func FinalizeProcessCoverageProfiles() (bool, error) {
	if !CanComputeCoverageProfile() {
		return false, nil
	}
	processCoverageMu.Lock()
	defer processCoverageMu.Unlock()
	if processAggregateCoverageErr != nil {
		return false, processAggregateCoverageErr
	}
	if processAggregateCoverage == nil {
		return false, nil
	}
	parentSnapshot, err := RuntimeCoverageSnapshot()
	if err != nil {
		return false, err
	}
	parent, err := parseOrderedCoverProfile(parentSnapshot.path)
	if err != nil {
		return false, err
	}
	if err := mergeProcessCoverageProfiles(parent, processAggregateCoverage); err != nil {
		return false, err
	}
	if err := parent.writeAtomic(parentSnapshot.path); err != nil {
		return false, err
	}
	processAggregateCoverage = nil
	return true, nil
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
		key := processCoverageBlockKey(line.fileName, line.block)
		block := byBlock[key]
		if block == nil {
			return errors.New("coverage profile blocks differ")
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
