// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package coverage

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/filebitmap"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	// testFramework represents the name of the testing framework.
	testFramework = "golang.org/pkg/testing"
)

type (
	// TestCoverage is the interface for collecting test coverage.
	TestCoverage interface {
		// CollectCoverageBeforeTestExecution collects coverage before test execution.
		CollectCoverageBeforeTestExecution()
		// CollectCoverageAfterTestExecution collects coverage after test execution.
		CollectCoverageAfterTestExecution()
	}

	// testCoverage holds information about test coverage.
	testCoverage struct {
		sessionID            uint64
		moduleID             uint64
		suiteID              uint64
		testID               uint64
		testFile             string
		preCoverageFilename  string
		postCoverageFilename string
		filesCovered         []coveredFile
	}

	// coverageData holds information about coverage data with their block
	coverageData struct {
		fileName string
		blocks   []coverageBlock
	}

	// coverageBlock holds information about coverage block.
	coverageBlock struct {
		startLine int
		startCol  int
		endLine   int
		endCol    int
		numStmt   int
		count     int
	}

	coveredFile struct {
		name   string
		bitmap []byte
	}

	// BackfillInput configures backend aggregate coverage that can repair the
	// process-local coverage profile after ITR skips have been applied.
	BackfillInput struct {
		BackendCoverage map[string]*filebitmap.FileBitmap
		ActualSkips     int
	}

	// BackfillResult describes one idempotent coverage backfill finalization.
	BackfillResult struct {
		Applied               bool
		Reason                string
		Coverage              float64
		ProfilePath           string
		MatchedFiles          int
		UnmatchedBackendFiles int
		MatchedBlocks         int
		UpdatedBlocks         int
	}

	runtimeCoverageSnapshot struct {
		path      string
		temporary bool
	}
)

var _ TestCoverage = (*testCoverage)(nil)

var (
	coverageStateMu locking.Mutex

	// mode is the coverage mode.
	mode string
	// tearDown is the function to write the coverage counters to the file.
	tearDown func(coverprofile string, gocoverdir string) (string, error)
	// coverageUploadEnabled is true when per-test coverage should be sent to Datadog.
	coverageUploadEnabled bool

	// covWriter is the coverage writer for sending test coverage data to the backend.
	covWriter *coverageWriter

	// runtimeSnapshot is the single runtime coverage snapshot shared by backfill
	// and session coverage calculation.
	runtimeSnapshot *runtimeCoverageSnapshot
	// runtimeSnapshotCleaned prevents duplicate cleanup of the generated temp snapshot.
	runtimeSnapshotCleaned bool
	// backfillInput stores backend aggregate coverage for finalization.
	backfillInput *BackfillInput
	// backfillFinalized records whether FinalizeBackfill has already run.
	backfillFinalized bool
	// backfillResult stores the idempotent finalization result.
	backfillResult BackfillResult

	// temporaryDir is the temporary directory to store coverage files.
	temporaryDir string
	// modulePath is the module path.
	modulePath string
	// moduleDir is the module directory.
	moduleDir string
)

// InitializeCoverage initializes the runtime coverage.
func InitializeCoverage(m *testing.M, uploadEnabled bool) {
	log.Debug("civisibility.cov: initializing runtime coverage")
	testDep, err := getTestDepsCoverage(m)
	if err != nil {
		log.Debug("civisibility.cov: error initializing runtime coverage: %s", err.Error())
		return
	}
	if testDep == nil {
		log.Debug("civisibility.cov: runtime coverage dependencies are unavailable")
		return
	}

	// initializing runtime coverage
	tMode, tDown, _ := testDep.InitRuntimeCoverage()
	mode = tMode
	tearDown = func(coverprofile string, gocoverdir string) (string, error) {
		// writing the coverage counters to the file
		return tDown(coverprofile, gocoverdir)
	}
	coverageUploadEnabled = uploadEnabled
	runtimeSnapshot = nil
	runtimeSnapshotCleaned = false
	backfillInput = nil
	backfillFinalized = false
	backfillResult = BackfillResult{}

	initializeModuleInfo()

	// if we cannot collect we bailout early
	if !CanCollectPerTestCoverage() {
		return
	}

	// initializing coverage writer
	covWriter = newCoverageWriter()
	integrations.PushCiVisibilityCloseAction(func() {
		covWriter.stop()
	})

	// create a temporary directory to store coverage files
	temporaryDir, err = os.MkdirTemp("", "coverage")
	if err != nil {
		log.Debug("civisibility.cov: error creating temporary directory: %s", err.Error())
	} else {
		log.Debug("civisibility.cov: temporary coverage directory created: %s", temporaryDir)
	}
	integrations.PushCiVisibilityCloseAction(func() {
		_ = os.RemoveAll(temporaryDir)
	})

}

