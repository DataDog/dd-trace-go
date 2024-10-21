// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package coverage

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/tinylib/msgp/msgp"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type (
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

	coverageData struct {
		fileName string
		blocks   []coverageBlock
	}

	coverageBlock struct {
		startLine int
		startCol  int
		endLine   int
		endCol    int
		numStmt   int
		count     int
	}
)

var (
	mode     string
	tearDown func(coverprofile string, gocoverdir string) (string, error)

	tempFile *os.File
)

func InitializeCoverage(m *testing.M) {
	if testDep, err := getTestDepsCoverage(m); err == nil {
		mode, tearDown, _ = testDep.InitRuntimeCoverage()
		tempFile, _ = os.CreateTemp("", "coverage")
	} else {
		log.Debug("Error initializing runtime coverage: %v", err)
	}
}

func canCollect() bool {
	return mode == "count" || mode == "atomic"
}

func setStdOutToTemp() (restore func()) {
	stdout := os.Stdout
	os.Stdout = tempFile
	return func() { os.Stdout = stdout }
}

func NewTestCoverage(sessionID, moduleID, suiteID, testID uint64, testFile string) *testCoverage {
	return &testCoverage{
		sessionID: sessionID,
		moduleID:  moduleID,
		suiteID:   suiteID,
		testID:    testID,
		testFile:  testFile,
	}
}

func (t *testCoverage) CollectCoverageBeforeTestExecution() {
	if !canCollect() {
		return
	}

	restore := setStdOutToTemp()
	t.preCoverageFilename = fmt.Sprintf("%d-%d-%d-pre.out", t.moduleID, t.suiteID, t.testID)
	_, err := tearDown(t.preCoverageFilename, "")
	restore()
	if err != nil {
		log.Debug("Error getting coverage file: %v", err)
	}
}

func (t *testCoverage) CollectCoverageAfterTestExecution() {
	if !canCollect() {
		return
	}

	restore := setStdOutToTemp()
	t.postCoverageFilename = fmt.Sprintf("%d-%d-%d-post.out", t.moduleID, t.suiteID, t.testID)
	_, err := tearDown(t.postCoverageFilename, "")
	restore()
	if err != nil {
		log.Debug("Error getting coverage file: %v", err)
	}

	t.processCoverageData()
}

func (t *testCoverage) processCoverageData() {
	if t.preCoverageFilename == "" ||
		t.postCoverageFilename == "" ||
		t.preCoverageFilename == t.postCoverageFilename {
		log.Debug("No coverage data to process")
		return
	}
	preCoverage, err := parseCoverProfile(t.preCoverageFilename)
	if err != nil {
		log.Debug("Error parsing pre-coverage file: %v", err)
		return
	}
	postCoverage, err := parseCoverProfile(t.postCoverageFilename)
	if err != nil {
		log.Debug("Error parsing post-coverage file: %v", err)
		return
	}

	t.filesCovered = getFilesCovered(t.testFile, preCoverage, postCoverage)

	err = os.Remove(t.preCoverageFilename)
	if err != nil {
		log.Debug("Error removing pre-coverage file: %v", err)
	}

	err = os.Remove(t.postCoverageFilename)
	if err != nil {
		log.Debug("Error removing post-coverage file: %v", err)
	}

	covData := newCiTestCoverageData(t)
	var buf bytes.Buffer
	msgp.Encode(&buf, covData)
	msgp.CopyToJSON(os.Stdout, bytes.NewReader(buf.Bytes()))
	fmt.Println()
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
						result = append(result, fileName)
						break
					}
				} else if afterBlock.count > 0 {
					// If there's no matching block in before, add the whole block from after
					result = append(result, fileName)
					break
				}
			}
		} else {
			// If there's no before profile for this file, add the entire after profile
			for _, afterBlock := range afterBlocks {
				if afterBlock.count > 0 {
					result = append(result, fileName)
					break
				}
			}
		}
	}

	return result
}
