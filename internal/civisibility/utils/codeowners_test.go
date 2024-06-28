// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewCodeOwners(t *testing.T) {
	// Create a temporary file for testing
	fileContent := `[Section 1]
/path/to/file @owner1 @owner2
/path/to/* @owner3

[Section 2]
/another/path @owner4
`

	tmpFile, err := os.CreateTemp("", "CODEOWNERS")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(fileContent)
	assert.NoError(t, err)

	err = tmpFile.Close()
	assert.NoError(t, err)

	// Test NewCodeOwners
	codeOwners, err := NewCodeOwners(tmpFile.Name())
	assert.NoError(t, err)
	assert.NotNil(t, codeOwners)
	assert.Equal(t, 2, len(codeOwners.Sections))
	assert.Equal(t, 2, len(codeOwners.GetSection("Section 1").Entries))
	assert.Equal(t, 1, len(codeOwners.GetSection("Section 2").Entries))

	// Test empty file path
	_, err = NewCodeOwners("")
	assert.Error(t, err)
}

func TestFindSectionIgnoreCase(t *testing.T) {
	sections := []string{"Section1", "section2", "SECTION3"}
	assert.Equal(t, "Section1", findSectionIgnoreCase(sections, "section1"))
	assert.Equal(t, "section2", findSectionIgnoreCase(sections, "SECTION2"))
	assert.Equal(t, "SECTION3", findSectionIgnoreCase(sections, "Section3"))
	assert.Equal(t, "", findSectionIgnoreCase(sections, "Section4"))
}

func TestMatch(t *testing.T) {
	entries := []Entry{
		{Pattern: "/path/to/file", Owners: []string{"@owner1", "@owner2"}, Section: "Section 1"},
		{Pattern: "/path/to/*", Owners: []string{"@owner3"}, Section: "Section 1"},
		{Pattern: "/another/path", Owners: []string{"@owner4"}, Section: "Section 2"},
	}
	sections := []*Section{
		{Name: "Section 1", Entries: []Entry{entries[0], entries[1]}},
		{Name: "Section 2", Entries: []Entry{entries[2]}},
	}

	codeOwners := &CodeOwners{Sections: sections}

	// Test exact match
	entry, ok := codeOwners.Match("/path/to/file")
	assert.True(t, ok)
	assert.Equal(t, entries[0], *entry)

	// Test wildcard match
	entry, ok = codeOwners.Match("/path/to/anything")
	assert.True(t, ok)
	assert.Equal(t, entries[1], *entry)

	// Test no match
	entry, ok = codeOwners.Match("/no/match")
	assert.False(t, ok)
}

func TestGetOwnersString(t *testing.T) {
	entry := Entry{Owners: []string{"@owner1", "@owner2"}}
	assert.Equal(t, "[\"@owner1\",\"@owner2\"]", entry.GetOwnersString())

	entry = Entry{}
	assert.Equal(t, "", entry.GetOwnersString())
}

func TestGithubCodeOwners(t *testing.T) {
	cOwners, err := NewCodeOwners("testdata/fixtures/codeowners/CODEOWNERS_GITHUB")
	if err != nil {
		t.Fatal(err)
	}
	if cOwners == nil {
		t.Fatal("nil codeowners")
	}

	data := []struct {
		value    string
		expected string
	}{
		{value: "unexistent/path/test.cs", expected: "[\"@global-owner1\",\"@global-owner2\"]"},
		{value: "apps/test.cs", expected: "[\"@octocat\"]"},
		{value: "/example/apps/test.cs", expected: "[\"@octocat\"]"},
		{value: "/docs/test.cs", expected: "[\"@doctocat\"]"},
		{value: "/examples/docs/test.cs", expected: "[\"docs@example.com\"]"},
		{value: "/src/vendor/match.go", expected: "[\"docs@example.com\"]"},
		{value: "/examples/docs/inside/test.cs", expected: "[\"@global-owner1\",\"@global-owner2\"]"},
		{value: "/component/path/test.js", expected: "[\"@js-owner\"]"},
		{value: "/mytextbox.txt", expected: "[\"@octo-org/octocats\"]"},
		{value: "/scripts/artifacts/value.js", expected: "[\"@doctocat\",\"@octocat\"]"},
		{value: "/apps/octo/test.cs", expected: "[\"@octocat\"]"},
		{value: "/apps/github", expected: ""},
	}

	for _, item := range data {
		t.Run(strings.ReplaceAll(item.value, "/", "_"), func(t *testing.T) {
			match, ok := cOwners.Match(item.value)
			assert.True(t, ok)
			assert.EqualValues(t, item.expected, match.GetOwnersString())
		})
	}
}

func TestGitlabCodeOwners(t *testing.T) {
	cOwners, err := NewCodeOwners("testdata/fixtures/codeowners/CODEOWNERS_GITLAB")
	if err != nil {
		t.Fatal(err)
	}
	if cOwners == nil {
		t.Fatal("nil codeowners")
	}

	data := []struct {
		value    string
		expected string
	}{
		{value: "apps/README.md", expected: "[\"@docs\",\"@database\",\"@multiple\",\"@code\",\"@owners\"]"},
		{value: "model/db", expected: "[\"@database\",\"@multiple\",\"@code\",\"@owners\"]"},
		{value: "/config/data.conf", expected: "[\"@config-owner\"]"},
		{value: "/docs/root.md", expected: "[\"@root-docs\"]"},
		{value: "/docs/sub/root.md", expected: "[\"@all-docs\"]"},
		{value: "/src/README", expected: "[\"@group\",\"@group/with-nested/subgroup\"]"},
		{value: "/src/lib/internal.h", expected: "[\"@lib-owner\"]"},
		{value: "src/ee/docs", expected: "[\"@docs\",\"@multiple\",\"@code\",\"@owners\"]"},
	}

	for _, item := range data {
		t.Run(strings.ReplaceAll(item.value, "/", "_"), func(t *testing.T) {
			match, ok := cOwners.Match(item.value)
			assert.True(t, ok)
			assert.EqualValues(t, item.expected, match.GetOwnersString())
		})
	}
}