func initializeModuleInfo() {
	stdOut, err := exec.Command("go", "list", "-f", "{{.Module.Path}};{{.Module.Dir}}").CombinedOutput()
	if err != nil {
		log.Debug("civisibility.cov: error getting module path and module dir: %s", err.Error())
	} else {
		parts := strings.Split(string(stdOut), ";")
		if len(parts) == 2 {
			modulePath = strings.TrimSpace(parts[0])
			moduleDir = strings.TrimSpace(parts[1])
		}
	}
}

// CanCollect returns whether coverage can be collected.
func CanCollect() bool {
	return mode == "count" || mode == "atomic"
}

// CanComputeCoverageProfile returns whether a runtime coverprofile can be produced.
func CanComputeCoverageProfile() bool {
	return tearDown != nil && (mode == "set" || mode == "count" || mode == "atomic")
}

// CanCollectPerTestCoverage returns whether per-test coverage upload can run.
func CanCollectPerTestCoverage() bool {
	return coverageUploadEnabled && CanCollect()
}

// GetCoverage returns the total coverage percentage for the test package
func GetCoverage() float64 {
	if !CanComputeCoverageProfile() {
		return 0
	}

	snapshot, err := RuntimeCoverageSnapshot()
	if err != nil {
		log.Debug("civisibility.cov: error getting coverage file: %s", err.Error())
		return 0
	}

	totalStatements, coveredStatements, err := getCoverageStatementsInfo(snapshot.path)
	if err != nil {
		log.Debug("civisibility.cov: error parsing coverage file: %s", err.Error())
	}

	if totalStatements == 0 {
		return 0
	}

	return float64(coveredStatements) / float64(totalStatements)
}

// RuntimeCoverageSnapshot returns the single coverage profile snapshot used for
// final coverage calculation and ITR backfill.
func RuntimeCoverageSnapshot() (*runtimeCoverageSnapshot, error) {
	coverageStateMu.Lock()
	defer coverageStateMu.Unlock()
	return runtimeCoverageSnapshotLocked()
}

func runtimeCoverageSnapshotLocked() (*runtimeCoverageSnapshot, error) {
	if runtimeSnapshot != nil {
		return runtimeSnapshot, nil
	}
	if !CanComputeCoverageProfile() {
		return nil, errors.New("runtime coverage unavailable")
	}

	if coverProfile := currentCoverProfilePath(); coverProfile != "" {
		if _, err := os.Stat(coverProfile); err == nil {
			runtimeSnapshot = &runtimeCoverageSnapshot{path: coverProfile}
			return runtimeSnapshot, nil
		}
	}

	if err := ensureTemporaryDirLocked(); err != nil {
		return nil, err
	}
	coverageFile := filepath.Join(temporaryDir, "global_coverage.out")
	if _, err := tearDown(coverageFile, ""); err != nil {
		return nil, err
	}
	runtimeSnapshot = &runtimeCoverageSnapshot{path: coverageFile, temporary: true}
	return runtimeSnapshot, nil
}

func currentCoverProfilePath() string {
	if coverProfileFlag := flag.Lookup("test.coverprofile"); coverProfileFlag != nil {
		return coverProfileFlag.Value.String()
	}
	return ""
}

func ensureTemporaryDirLocked() error {
	if temporaryDir != "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "coverage")
	if err != nil {
		return err
	}
	temporaryDir = dir
	integrations.PushCiVisibilityCloseAction(func() {
		_ = os.RemoveAll(dir)
	})
	return nil
}

// ConfigureBackfill stores backend aggregate coverage for the finalizer.
func ConfigureBackfill(input BackfillInput) {
	coverageStateMu.Lock()
	defer coverageStateMu.Unlock()

	copiedCoverage := make(map[string]*filebitmap.FileBitmap, len(input.BackendCoverage))
	maps.Copy(copiedCoverage, input.BackendCoverage)
	backfillInput = &BackfillInput{
		BackendCoverage: copiedCoverage,
		ActualSkips:     input.ActualSkips,
	}
	backfillFinalized = false
	backfillResult = BackfillResult{}
}

