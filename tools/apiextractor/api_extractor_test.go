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
