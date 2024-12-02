// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package coverage

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/telemetry"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
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
		filesCovered         []string
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
)

var _ TestCoverage = (*testCoverage)(nil)

var (
	// mode is the coverage mode.
	mode string
	// tearDown is the function to write the coverage counters to the file.
	tearDown func(coverprofile string, gocoverdir string) (string, error)

	// tempFile is the temp file to store coverage messages that we don't want to print to stdout.
	tempFile *os.File

	// covWriter is the coverage writer for sending test coverage data to the backend.
	covWriter *coverageWriter

	// temporaryDir is the temporary directory to store coverage files.
	temporaryDir string
	// modulePath is the module path.
	modulePath string
	// moduleDir is the module directory.
	moduleDir string
)

// InitializeCoverage initializes the runtime coverage.
func InitializeCoverage(m *testing.M) {
	log.Debug("civisibility.coverage: initializing runtime coverage")
	testDep, err := getTestDepsCoverage(m)
	if err != nil || testDep == nil {
		log.Debug("civisibility.coverage: error initializing runtime coverage: %v", err)
		return
	}

	// initializing runtime coverage
	tMode, tDown, _ := testDep.InitRuntimeCoverage()
	mode = tMode
	tearDown = func(coverprofile string, gocoverdir string) (string, error) {
		// redirecting stdout to a temp file to avoid printing coverage messages to stdout
		stdout := os.Stdout
		os.Stdout = tempFile
		defer func() { os.Stdout = stdout }()
		// writing the coverage counters to the file
		return tDown(coverprofile, gocoverdir)
	}

	// if we cannot collect we bailout early
	if !CanCollect() {
		return
	}

	// creating a temp file to store coverage messages that we don't want to print to stdout
	tempFile, _ = os.CreateTemp("", "coverage")

	// initializing coverage writer
	covWriter = newCoverageWriter()
	integrations.PushCiVisibilityCloseAction(func() {
		covWriter.stop()
	})

	// create a temporary directory to store coverage files
	temporaryDir, err = os.MkdirTemp("", "coverage")
	if err != nil {
		log.Debug("civisibility.coverage: error creating temporary directory: %v", err)
	} else {
		log.Debug("civisibility.coverage: temporary coverage directory created: %s", temporaryDir)
	}
	integrations.PushCiVisibilityCloseAction(func() {
		_ = os.RemoveAll(temporaryDir)
	})

	// executing go list -f '{{.Module.Path}};{{.Module.Dir}}' to get the module path and module dir
	stdOut, err := exec.Command("go", "list", "-f", "{{.Module.Path}};{{.Module.Dir}}").CombinedOutput()
	if err != nil {
		log.Debug("civisibility.coverage: error getting module path and module dir: %v", err)
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

// GetCoverage returns the total coverage percentage for the test package
func GetCoverage() float64 {
	if !CanCollect() {
		return 0
	}

	coverageFile := filepath.Join(temporaryDir, "global_coverage.out")
	_, err := tearDown(coverageFile, "")
	if err != nil {
		log.Debug("civisibility.coverage: error getting coverage file: %v", err)
	}

	defer func(cFile string) {
		err = os.Remove(cFile)
		if err != nil {
			log.Debug("civisibility.coverage: error removing coverage file: %v", err)
		}
	}(coverageFile)

	totalStatements, coveredStatements, err := getCoverageStatementsInfo(coverageFile)
	if err != nil {
		log.Debug("civisibility.coverage: error parsing coverage file: %v", err)
	}

	if totalStatements == 0 {
		return 0
	}

	return float64(coveredStatements) / float64(totalStatements)
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
	if !CanCollect() {
		return
	}

	t.preCoverageFilename = filepath.Join(temporaryDir, fmt.Sprintf("%d-%d-%d-pre.out", t.moduleID, t.suiteID, t.testID))
	_, err := tearDown(t.preCoverageFilename, "")
	if err != nil {
		log.Debug("civisibility.coverage: error getting coverage file: %v", err)
		telemetry.CodeCoverageErrors()
	} else {
		telemetry.CodeCoverageStarted(testFramework, telemetry.DefaultCoverageLibraryType)
	}
}

// CollectCoverageAfterTestExecution collects coverage after test execution.
func (t *testCoverage) CollectCoverageAfterTestExecution() {
	if !CanCollect() {
		return
	}

	t.postCoverageFilename = filepath.Join(temporaryDir, fmt.Sprintf("%d-%d-%d-post.out", t.moduleID, t.suiteID, t.testID))
	_, err := tearDown(t.postCoverageFilename, "")
	if err != nil {
		log.Debug("civisibility.coverage: error getting coverage file: %v", err)
		telemetry.CodeCoverageErrors()
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

// processCoverageData processes the coverage data.
func (t *testCoverage) processCoverageData() {
	if t.preCoverageFilename == "" ||
		t.postCoverageFilename == "" ||
		t.preCoverageFilename == t.postCoverageFilename {
		log.Debug("civisibility.coverage: no coverage data to process")
		telemetry.CodeCoverageErrors()
		return
	}
	preCoverage, err := parseCoverProfile(t.preCoverageFilename)
	if err != nil {
		log.Debug("civisibility.coverage: error parsing pre-coverage file: %v", err)
		telemetry.CodeCoverageErrors()
		return
	}
	postCoverage, err := parseCoverProfile(t.postCoverageFilename)
	if err != nil {
		log.Debug("civisibility.coverage: error parsing post-coverage file: %v", err)
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
		log.Debug("civisibility.coverage: error removing pre-coverage file: %v", err)
	}

	err = os.Remove(t.postCoverageFilename)
	if err != nil {
		log.Debug("civisibility.coverage: error removing post-coverage file: %v", err)
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

		// Split the line into the file and block parts
		parts := strings.SplitN(line, ":", 2)
		if len(parts) < 2 {
			continue
		}

		fileName := parts[0]
		blockInfo := parts[1]

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

// getFilesCovered subtracts the before profile from the after profile and returns the files covered
func getFilesCovered(testFile string, before, after map[string][]coverageBlock) []string {
	result := []string{testFile}

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
						result = append(result, getRelativePathFromCITagsSourceRootForCoverage(fileName))
						break
					}
				} else if afterBlock.count > 0 {
					// If there's no matching block in before, add the whole block from after
					result = append(result, getRelativePathFromCITagsSourceRootForCoverage(fileName))
					break
				}
			}
		} else {
			// If there's no before profile for this file, add the entire after profile
			for _, afterBlock := range afterBlocks {
				if afterBlock.count > 0 {
					result = append(result, getRelativePathFromCITagsSourceRootForCoverage(fileName))
					break
				}
			}
		}
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

		// Split the line into the file and block parts
		parts := strings.SplitN(line, ":", 2)
		if len(parts) < 2 {
			continue
		}

		blockInfo := parts[1] // Block data, including line info and statement counts

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