// PreflightBackfill validates that runtime coverage can later be backfilled.
// Path matching is deferred to FinalizeBackfill because producing a coverage
// profile before testing.M.Run completes mutates Go's runtime coverage state.
func PreflightBackfill(input BackfillInput) BackfillResult {
	coverageStateMu.Lock()
	defer coverageStateMu.Unlock()

	if len(input.BackendCoverage) == 0 {
		return BackfillResult{Reason: "backfill not configured"}
	}
	if !CanComputeCoverageProfile() {
		return BackfillResult{Reason: "runtime coverage unavailable"}
	}
	if !CanCollect() {
		return BackfillResult{Reason: "coverage mode unsupported"}
	}
	result := validateBackendCoverageSourceFiles(input.BackendCoverage)
	if result.unmatchedBackendFiles > 0 {
		return BackfillResult{
			Reason:                "coverage paths unmatched",
			MatchedFiles:          result.matchedFiles,
			UnmatchedBackendFiles: result.unmatchedBackendFiles,
		}
	}
	if result.matchedFiles == 0 {
		return BackfillResult{Reason: "coverage paths unmatched"}
	}
	return BackfillResult{}
}

// FinalizeBackfill applies backend coverage to the runtime snapshot once.
func FinalizeBackfill() BackfillResult {
	coverageStateMu.Lock()
	defer coverageStateMu.Unlock()

	if backfillFinalized {
		return backfillResult
	}
	backfillFinalized = true

	if backfillInput == nil || len(backfillInput.BackendCoverage) == 0 {
		backfillResult = BackfillResult{Reason: "backfill not configured"}
		return backfillResult
	}
	if backfillInput.ActualSkips <= 0 {
		backfillResult = BackfillResult{Reason: "no actual itr skips"}
		return backfillResult
	}
	if !CanComputeCoverageProfile() {
		backfillResult = BackfillResult{Reason: "runtime coverage unavailable"}
		return backfillResult
	}
	if !CanCollect() {
		backfillResult = BackfillResult{Reason: "coverage mode unsupported"}
		return backfillResult
	}

	snapshot, err := runtimeCoverageSnapshotLocked()
	if err != nil {
		backfillResult = BackfillResult{Reason: "runtime coverage unavailable"}
		return backfillResult
	}

	profile, err := parseOrderedCoverProfile(snapshot.path)
	if err != nil {
		backfillResult = BackfillResult{Reason: "coverage profile invalid", ProfilePath: snapshot.path}
		return backfillResult
	}
	result := profile.applyBackfill(backfillInput.BackendCoverage)
	if result.unmatchedBackendFiles > 0 {
		backfillResult = BackfillResult{
			Reason:                "coverage paths unmatched",
			ProfilePath:           snapshot.path,
			MatchedFiles:          result.matchedFiles,
			UnmatchedBackendFiles: result.unmatchedBackendFiles,
			MatchedBlocks:         result.matchedBlocks,
		}
		return backfillResult
	}
	if result.matchedBlocks == 0 {
		backfillResult = BackfillResult{Reason: "coverage paths unmatched", ProfilePath: snapshot.path, MatchedFiles: result.matchedFiles}
		return backfillResult
	}
	if result.updatedBlocks > 0 {
		if err := profile.writeAtomic(snapshot.path); err != nil {
			backfillResult = BackfillResult{Reason: "runtime coverage unavailable", ProfilePath: snapshot.path}
			return backfillResult
		}
	}

	coverage := 0.0
	if result.totalStatements > 0 {
		coverage = float64(result.coveredStmts) / float64(result.totalStatements)
	}
	backfillResult = BackfillResult{
		Applied:               result.updatedBlocks > 0,
		Coverage:              coverage,
		ProfilePath:           snapshot.path,
		MatchedFiles:          result.matchedFiles,
		UnmatchedBackendFiles: result.unmatchedBackendFiles,
		MatchedBlocks:         result.matchedBlocks,
		UpdatedBlocks:         result.updatedBlocks,
	}
	return backfillResult
}

// CleanupRuntimeCoverageSnapshot removes a generated temp snapshot once.
func CleanupRuntimeCoverageSnapshot() {
	coverageStateMu.Lock()
	defer coverageStateMu.Unlock()
	if runtimeSnapshot == nil || !runtimeSnapshot.temporary || runtimeSnapshotCleaned {
		return
	}
	if err := os.Remove(runtimeSnapshot.path); err != nil {
		log.Debug("civisibility.cov: error removing coverage file: %s", err.Error())
	}
	runtimeSnapshotCleaned = true
}

// ResetForTesting clears package globals used by coverage tests.
func ResetForTesting() {
	coverageStateMu.Lock()
	defer coverageStateMu.Unlock()

	mode = ""
	tearDown = nil
	coverageUploadEnabled = false
	covWriter = nil
	runtimeSnapshot = nil
	runtimeSnapshotCleaned = false
	backfillInput = nil
	backfillFinalized = false
	backfillResult = BackfillResult{}
	temporaryDir = ""
	modulePath = ""
	moduleDir = ""
}

