// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetCiTagsCache(t *testing.T) {
	ciTags = map[string]string{"key": "value"}

	// First call to initialize ciTags
	tags := GetCiTags()
	assert.Equal(t, "value", tags["key"])

	tags["key"] = "newvalue"
	tags = GetCiTags()
	assert.Equal(t, "newvalue", tags["key"])
}

func TestGetRelativePathFromCiTagsSourceRoot(t *testing.T) {
	ciTags = map[string]string{constants.CIWorkspacePath: "/ci/workspace"}
	absPath := "/ci/workspace/subdir/file.txt"
	expectedRelPath := "subdir/file.txt"

	relPath := GetRelativePathFromCiTagsSourceRoot(absPath)
	assert.Equal(t, expectedRelPath, relPath)

	// Test case when CIWorkspacePath is not set in ciTags
	ciTags = map[string]string{}
	relPath = GetRelativePathFromCiTagsSourceRoot(absPath)
	assert.Equal(t, absPath, relPath)
}
