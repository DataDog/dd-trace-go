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

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
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
	// Reset provider env key when running in CI
	resetProviders := map[string]string{}
	for key := range providers {
		if value, ok := env.LookupEnv(key); ok {
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