// NewTestCoverage creates a new test coverage.
func NewTestCoverage(sessionID, moduleID, suiteID, testID uint64, testFile string) TestCoverage {
	testFile = utils.GetRelativePathFromCITagsSourceRoot(testFile)
	return &testCoverage{
		sessionID: sessionID,
		moduleID:  moduleID,
		suiteID:   suiteID,
		testID:    testID,
		testFile:  testFile,
	}
}

// CollectCoverageBeforeTestExecution collects coverage before test execution.
func (t *testCoverage) CollectCoverageBeforeTestExecution() {
	if !CanCollectPerTestCoverage() {
		return
	}

	t.preCoverageFilename = filepath.Join(temporaryDir, fmt.Sprintf("%d-%d-%d-pre.out", t.moduleID, t.suiteID, t.testID))
	_, err := tearDown(t.preCoverageFilename, "")
	if err != nil {
		log.Debug("civisibility.cov: error getting coverage file: %s", err.Error())
		telemetry.CodeCoverageErrors()
	} else {
		telemetry.CodeCoverageStarted(testFramework, telemetry.DefaultCoverageLibraryType)
	}
}

// CollectCoverageAfterTestExecution collects coverage after test execution.
func (t *testCoverage) CollectCoverageAfterTestExecution() {
	if !CanCollectPerTestCoverage() {
		return
	}

	if t.getCoverageData() != nil {
		return
	}

	var pChannel = make(chan struct{})
	integrations.PushCiVisibilityCloseAction(func() {
		<-pChannel
	})
	go func() {
		t.processCoverageData()
		pChannel <- struct{}{}
	}()
}

// getCoverageData gets the coverage data.
func (t *testCoverage) getCoverageData() error {
	if !CanCollectPerTestCoverage() {
		return nil
	}

	t.postCoverageFilename = filepath.Join(temporaryDir, fmt.Sprintf("%d-%d-%d-post.out", t.moduleID, t.suiteID, t.testID))
	_, err := tearDown(t.postCoverageFilename, "")
	if err != nil {
		log.Debug("civisibility.cov: error getting coverage file: %s", err.Error())
		telemetry.CodeCoverageErrors()
	}

	return err
}

// processCoverageData processes the coverage data.
func (t *testCoverage) processCoverageData() {
	if t.preCoverageFilename == "" ||
		t.postCoverageFilename == "" ||
		t.preCoverageFilename == t.postCoverageFilename {
		log.Debug("civisibility.cov: no coverage data to process")
		telemetry.CodeCoverageErrors()
		return
	}
	preCoverage, err := parseCoverProfile(t.preCoverageFilename)
	if err != nil {
		log.Debug("civisibility.cov: error parsing pre-coverage file: %s", err.Error())
		telemetry.CodeCoverageErrors()
		return
	}
	postCoverage, err := parseCoverProfile(t.postCoverageFilename)
	if err != nil {
		log.Debug("civisibility.cov: error parsing post-coverage file: %s", err.Error())
		telemetry.CodeCoverageErrors()
		return
	}

	t.filesCovered = getFilesCovered(t.testFile, preCoverage, postCoverage)
	telemetry.CodeCoverageFinished(testFramework, telemetry.DefaultCoverageLibraryType)
	if len(t.filesCovered) == 0 {
		telemetry.CodeCoverageIsEmpty()
	}

	covWriter.add(t)

	err = os.Remove(t.preCoverageFilename)
	if err != nil {
		log.Debug("civisibility.cov: error removing pre-coverage file: %s", err.Error())
	}

	err = os.Remove(t.postCoverageFilename)
	if err != nil {
		log.Debug("civisibility.cov: error removing post-coverage file: %s", err.Error())
	}
}

// parseCoverProfile parses the coverage profile data and returns the coverage data for each file
func parseCoverProfile(filename string) (map[string][]coverageBlock, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	coverageData := make(map[string][]coverageBlock)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()

		// Skip the header
		if strings.HasPrefix(line, "mode:") {
			continue
		}

		fileName, blockInfo, err := splitCoverageProfileLine(line)
		if err != nil {
			continue
		}

		// Split the block info by space
		infoParts := strings.Fields(blockInfo)
		if len(infoParts) < 3 {
			continue
		}

		// Extract start and end positions (line.column)
		startEnd := strings.Split(infoParts[0], ",")
		if len(startEnd) < 2 {
			continue
		}

		startPos := strings.Split(startEnd[0], ".")
		endPos := strings.Split(startEnd[1], ".")

		if len(startPos) < 2 || len(endPos) < 2 {
			continue
		}

		// Convert to integers
		startLine, err1 := strconv.Atoi(startPos[0])
		startCol, err2 := strconv.Atoi(startPos[1])
		endLine, err3 := strconv.Atoi(endPos[0])
		endCol, err4 := strconv.Atoi(endPos[1])
		numStmt, err5 := strconv.Atoi(infoParts[1])
		count, err6 := strconv.Atoi(infoParts[2])

		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || err6 != nil {
			continue
		}

		block := coverageBlock{
			startLine: startLine,
			startCol:  startCol,
			endLine:   endLine,
			endCol:    endCol,
			numStmt:   numStmt,
			count:     count,
		}

		coverageData[fileName] = append(coverageData[fileName], block)
	}

	return coverageData, scanner.Err()
}

