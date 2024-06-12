// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

//go:build go1.20

package gotesting

import (
	"bytes"
	"errors"
	"io"
	"os"
	"regexp"
	"runtime/coverage"
	"strconv"
	"testing"
	_ "unsafe"
)

// runtime_coverage_processCoverTestDirInternal is an internal runtime function used to process coverage data.
// This declaration uses go:linkname to access the unexported function from the runtime package.
//
//go:linkname runtime_coverage_processCoverTestDirInternal runtime/coverage.processCoverTestDirInternal
func runtime_coverage_processCoverTestDirInternal(dir string, cfile string, cm string, cpkg string, w io.Writer) error

// Ensure the coverage package is included in the binary so the linker can find the symbols.
var _ = coverage.ClearCounters

// getCoverage processes the coverage counters using the internal runtime function
// and parses the result to return the coverage percentage.
//
// It reads the GOCOVERDIR environment variable to locate the directory containing coverage data,
// invokes the runtime_coverage_processCoverTestDirInternal function to process the coverage data,
// and extracts the coverage percentage from the output.
//
// Returns:
//
//	A float64 representing the coverage percentage.
//	An error if any part of the process fails or if the GOCOVERDIR environment variable is not set.
func getCoverage() (float64, error) {
	goCoverDir := os.Getenv("GOCOVERDIR")
	if goCoverDir == "" {
		return 0, errors.New("GOCOVERDIR environment variable not set")
	}

	buffer := new(bytes.Buffer)
	err := runtime_coverage_processCoverTestDirInternal(goCoverDir, "", testing.CoverMode(), "", buffer)
	if err == nil {
		re := regexp.MustCompile(`(?si)coverage: (.*)%`)
		results := re.FindStringSubmatch(buffer.String())
		if len(results) == 2 {
			percentage, err := strconv.ParseFloat(results[1], 64)
			if err == nil {
				return percentage, nil
			}
		}
	}
	return 0, err
}
