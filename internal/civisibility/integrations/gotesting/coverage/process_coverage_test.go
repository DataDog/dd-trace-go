// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"
)

func TestMergeProcessCoverageProfilesAddsIsolatedCounts(t *testing.T) {
	dir := t.TempDir()
	parentPath := filepath.Join(dir, "parent.out")
	childPath := filepath.Join(dir, "child.out")
	require.NoError(t, os.WriteFile(parentPath, []byte("mode: count\npkg/file.go:1.1,1.2 1 2\npkg/file.go:2.1,2.2 1 0\n"), 0o600))
	require.NoError(t, os.WriteFile(childPath, []byte("mode: count\npkg/file.go:1.1,1.2 1 3\npkg/file.go:2.1,2.2 1 1\n"), 0o600))
	parent, err := parseOrderedCoverProfile(parentPath)
	require.NoError(t, err)
	child, err := parseOrderedCoverProfile(childPath)
	require.NoError(t, err)

	require.NoError(t, mergeProcessCoverageProfiles(parent, child))

	require.Equal(t, 5, parent.lines[1].block.count)
	require.Equal(t, 1, parent.lines[2].block.count)
}

func TestMergeProcessCoverageProfilesRejectsDifferentModes(t *testing.T) {
	parent := &orderedCoverProfile{lines: []orderedCoverProfileLine{{raw: "mode: count"}}}
	child := &orderedCoverProfile{lines: []orderedCoverProfileLine{{raw: "mode: atomic"}}}
	require.Error(t, mergeProcessCoverageProfiles(parent, child))
}

func TestProcessTestCoverageUsesTestCoverageCollector(t *testing.T) {
	oldMode, oldTearDown := mode, tearDown
	t.Cleanup(func() {
		mode, tearDown = oldMode, oldTearDown
	})
	mode = "count"
	profiles := [][]byte{
		[]byte("mode: count\npkg/source.go:1.1,1.2 1 0\n"),
		[]byte("mode: count\npkg/source.go:1.1,1.2 1 1\n"),
	}
	var snapshots int
	tearDown = func(path, _ string) (string, error) {
		snapshots++
		return path, os.WriteFile(path, profiles[snapshots-1], 0o600)
	}

	collector := BeginProcessTestCoverage("pkg/source_test.go")
	require.NotNil(t, collector)
	files := collector.Finish()

	require.Equal(t, 2, snapshots)
	require.Equal(t, []ProcessTestCoverageFile{
		{Name: "pkg/source_test.go"},
		{Name: "pkg/source.go", Bitmap: []byte{0x80}},
	}, files)
}

func TestSubmitProcessTestCoverageUsesParentEventIdentifiers(t *testing.T) {
	oldMode, oldUpload, oldWriter := mode, coverageUploadEnabled, covWriter
	t.Cleanup(func() {
		mode, coverageUploadEnabled, covWriter = oldMode, oldUpload, oldWriter
	})
	mode = "count"
	coverageUploadEnabled = true
	covWriter = &coverageWriter{payload: newCoveragePayload(), climit: make(chan struct{}, 1)}

	SubmitProcessTestCoverage(11, 22, 33, []ProcessTestCoverageFile{
		{Name: "pkg/test_file.go"},
		{Name: "pkg/source.go", Bitmap: []byte{0x03}},
	})

	var got ciTestCoverages
	payload, err := io.ReadAll(covWriter.payload)
	require.NoError(t, err)
	require.NoError(t, msgp.Decode(bytes.NewReader(payload), &got))
	require.Len(t, got, 1)
	require.Equal(t, uint64(11), got[0].SessionID)
	require.Equal(t, uint64(22), got[0].SuiteID)
	require.Equal(t, uint64(33), got[0].SpanID)
	require.Equal(t, "pkg/test_file.go", got[0].Files[0].FileName)
	require.Equal(t, []byte{0x03}, got[0].Files[1].Bitmap)
}
