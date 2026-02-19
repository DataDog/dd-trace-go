// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
)

func setEnvs(t *testing.T, env map[string]any) {
	for key, value := range env {
		if strValue, ok := value.(string); ok {
			t.Setenv(key, strValue)
		}
		if intValue, ok := value.(int); ok {
			t.Setenv(key, fmt.Sprintf("%d", intValue))
		}
		if boolValue, ok := value.(bool); ok {
			if boolValue {
				t.Setenv(key, "true")
			} else {
				t.Setenv(key, "false")
			}
		}
		if floatValue, ok := value.(float64); ok {
			t.Setenv(key, fmt.Sprintf("%d", int(floatValue)))
		}
	}
}

func sortJSONKeys(jsonStr string) string {
	tmp := map[string]string{}
	_ = json.Unmarshal([]byte(jsonStr), &tmp)
	jsonBytes, _ := json.Marshal(tmp)
	return string(jsonBytes)
}

// TestTags asserts that all tags are extracted from environment variables.
func TestTags(t *testing.T) {
	// Disable diagnostics scanning to prevent tests from reading real _diag directories
	// when running on GitHub Actions runners
	originalDiagEnabled := githubActionsDiagnosticsEnabled
	githubActionsDiagnosticsEnabled = false
	defer func() {
		githubActionsDiagnosticsEnabled = originalDiagEnabled
	}()

	// Reset provider env key when running in CI
	resetProviders := map[string]string{}
	for key := range providers {
		if value, ok := os.LookupEnv(key); ok {
			resetProviders[key] = value
			_ = os.Unsetenv(key)
		}
	}
	defer func() {
		for key, value := range resetProviders {
			_ = os.Setenv(key, value)
		}
	}()

	paths, err := filepath.Glob("testdata/fixtures/providers/*.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		providerName := strings.TrimSuffix(filepath.Base(path), ".json")

		t.Run(providerName, func(t *testing.T) {
			fp, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}

			data, err := io.ReadAll(fp)
			if err != nil {
				t.Fatal(err)
			}

			var examples [][]map[string]any
			if err := json.Unmarshal(data, &examples); err != nil {
				t.Fatal(err)
			}

			for i, line := range examples {
				name := fmt.Sprintf("%d", i)
				env := line[0]
				tags := line[1]

				// Because we have a fallback algorithm for some variables
				// we need to initialize some of them to not use the one set by the github action running this test.
				if providerName == "github" {
					// We initialize GITHUB_RUN_ATTEMPT if it doesn't exist to avoid using the one set in the GitHub action.
					if _, ok := env["GITHUB_RUN_ATTEMPT"]; !ok {
						env["GITHUB_RUN_ATTEMPT"] = ""
					}
					// We initialize GITHUB_HEAD_REF if it doesn't exist to avoid using the one set in the GitHub action.
					if _, ok := env["GITHUB_HEAD_REF"]; !ok {
						env["GITHUB_HEAD_REF"] = ""
					}
					// We initialize GITHUB_REF if it doesn't exist to avoid using the one set in the GitHub action.
					if _, ok := env["GITHUB_REF"]; !ok {
						env["GITHUB_REF"] = ""
					}
					// We initialize JOB_CHECK_RUN_ID if it doesn't exist to avoid using the one set in the GitHub action.
					if _, ok := env["JOB_CHECK_RUN_ID"]; !ok {
						env["JOB_CHECK_RUN_ID"] = ""
					}
				}

				t.Run(name, func(t *testing.T) {
					setEnvs(t, env)
					providerTags := getProviderTags()

					for expectedKey, expectedValue := range tags {
						if actualValue, ok := providerTags[expectedKey]; ok {
							if expectedKey == "_dd.ci.env_vars" {
								expectedValue = sortJSONKeys(expectedValue.(string))
							}
							if providerName == "github" && expectedKey == constants.GitPrBaseBranch || expectedKey == constants.GitPrBaseCommit || expectedKey == constants.GitHeadCommit {
								continue
							}
							if fmt.Sprintln(expectedValue) != actualValue {
								if expectedValue == strings.ReplaceAll(actualValue, "\\", "/") {
									continue
								}

								t.Fatalf("Key: %s, the actual value (%s) is different to the expected value (%s)", expectedKey, actualValue, expectedValue)
							}
						} else {
							t.Fatalf("Key: %s, doesn't exist.", expectedKey)
						}
					}
				})
			}
		})
	}
}

func TestGitHubEventFile(t *testing.T) {
	originalEventPath := os.Getenv("GITHUB_EVENT_PATH")
	originalBaseRef := os.Getenv("GITHUB_BASE_REF")
	defer func() {
		os.Setenv("GITHUB_EVENT_PATH", originalEventPath)
		os.Setenv("GITHUB_BASE_REF", originalBaseRef)
	}()

	os.Unsetenv("GITHUB_EVENT_PATH")
	os.Unsetenv("GITHUB_BASE_REF")

	checkValue := func(tags map[string]string, key, expectedValue string) {
		if tags[key] != expectedValue {
			t.Fatalf("Key: %s, the actual value (%s) is different to the expected value (%s)", key, tags[key], expectedValue)
		}
	}

	t.Run("with event file", func(t *testing.T) {
		eventFile := "testdata/fixtures/github-event.json"
		t.Setenv("GITHUB_EVENT_PATH", eventFile)
		t.Setenv("GITHUB_BASE_REF", "my-base-ref") // this should be ignored in favor of the event file value

		tags := extractGithubActions()
		expectedHeadCommit := "df289512a51123083a8e6931dd6f57bb3883d4c4"
		expectedBaseCommit := "52e0974c74d41160a03d59ddc73bb9f5adab054b"
		expectedBaseRef := "main"
		expectedPrNumber := "1"

		checkValue(tags, constants.GitHeadCommit, expectedHeadCommit)
		checkValue(tags, constants.GitPrBaseHeadCommit, expectedBaseCommit)
		checkValue(tags, constants.GitPrBaseBranch, expectedBaseRef)
		checkValue(tags, constants.PrNumber, expectedPrNumber)
	})

	t.Run("no event file", func(t *testing.T) {
		t.Setenv("GITHUB_BASE_REF", "my-base-ref") // this should be ignored in favor of the event file value

		tags := extractGithubActions()
		checkValue(tags, constants.GitPrBaseBranch, "my-base-ref")
	})
}

