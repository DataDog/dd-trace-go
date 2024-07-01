// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"

	"github.com/stretchr/testify/assert"
)

func TestGetCITagsCache(t *testing.T) {
	ciTags = map[string]string{"key": "value"}

	// First call to initialize ciTags
	tags := GetCITags()
	assert.Equal(t, "value", tags["key"])

	tags["key"] = "newvalue"
	tags = GetCITags()
	assert.Equal(t, "newvalue", tags["key"])
}

func TestGetRelativePathFromCITagsSourceRoot(t *testing.T) {
	ciTags = map[string]string{constants.CIWorkspacePath: "/ci/workspace"}
	absPath := "/ci/workspace/subdir/file.txt"
	expectedRelPath := "subdir/file.txt"

	relPath := GetRelativePathFromCITagsSourceRoot(absPath)
	assert.Equal(t, expectedRelPath, relPath)

	// Test case when CIWorkspacePath is not set in ciTags
	ciTags = map[string]string{}
	relPath = GetRelativePathFromCITagsSourceRoot(absPath)
	assert.Equal(t, absPath, relPath)
}
