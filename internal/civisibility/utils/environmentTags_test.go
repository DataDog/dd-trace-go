// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"

	"github.com/stretchr/testify/assert"
)

func TestGetCITagsCache(t *testing.T) {
	ResetCITags()
	originalCiTags = map[string]string{"key": "value"}

	// First call to initialize ciTags
	tags := GetCITags()
	assert.Equal(t, "value", tags["key"])

	tags["key"] = "newvalue"
	tags = GetCITags()
	assert.Equal(t, "newvalue", tags["key"])
}

func TestAddCITags(t *testing.T) {
	ResetCITags()
	originalCiTags = map[string]string{"key": "value"}

	// First call to initialize ciTags
	tags := GetCITags()
	assert.Equal(t, "value", tags["key"])

	AddCITags("key", "newvalue")
	AddCITags("key2", "value2")
	tags = GetCITags()
	assert.Equal(t, "newvalue", tags["key"])
	assert.Equal(t, "value2", tags["key2"])
}

func TestAddCITagsMap(t *testing.T) {
	ResetCITags()
	originalCiTags = map[string]string{"key": "value"}

	// First call to initialize ciTags
	tags := GetCITags()
	assert.Equal(t, "value", tags["key"])

	nmap := map[string]string{}
	nmap["key"] = "newvalue"
	nmap["key2"] = "value2"
	AddCITagsMap(nmap)
	tags = GetCITags()
	assert.Equal(t, "newvalue", tags["key"])
	assert.Equal(t, "value2", tags["key2"])
}

func TestGetCIMetricsCache(t *testing.T) {
	ResetCIMetrics()
	originalCiMetrics = map[string]float64{"key": float64(1)}

	// First call to initialize ciMetrics
	tags := GetCIMetrics()
	assert.Equal(t, float64(1), tags["key"])

	tags["key"] = float64(42)
	tags = GetCIMetrics()
	assert.Equal(t, float64(42), tags["key"])
}

func TestAddCIMetrics(t *testing.T) {
	ResetCIMetrics()
	originalCiMetrics = map[string]float64{"key": float64(1)}

	// First call to initialize ciMetrics
	tags := GetCIMetrics()
	assert.Equal(t, float64(1), tags["key"])

	AddCIMetrics("key", float64(42))
	AddCIMetrics("key2", float64(2))
	tags = GetCIMetrics()
	assert.Equal(t, float64(42), tags["key"])
	assert.Equal(t, float64(2), tags["key2"])
}

func TestAddCIMetricsMap(t *testing.T) {
	ResetCIMetrics()
	originalCiMetrics = map[string]float64{"key": float64(1)}

	// First call to initialize ciMetrics
	tags := GetCIMetrics()
	assert.Equal(t, float64(1), tags["key"])

	nmap := map[string]float64{}
	nmap["key"] = float64(42)
	nmap["key2"] = float64(2)
	AddCIMetricsMap(nmap)
	tags = GetCIMetrics()
	assert.Equal(t, float64(42), tags["key"])
	assert.Equal(t, float64(2), tags["key2"])
}

func TestGetRelativePathFromCITagsSourceRoot(t *testing.T) {
	ResetCITags()
	originalCiTags = map[string]string{constants.CIWorkspacePath: "/ci/workspace"}

	absPath := "/ci/workspace/subdir/file.txt"
	expectedRelPath := "subdir/file.txt"

	relPath := GetRelativePathFromCITagsSourceRoot(absPath)
	assert.Equal(t, expectedRelPath, relPath)

	// Test case when CIWorkspacePath is not set in ciTags
	originalCiTags = map[string]string{}
	currentCiTags = nil
	relPath = GetRelativePathFromCITagsSourceRoot(absPath)
	assert.Equal(t, absPath, relPath)
}