// getFilesCovered subtracts the before profile from the after profile and returns the files covered.
func getFilesCovered(testFile string, before, after map[string][]coverageBlock) []coveredFile {
	coveredByName := map[string]*filebitmap.FileBitmap{}

	addCoveredRange := func(fileName string, block coverageBlock) {
		name := getRelativePathFromCITagsSourceRootForCoverage(fileName)
		bitmap := coveredByName[name]
		if bitmap == nil || bitmap.BitCount() < block.endLine {
			next := filebitmap.FromLineCount(block.endLine)
			if bitmap != nil {
				next = filebitmap.Or(next, bitmap, true)
			}
			bitmap = next
			coveredByName[name] = bitmap
		}
		for line := block.startLine; line <= block.endLine; line++ {
			bitmap.Set(line)
		}
	}

	for fileName, afterBlocks := range after {
		if beforeBlocks, found := before[fileName]; found {
			// Create a map for quick lookup by (startLine, startCol, endLine, endCol)
			beforeMap := make(map[string]coverageBlock)
			for _, block := range beforeBlocks {
				key := fmt.Sprintf("%d.%d-%d.%d", block.startLine, block.startCol, block.endLine, block.endCol)
				beforeMap[key] = block
			}

			// Subtract each block in after from the corresponding block in before
			for _, afterBlock := range afterBlocks {
				key := fmt.Sprintf("%d.%d-%d.%d", afterBlock.startLine, afterBlock.startCol, afterBlock.endLine, afterBlock.endCol)
				if beforeBlock, found := beforeMap[key]; found {
					// Subtract hit counts
					diffCount := afterBlock.count - beforeBlock.count
					if diffCount > 0 {
						addCoveredRange(fileName, afterBlock)
					}
				} else if afterBlock.count > 0 {
					// If there's no matching block in before, add the whole block from after
					addCoveredRange(fileName, afterBlock)
				}
			}
		} else {
			// If there's no before profile for this file, add the entire after profile
			for _, afterBlock := range afterBlocks {
				if afterBlock.count > 0 {
					addCoveredRange(fileName, afterBlock)
				}
			}
		}
	}

	names := make([]string, 0, len(coveredByName))
	for name := range coveredByName {
		names = append(names, name)
	}
	slices.Sort(names)
	result := make([]coveredFile, 0, 1+len(names))
	result = append(result, coveredFile{name: testFile})
	for _, name := range names {
		result = append(result, coveredFile{name: name, bitmap: coveredByName[name].ToArray()})
	}
	return result
}

// getRelativePathFromCITagsSourceRootForCoverage returns the relative path from the CI tags source root for coverage
// by converting a module path to a module directory.
func getRelativePathFromCITagsSourceRootForCoverage(filePath string) string {
	return utils.GetRelativePathFromCITagsSourceRoot(strings.ReplaceAll(filePath, modulePath, moduleDir))
}

// getCoverageStatementsInfo parses the coverage profile data and returns the total statements and covered statements
func getCoverageStatementsInfo(filename string) (totalStatements, coveredStatements int, err error) {
	file, err := os.Open(filename)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()

		// Skip the header
		if strings.HasPrefix(line, "mode:") {
			continue
		}

		_, blockInfo, err := splitCoverageProfileLine(line)
		if err != nil {
			continue
		}

		// Split the block info by space
		infoParts := strings.Fields(blockInfo)
		if len(infoParts) < 3 {
			continue
		}

		// Convert the number of statements and hit count
		numStmt, err1 := strconv.Atoi(infoParts[1])
		count, err2 := strconv.Atoi(infoParts[2])

		// Skip if any conversion failed
		if err1 != nil || err2 != nil {
			continue
		}

		// Update total statements
		totalStatements += numStmt

		// Update covered statements if the hit count is greater than 0
		if count > 0 {
			coveredStatements += numStmt
		}
	}

	return totalStatements, coveredStatements, scanner.Err()
}
