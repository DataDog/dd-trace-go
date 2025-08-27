// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package utils

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
)

// --------------------- Tests for getEnvironmentalData -------------------------

// TestGetEnvironmentalData_NoFile verifies that when the expected environmental
// data file does not exist, getEnvironmentalData returns nil.
func TestGetEnvironmentalData_NoFile(t *testing.T) {
	t.Setenv(constants.CIVisibilityEnvironmentDataFilePath, "")

	origArg := os.Args[0]
	defer func() { os.Args[0] = origArg }()

	tempDir, err := os.MkdirTemp("", "envtest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Set os.Args[0] so that fallback filename is computed.
	binaryPath := filepath.Join(tempDir, "testbinary")
	os.Args[0] = binaryPath

	// Since no .env.json file exists, expect nil.
	if result := getEnvironmentalData(); result != nil {
		t.Errorf("Expected nil when environmental file does not exist, got: %+v", result)
	}
}

// TestGetEnvironmentalData_InvalidJSON creates an env file with invalid JSON and
// verifies that getEnvironmentalData returns nil.
func TestGetEnvironmentalData_InvalidJSON(t *testing.T) {
	t.Setenv(constants.CIVisibilityEnvironmentDataFilePath, "")

	origArg := os.Args[0]
	defer func() { os.Args[0] = origArg }()

	tempDir, err := os.MkdirTemp("", "envtest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	binaryPath := filepath.Join(tempDir, "testbinary")
	os.Args[0] = binaryPath

	// Write a file with invalid JSON.
	envFilePath := filepath.Join(tempDir, "testbinary.env.json")
	invalidContent := []byte("{ invalid json }")
	if err := os.WriteFile(envFilePath, invalidContent, 0644); err != nil {
		t.Fatal(err)
	}

	if result := getEnvironmentalData(); result != nil {
		t.Errorf("Expected nil when JSON is invalid, got: %+v", result)
	}
}

// TestGetEnvironmentalData_ValidJSON creates a valid env file and verifies that
// getEnvironmentalData correctly decodes it.
func TestGetEnvironmentalData_ValidJSON(t *testing.T) {
	t.Setenv(constants.CIVisibilityEnvironmentDataFilePath, "")

	origArg := os.Args[0]
	defer func() { os.Args[0] = origArg }()

	tempDir, err := os.MkdirTemp("", "envtest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	binaryPath := filepath.Join(tempDir, "testbinary")
	os.Args[0] = binaryPath

	expected := &fileEnvironmentalData{
		WorkspacePath:        "/workspace/path",
		RepositoryURL:        "https://github.com/repo.git",
		CommitSHA:            "abc123",
		Branch:               "main",
		Tag:                  "v1.0",
		CommitAuthorDate:     "2021-01-01",
		CommitAuthorName:     "Author",
		CommitAuthorEmail:    "author@example.com",
		CommitCommitterDate:  "2021-01-02",
		CommitCommitterName:  "Committer",
		CommitCommitterEmail: "committer@example.com",
		CommitMessage:        "Initial commit",
		CIProviderName:       "provider",
		CIPipelineID:         "pipeline_id",
		CIPipelineURL:        "https://ci.example.com",
		CIPipelineName:       "pipeline",
		CIPipelineNumber:     "42",
		CIStageName:          "stage",
		CIJobName:            "job",
		CIJobURL:             "https://ci.example.com/job",
		CINodeName:           "node",
		CINodeLabels:         "label",
		DDCIEnvVars:          "env_vars",
	}

	envFilePath := filepath.Join(tempDir, "testbinary.env.json")
	file, err := os.Create(envFilePath)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(expected); err != nil {
		t.Fatal(err)
	}
	file.Close()

	result := getEnvironmentalData()
	if result == nil {
		t.Fatal("Expected non-nil result when environmental file exists with valid JSON")
	}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("Mismatch in environmental data.\nGot: %+v\nExpected: %+v", result, expected)
	}
}

// TestGetEnvironmentalData_UsesEnvVar verifies that when the environment variable
// is set, getEnvironmentalData uses that file.
func TestGetEnvironmentalData_UsesEnvVar(t *testing.T) {
	origEnv := os.Getenv(constants.CIVisibilityEnvironmentDataFilePath)
	defer os.Setenv(constants.CIVisibilityEnvironmentDataFilePath, origEnv)

	// Create a temporary file for environmental data.
	tempDir, err := os.MkdirTemp("", "envtest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	customPath := filepath.Join(tempDir, "custom.env.json")
	if err := os.Setenv(constants.CIVisibilityEnvironmentDataFilePath, customPath); err != nil {
		t.Fatal(err)
	}

	expected := &fileEnvironmentalData{
		// other fields can be left empty for this test
	}

	file, err := os.Create(customPath)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(expected); err != nil {
		t.Fatal(err)
	}
	file.Close()

	result := getEnvironmentalData()
	if result == nil {
		t.Fatal("Expected non-nil environmental data when using env var")
	}
}

// --------------------- Tests for getEnvDataFileName -------------------------

// TestGetEnvDataFileName_WithEnvVar verifies that getEnvDataFileName returns the
// value from the environment variable when set.
func TestGetEnvDataFileName_WithEnvVar(t *testing.T) {
	const customPath = "/tmp/custom.env.json"
	orig := os.Getenv(constants.CIVisibilityEnvironmentDataFilePath)
	defer os.Setenv(constants.CIVisibilityEnvironmentDataFilePath, orig)

	if err := os.Setenv(constants.CIVisibilityEnvironmentDataFilePath, customPath); err != nil {
		t.Fatal(err)
	}
	if got := getEnvDataFileName(); got != customPath {
		t.Errorf("Expected %q, got %q", customPath, got)
	}
}

// TestGetEnvDataFileName_WithoutEnvVar verifies that getEnvDataFileName constructs
// the file name based on os.Args[0] when the env var is not set.
func TestGetEnvDataFileName_WithoutEnvVar(t *testing.T) {
	origEnv := os.Getenv(constants.CIVisibilityEnvironmentDataFilePath)
	defer os.Setenv(constants.CIVisibilityEnvironmentDataFilePath, origEnv)
	os.Setenv(constants.CIVisibilityEnvironmentDataFilePath, "")

	origArg := os.Args[0]
	defer func() { os.Args[0] = origArg }()

	tempDir, err := os.MkdirTemp("", "envtest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	binaryPath := filepath.Join(tempDir, "testbinary")
	os.Args[0] = binaryPath

	expected := filepath.Join(tempDir, "testbinary.env.json")
	if got := getEnvDataFileName(); got != expected {
		t.Errorf("Expected %q, got %q", expected, got)
	}
}

// --------------------- Tests for applyEnvironmentalDataIfRequired -------------------------

// TestApplyEnvironmentalDataIfRequired_NoEnvFile verifies that if there is no
// environmental file, the tags map remains unchanged.
func TestApplyEnvironmentalDataIfRequired_NoEnvFile(t *testing.T) {
	t.Setenv(constants.CIVisibilityEnvironmentDataFilePath, "")

	origArg := os.Args[0]
	defer func() { os.Args[0] = origArg }()

	tempDir, err := os.MkdirTemp("", "envtest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	binaryPath := filepath.Join(tempDir, "testbinary")
	os.Args[0] = binaryPath

	tags := map[string]string{
		constants.CIWorkspacePath:         "",
		constants.GitRepositoryURL:        "",
		constants.GitCommitSHA:            "",
		constants.GitBranch:               "",
		constants.GitTag:                  "",
		constants.GitCommitAuthorDate:     "",
		constants.GitCommitAuthorName:     "",
		constants.GitCommitAuthorEmail:    "",
		constants.GitCommitCommitterDate:  "",
		constants.GitCommitCommitterName:  "",
		constants.GitCommitCommitterEmail: "",
		constants.GitCommitMessage:        "",
		constants.CIProviderName:          "",
		constants.CIPipelineID:            "",
		constants.CIPipelineURL:           "",
		constants.CIPipelineName:          "",
		constants.CIPipelineNumber:        "",
		constants.CIStageName:             "",
		constants.CIJobName:               "",
		constants.CIJobURL:                "",
		constants.CINodeName:              "",
		constants.CINodeLabels:            "",
		constants.CIEnvVars:               "",
	}

	applyEnvironmentalDataIfRequired(tags)
	for key, val := range tags {
		if val != "" {
			t.Errorf("Expected tag %s to remain empty, got: %s", key, val)
		}
	}
}

// TestApplyEnvironmentalDataIfRequired_WithEnvFile creates an env file and checks
// that applyEnvironmentalDataIfRequired populates only the missing values.
func TestApplyEnvironmentalDataIfRequired_WithEnvFile(t *testing.T) {
	t.Setenv(constants.CIVisibilityEnvironmentDataFilePath, "")

	origArg := os.Args[0]
	defer func() { os.Args[0] = origArg }()

	tempDir, err := os.MkdirTemp("", "envtest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	binaryPath := filepath.Join(tempDir, "testbinary")
	os.Args[0] = binaryPath

	envData := &fileEnvironmentalData{
		WorkspacePath:        "/workspace",
		RepositoryURL:        "repo_url",
		CommitSHA:            "sha",
		Branch:               "branch",
		Tag:                  "tag",
		CommitAuthorDate:     "date1",
		CommitAuthorName:     "author",
		CommitAuthorEmail:    "author@example.com",
		CommitCommitterDate:  "date2",
		CommitCommitterName:  "committer",
		CommitCommitterEmail: "committer@example.com",
		CommitMessage:        "message",
		CIProviderName:       "ci_provider",
		CIPipelineID:         "pipeline_id",
		CIPipelineURL:        "pipeline_url",
		CIPipelineName:       "pipeline_name",
		CIPipelineNumber:     "pipeline_number",
		CIStageName:          "stage",
		CIJobName:            "job",
		CIJobURL:             "job_url",
		CINodeName:           "node",
		CINodeLabels:         "labels",
		DDCIEnvVars:          "env_vars",
	}

	// Create the env file.
	envFilePath := filepath.Join(tempDir, "testbinary.env.json")
	file, err := os.Create(envFilePath)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(envData); err != nil {
		t.Fatal(err)
	}
	file.Close()

	// Prepare tags with some values already set.
	tags := map[string]string{
		constants.CIWorkspacePath:         "existing", // should not be overwritten
		constants.GitRepositoryURL:        "",
		constants.GitCommitSHA:            "",
		constants.GitBranch:               "",
		constants.GitTag:                  "",
		constants.GitCommitAuthorDate:     "",
		constants.GitCommitAuthorName:     "",
		constants.GitCommitAuthorEmail:    "",
		constants.GitCommitCommitterDate:  "",
		constants.GitCommitCommitterName:  "",
		constants.GitCommitCommitterEmail: "",
		constants.GitCommitMessage:        "",
		constants.CIProviderName:          "",
		constants.CIPipelineID:            "",
		constants.CIPipelineURL:           "",
		constants.CIPipelineName:          "",
		constants.CIPipelineNumber:        "",
		constants.CIStageName:             "",
		constants.CIJobName:               "",
		constants.CIJobURL:                "",
		constants.CINodeName:              "",
		constants.CINodeLabels:            "",
		constants.CIEnvVars:               "",
	}

	applyEnvironmentalDataIfRequired(tags)
	if tags[constants.CIWorkspacePath] != "existing" {
		t.Errorf("Expected CIWorkspacePath to remain 'existing', got '%s'", tags[constants.CIWorkspacePath])
	}
	if tags[constants.GitRepositoryURL] != "repo_url" {
		t.Errorf("Expected GitRepositoryURL to be 'repo_url', got '%s'", tags[constants.GitRepositoryURL])
	}
	if tags[constants.GitCommitSHA] != "sha" {
		t.Errorf("Expected GitCommitSHA to be 'sha', got '%s'", tags[constants.GitCommitSHA])
	}
	if tags[constants.GitBranch] != "branch" {
		t.Errorf("Expected GitBranch to be 'branch', got '%s'", tags[constants.GitBranch])
	}
	if tags[constants.GitTag] != "tag" {
		t.Errorf("Expected GitTag to be 'tag', got '%s'", tags[constants.GitTag])
	}
	if tags[constants.GitCommitAuthorDate] != "date1" {
		t.Errorf("Expected GitCommitAuthorDate to be 'date1', got '%s'", tags[constants.GitCommitAuthorDate])
	}
	if tags[constants.GitCommitAuthorName] != "author" {
		t.Errorf("Expected GitCommitAuthorName to be 'author', got '%s'", tags[constants.GitCommitAuthorName])
	}
	if tags[constants.GitCommitAuthorEmail] != "author@example.com" {
		t.Errorf("Expected GitCommitAuthorEmail to be 'author@example.com', got '%s'", tags[constants.GitCommitAuthorEmail])
	}
	if tags[constants.GitCommitCommitterDate] != "date2" {
		t.Errorf("Expected GitCommitCommitterDate to be 'date2', got '%s'", tags[constants.GitCommitCommitterDate])
	}
	if tags[constants.GitCommitCommitterName] != "committer" {
		t.Errorf("Expected GitCommitCommitterName to be 'committer', got '%s'", tags[constants.GitCommitCommitterName])
	}
	if tags[constants.GitCommitCommitterEmail] != "committer@example.com" {
		t.Errorf("Expected GitCommitCommitterEmail to be 'committer@example.com', got '%s'", tags[constants.GitCommitCommitterEmail])
	}
	if tags[constants.GitCommitMessage] != "message" {
		t.Errorf("Expected GitCommitMessage to be 'message', got '%s'", tags[constants.GitCommitMessage])
	}
	if tags[constants.CIProviderName] != "ci_provider" {
		t.Errorf("Expected CIProviderName to be 'ci_provider', got '%s'", tags[constants.CIProviderName])
	}
	if tags[constants.CIPipelineID] != "pipeline_id" {
		t.Errorf("Expected CIPipelineID to be 'pipeline_id', got '%s'", tags[constants.CIPipelineID])
	}
	if tags[constants.CIPipelineURL] != "pipeline_url" {
		t.Errorf("Expected CIPipelineURL to be 'pipeline_url', got '%s'", tags[constants.CIPipelineURL])
	}
	if tags[constants.CIPipelineName] != "pipeline_name" {
		t.Errorf("Expected CIPipelineName to be 'pipeline_name', got '%s'", tags[constants.CIPipelineName])
	}
	if tags[constants.CIPipelineNumber] != "pipeline_number" {
		t.Errorf("Expected CIPipelineNumber to be 'pipeline_number', got '%s'", tags[constants.CIPipelineNumber])
	}
	if tags[constants.CIStageName] != "stage" {
		t.Errorf("Expected CIStageName to be 'stage', got '%s'", tags[constants.CIStageName])
	}
	if tags[constants.CIJobName] != "job" {
		t.Errorf("Expected CIJobName to be 'job', got '%s'", tags[constants.CIJobName])
	}
	if tags[constants.CIJobURL] != "job_url" {
		t.Errorf("Expected CIJobURL to be 'job_url', got '%s'", tags[constants.CIJobURL])
	}
	if tags[constants.CINodeName] != "node" {
		t.Errorf("Expected CINodeName to be 'node', got '%s'", tags[constants.CINodeName])
	}
	if tags[constants.CINodeLabels] != "labels" {
		t.Errorf("Expected CINodeLabels to be 'labels', got '%s'", tags[constants.CINodeLabels])
	}
	if tags[constants.CIEnvVars] != "env_vars" {
		t.Errorf("Expected CIEnvVars to be 'env_vars', got '%s'", tags[constants.CIEnvVars])
	}
}

// --------------------- Tests for createEnvironmentalDataFromTags -------------------------

// TestCreateEnvironmentalDataFromTags checks that a proper fileEnvironmentalData
// object is created from the given tags.
func TestCreateEnvironmentalDataFromTags(t *testing.T) {
	// Nil tags should return nil.
	if data := createEnvironmentalDataFromTags(nil); data != nil {
		t.Error("Expected nil for nil tags")
	}

	tags := map[string]string{
		constants.TestSessionName:         "session",
		constants.CIWorkspacePath:         "/workspace",
		constants.GitRepositoryURL:        "repo",
		constants.GitCommitSHA:            "sha",
		constants.GitBranch:               "branch",
		constants.GitTag:                  "tag",
		constants.GitCommitAuthorDate:     "date1",
		constants.GitCommitAuthorName:     "author",
		constants.GitCommitAuthorEmail:    "author@example.com",
		constants.GitCommitCommitterDate:  "date2",
		constants.GitCommitCommitterName:  "committer",
		constants.GitCommitCommitterEmail: "committer@example.com",
		constants.GitCommitMessage:        "message",
		constants.CIProviderName:          "provider",
		constants.CIPipelineID:            "id",
		constants.CIPipelineURL:           "url",
		constants.CIPipelineName:          "name",
		constants.CIPipelineNumber:        "num",
		constants.CIStageName:             "stage",
		constants.CIJobName:               "job",
		constants.CIJobURL:                "joburl",
		constants.CINodeName:              "node",
		constants.CINodeLabels:            "labels",
		constants.CIEnvVars:               "env_vars",
	}

	data := createEnvironmentalDataFromTags(tags)
	if data == nil {
		t.Fatal("Expected non-nil environmental data")
	}
	if data.WorkspacePath != "/workspace" {
		t.Errorf("Expected WorkspacePath '/workspace', got '%s'", data.WorkspacePath)
	}
	// Additional field checks can be added similarly if needed.
}

// --------------------- Tests for writeEnvironmentalDataToFile -------------------------

// TestWriteEnvironmentalDataToFile_NilTags verifies that if tags is nil,
// the function returns nil and does not create a file.
func TestWriteEnvironmentalDataToFile_NilTags(t *testing.T) {
	t.Setenv(constants.CIVisibilityEnvironmentDataFilePath, "")

	tempDir, err := os.MkdirTemp("", "envtest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	filePath := filepath.Join(tempDir, "output.env.json")
	err = writeEnvironmentalDataToFile(filePath, nil)
	if err != nil {
		t.Errorf("Expected nil error for nil tags, got: %v", err)
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("Expected file not to exist when tags is nil")
	}
}

// TestWriteEnvironmentalDataToFile_WithTags creates a file from given tags and
// verifies that the written JSON matches the expected values.
func TestWriteEnvironmentalDataToFile_WithTags(t *testing.T) {
	t.Setenv(constants.CIVisibilityEnvironmentDataFilePath, "")

	tempDir, err := os.MkdirTemp("", "envtest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	filePath := filepath.Join(tempDir, "output.env.json")
	tags := map[string]string{
		constants.CIWorkspacePath:         "/workspace",
		constants.GitRepositoryURL:        "repo",
		constants.GitCommitSHA:            "sha",
		constants.GitBranch:               "branch",
		constants.GitTag:                  "tag",
		constants.GitCommitAuthorDate:     "date1",
		constants.GitCommitAuthorName:     "author",
		constants.GitCommitAuthorEmail:    "author@example.com",
		constants.GitCommitCommitterDate:  "date2",
		constants.GitCommitCommitterName:  "committer",
		constants.GitCommitCommitterEmail: "committer@example.com",
		constants.GitCommitMessage:        "message",
		constants.CIProviderName:          "provider",
		constants.CIPipelineID:            "id",
		constants.CIPipelineURL:           "url",
		constants.CIPipelineName:          "name",
		constants.CIPipelineNumber:        "num",
		constants.CIStageName:             "stage",
		constants.CIJobName:               "job",
		constants.CIJobURL:                "joburl",
		constants.CINodeName:              "node",
		constants.CINodeLabels:            "labels",
		constants.CIEnvVars:               "env_vars",
	}

	err = writeEnvironmentalDataToFile(filePath, tags)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Error reading file: %v", err)
	}
	var envData fileEnvironmentalData
	if err := json.Unmarshal(data, &envData); err != nil {
		t.Fatalf("Error decoding JSON: %v", err)
	}
	if envData.WorkspacePath != "/workspace" {
		t.Errorf("Expected WorkspacePath '/workspace', got '%s'", envData.WorkspacePath)
	}
	// Additional field checks can be added similarly if needed.
}
