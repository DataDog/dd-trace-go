// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

func TestExtractPublicAPI(t *testing.T) {
	testdataDir := filepath.Join("_testdata", "dummy")
	expectedOutputFile := filepath.Join("_testdata", "expected_output.txt")

	var buf bytes.Buffer

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	if err := run(testdataDir, ""); err != nil {
		t.Fatalf("failed to run api_extractor: %v", err)
	}

	os.Stdout = old

	if err := w.Close(); err != nil {
		t.Fatalf("failed to close pipe: %v", err)
	}

	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("failed to copy output: %v", err)
	}

	// Update golden files if requested
	if *update {
		if err := os.WriteFile(expectedOutputFile, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("failed to update golden file: %v", err)
		}

		return
	}

	// Normal test comparison
	expectedOutput, err := os.ReadFile(expectedOutputFile)
	if err != nil {
		t.Fatalf("failed to read expected output file: %v", err)
	}

	if buf.String() != string(expectedOutput) {
		t.Errorf("output does not match expected output\nGot:\n%s\nExpected:\n%s", buf.String(), string(expectedOutput))
	}
}
