// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type (
	// testCoverage holds information about test coverage.
	testCoverage struct {
		sessionID            uint64
		moduleID             uint64
		suiteID              uint64
		testID               uint64
		preCoverageFilename  string
		postCoverageFilename string
	}

	fileCoverages []coverageData

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

func initializeCoverage(m *testing.M) {
	tempFile, _ = os.CreateTemp("", "coverage")
	if testDep, err := getTestDepsCoverage(m); err == nil {
		mode, tearDown, _ = testDep.InitRuntimeCoverage()
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

	go func() {
		preCoverage, err := parseCoverageFile(t.preCoverageFilename)
		if err != nil {
			log.Debug("Error parsing pre-coverage file: %v", err)
		}
		postCoverage, err := parseCoverageFile(t.postCoverageFilename)
		if err != nil {
			log.Debug("Error parsing post-coverage file: %v", err)
		}

		fmt.Printf("Files in coverage: %v | %v\n %v", len(preCoverage), len(postCoverage), postCoverage)
	}()
}

func parseCoverageFile(filename string) (data fileCoverages, err error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var fileCoverage fileCoverages
	scanner := bufio.NewScanner(file)

	// Map to store file data blocks
	fileData := make(map[string][]coverageBlock)

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

		fileName := parts[0]  // The file path
		blockInfo := parts[1] // Block data, including line info and statement counts

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

		// Ensure the positions are valid
		if len(startPos) < 2 || len(endPos) < 2 {
			continue
		}

		// Convert start and end positions to integers
		startLine, err1 := strconv.Atoi(startPos[0])
		startCol, err2 := strconv.Atoi(startPos[1])
		endLine, err3 := strconv.Atoi(endPos[0])
		endCol, err4 := strconv.Atoi(endPos[1])
		numStmt, err5 := strconv.Atoi(infoParts[1])
		count, err6 := strconv.Atoi(infoParts[2])

		// Skip if any conversion failed
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || err6 != nil {
			continue
		}

		// Create the coverage block
		block := coverageBlock{
			startLine: startLine,
			startCol:  startCol,
			endLine:   endLine,
			endCol:    endCol,
			numStmt:   numStmt,
			count:     count,
		}

		// Append the block to the file's coverage data
		fileData[fileName] = append(fileData[fileName], block)
	}

	// Convert the map into a slice of CoverageData
	for file, blocks := range fileData {
		fileCoverage = append(fileCoverage, coverageData{
			fileName: file,
			blocks:   blocks,
		})
	}

	return fileCoverage, scanner.Err()
}
