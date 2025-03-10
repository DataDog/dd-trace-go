// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractPublicAPI(t *testing.T) {
	testdataDir := filepath.Join("_testdata", "dummy")
	expectedOutputFile := filepath.Join("_testdata", "expected_output.txt")

	expectedOutput, err := os.ReadFile(expectedOutputFile)
	if err != nil {
		t.Fatalf("failed to read expected output file: %v", err)
	}

	var buf bytes.Buffer
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	if err := run(testdataDir); err != nil {
		t.Fatalf("failed to run api_extractor: %v", err)
	}

	os.Stdout = old
	w.Close()
	io.Copy(&buf, r)

	if buf.String() != string(expectedOutput) {
		t.Errorf("output does not match expected output\nGot:\n%s\nExpected:\n%s", buf.String(), string(expectedOutput))
	}
}