func TestIsNumericJobID(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"12345678901", true},
		{"0", true},
		{"1", true},
		{"9999999999999999999", true},
		{"", false},
		{"abc", false},
		{"123abc", false},
		{"abc123", false},
		{"-123", false},
		{"12.34", false},
		{" 123", false},
		{"123 ", false},
		{" ", false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("input=%q", tt.input), func(t *testing.T) {
			if got := isNumericJobID(tt.input); got != tt.expected {
				t.Errorf("isNumericJobID(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestGithubActionsJobIDFromDiagnostics(t *testing.T) {
	t.Run("valid JSON", func(t *testing.T) {
		diagDir := t.TempDir()
		content := `{"job":{"d":[{"k":"check_run_id","v":12345678901}]}}`
		workerLog := filepath.Join(diagDir, "Worker_20240101.log")
		if err := os.WriteFile(workerLog, []byte(content), 0o644); err != nil {
			t.Fatalf("write worker log: %v", err)
		}

		jobID, ok := tryExtractJobIDFromDiag([]string{diagDir})
		if !ok || jobID != "12345678901" {
			t.Fatalf("expected 12345678901, got %q (ok=%v)", jobID, ok)
		}
	})

	t.Run("valid JSON with float value", func(t *testing.T) {
		diagDir := t.TempDir()
		content := `{"job":{"d":[{"k":"check_run_id","v":55411116365.0}]}}`
		workerLog := filepath.Join(diagDir, "Worker_20240101.log")
		if err := os.WriteFile(workerLog, []byte(content), 0o644); err != nil {
			t.Fatalf("write worker log: %v", err)
		}

		jobID, ok := tryExtractJobIDFromDiag([]string{diagDir})
		if !ok || jobID != "55411116365" {
			t.Fatalf("expected 55411116365, got %q (ok=%v)", jobID, ok)
		}
	})

	t.Run("regex fallback with timestamp prefix", func(t *testing.T) {
		diagDir := t.TempDir()
		content := `[2024-01-01 12:00:00] {"job":{"d":[{"k":"check_run_id","v":12345678901.0}]}}`
		workerLog := filepath.Join(diagDir, "Worker_20240101.log")
		if err := os.WriteFile(workerLog, []byte(content), 0o644); err != nil {
			t.Fatalf("write worker log: %v", err)
		}

		jobID, ok := tryExtractJobIDFromDiag([]string{diagDir})
		if !ok || jobID != "12345678901" {
			t.Fatalf("expected 12345678901, got %q (ok=%v)", jobID, ok)
		}
	})

	t.Run("empty directory", func(t *testing.T) {
		diagDir := t.TempDir()

		jobID, ok := tryExtractJobIDFromDiag([]string{diagDir})
		if ok || jobID != "" {
			t.Fatalf("expected empty result, got %q (ok=%v)", jobID, ok)
		}
	})

	t.Run("non-existent directory", func(t *testing.T) {
		jobID, ok := tryExtractJobIDFromDiag([]string{"/non/existent/path"})
		if ok || jobID != "" {
			t.Fatalf("expected empty result, got %q (ok=%v)", jobID, ok)
		}
	})

	t.Run("newest file is used first", func(t *testing.T) {
		diagDir := t.TempDir()

		// Create older file with different job ID
		oldContent := `{"job":{"d":[{"k":"check_run_id","v":11111111111}]}}`
		oldLog := filepath.Join(diagDir, "Worker_20230101.log")
		if err := os.WriteFile(oldLog, []byte(oldContent), 0o644); err != nil {
			t.Fatalf("write old worker log: %v", err)
		}

		// Create newer file with expected job ID
		newContent := `{"job":{"d":[{"k":"check_run_id","v":22222222222}]}}`
		newLog := filepath.Join(diagDir, "Worker_20240101.log")
		if err := os.WriteFile(newLog, []byte(newContent), 0o644); err != nil {
			t.Fatalf("write new worker log: %v", err)
		}

		// Set explicit modification times to ensure deterministic ordering
		oldTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
		newTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		if err := os.Chtimes(oldLog, oldTime, oldTime); err != nil {
			t.Fatalf("set old log time: %v", err)
		}
		if err := os.Chtimes(newLog, newTime, newTime); err != nil {
			t.Fatalf("set new log time: %v", err)
		}

		jobID, ok := tryExtractJobIDFromDiag([]string{diagDir})
		if !ok || jobID != "22222222222" {
			t.Fatalf("expected 22222222222 (from newest file), got %q (ok=%v)", jobID, ok)
		}
	})

	t.Run("invalid JSON without check_run_id", func(t *testing.T) {
		diagDir := t.TempDir()
		content := `{"job":{"d":[{"k":"other_key","v":12345}]}}`
		workerLog := filepath.Join(diagDir, "Worker_20240101.log")
		if err := os.WriteFile(workerLog, []byte(content), 0o644); err != nil {
			t.Fatalf("write worker log: %v", err)
		}

		jobID, ok := tryExtractJobIDFromDiag([]string{diagDir})
		if ok || jobID != "" {
			t.Fatalf("expected empty result, got %q (ok=%v)", jobID, ok)
		}
	})
}
