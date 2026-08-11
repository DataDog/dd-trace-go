// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"bytes"
	"errors"
	"fmt"
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

func TestMergeProcessCoverageProfilesRejectsDifferentBlocks(t *testing.T) {
	parent := processCoverageProfileForTest(t, "count", 1)
	childPath := filepath.Join(t.TempDir(), "child.out")
	require.NoError(t, os.WriteFile(childPath, []byte("mode: count\npkg/other.go:1.1,1.2 1 1\n"), 0o600))
	child, err := parseOrderedCoverProfile(childPath)
	require.NoError(t, err)
	require.Error(t, mergeProcessCoverageProfiles(parent, child))
}

func TestFinalizeProcessCoverageProfilesWaitsForParentProfile(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)
	dir := t.TempDir()
	parentPath := filepath.Join(dir, "parent.out")
	firstChildPath := filepath.Join(dir, "first-child.out")
	secondChildPath := filepath.Join(dir, "second-child.out")
	require.NoError(t, os.WriteFile(parentPath, []byte("mode: count\npkg/file.go:1.1,1.2 1 2\n"), 0o600))
	require.NoError(t, os.WriteFile(firstChildPath, []byte("mode: count\npkg/file.go:1.1,1.2 1 3\n"), 0o600))
	require.NoError(t, os.WriteFile(secondChildPath, []byte("mode: count\npkg/file.go:1.1,1.2 1 4\n"), 0o600))
	mode = "count"
	tearDown = func(_, _ string) (string, error) {
		return "", errors.New("runtime snapshot should already exist")
	}
	runtimeSnapshot = &runtimeCoverageSnapshot{path: parentPath}

	require.NoError(t, MergeProcessCoverageProfile(firstChildPath))
	require.NoError(t, MergeProcessCoverageProfile(secondChildPath))
	beforeFinalization, err := os.ReadFile(parentPath)
	require.NoError(t, err)
	require.Contains(t, string(beforeFinalization), " 2\n")

	merged, err := FinalizeProcessCoverageProfiles()
	require.NoError(t, err)
	require.True(t, merged)
	parent, err := parseOrderedCoverProfile(parentPath)
	require.NoError(t, err)
	require.Equal(t, 9, parent.lines[1].block.count)

	merged, err = FinalizeProcessCoverageProfiles()
	require.NoError(t, err)
	require.False(t, merged)
}

func TestFinalizeProcessCoverageProfilesRetainsInvocationMergeFailure(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)
	mode = "count"
	tearDown = func(_, _ string) (string, error) {
		return "", errors.New("runtime snapshot must not be published")
	}
	childPath := filepath.Join(t.TempDir(), "malformed-child.out")
	require.NoError(t, os.WriteFile(childPath, []byte("not a coverage profile\n"), 0o600))

	mergeErr := MergeProcessCoverageProfile(childPath)
	require.Error(t, mergeErr)
	merged, err := FinalizeProcessCoverageProfiles()
	require.False(t, merged)
	require.EqualError(t, err, mergeErr.Error())
}

func TestProcessCoverageProfileWritesOnlyWorkloadDelta(t *testing.T) {
	oldMode, oldTearDown := mode, tearDown
	t.Cleanup(func() { mode, tearDown = oldMode, oldTearDown })
	mode = "count"
	profiles := [][]byte{
		[]byte("mode: count\npkg/source.go:1.1,1.2 1 2\npkg/source.go:2.1,2.2 1 0\n"),
		[]byte("mode: count\npkg/source.go:1.1,1.2 1 2\npkg/source.go:2.1,2.2 1 3\n"),
	}
	var snapshots int
	tearDown = func(path, _ string) (string, error) {
		profile := profiles[snapshots]
		snapshots++
		return path, os.WriteFile(path, profile, 0o600)
	}

	profile, err := BeginProcessCoverageProfile()
	require.NoError(t, err)
	output := filepath.Join(t.TempDir(), "delta.out")
	require.NoError(t, profile.WriteDelta(output))

	delta, err := parseOrderedCoverProfile(output)
	require.NoError(t, err)
	require.Equal(t, 0, delta.lines[1].block.count)
	require.Equal(t, 3, delta.lines[2].block.count)
	require.Equal(t, 2, snapshots)
}

func TestProcessCoverageProfilePauseExcludesSuspendedCoverage(t *testing.T) {
	oldMode, oldTearDown := mode, tearDown
	t.Cleanup(func() { mode, tearDown = oldMode, oldTearDown })
	mode = "count"
	counts := [][3]int{
		{0, 0, 0}, // first segment starts
		{1, 0, 0}, // first segment finishes
		{1, 1, 0}, // ancestor work runs while the isolated root is paused
		{1, 1, 1}, // resumed segment finishes
	}
	var snapshots int
	tearDown = func(path, _ string) (string, error) {
		count := counts[snapshots]
		snapshots++
		profile := fmt.Appendf(nil, "mode: count\npkg/source.go:1.1,1.2 1 %d\npkg/source.go:2.1,2.2 1 %d\npkg/source.go:3.1,3.2 1 %d\n", count[0], count[1], count[2])
		return path, os.WriteFile(path, profile, 0o600)
	}

	profile, err := BeginProcessCoverageProfile()
	require.NoError(t, err)
	require.NoError(t, profile.Pause())
	require.NoError(t, profile.Resume())
	output := filepath.Join(t.TempDir(), "delta.out")
	require.NoError(t, profile.WriteDelta(output))

	delta, err := parseOrderedCoverProfile(output)
	require.NoError(t, err)
	require.Equal(t, 1, delta.lines[1].block.count)
	require.Equal(t, 0, delta.lines[2].block.count)
	require.Equal(t, 1, delta.lines[3].block.count)
	require.Equal(t, 4, snapshots)
}

func TestSubtractProcessCoverageProfilesUsesModeSemantics(t *testing.T) {
	for _, tt := range []struct {
		mode        string
		beforeCount int
		afterCount  int
		want        int
	}{
		{mode: "count", beforeCount: 4, afterCount: 7, want: 3},
		{mode: "atomic", beforeCount: 4, afterCount: 7, want: 3},
		{mode: "set", beforeCount: 1, afterCount: 1, want: 0},
		{mode: "set", beforeCount: 0, afterCount: 1, want: 1},
	} {
		t.Run(fmt.Sprintf("%s/%d-%d", tt.mode, tt.beforeCount, tt.afterCount), func(t *testing.T) {
			before := processCoverageProfileForTest(t, tt.mode, tt.beforeCount)
			after := processCoverageProfileForTest(t, tt.mode, tt.afterCount)
			require.NoError(t, subtractProcessCoverageProfiles(before, after))
			require.Equal(t, tt.want, after.lines[1].block.count)
		})
	}
}

func processCoverageProfileForTest(t *testing.T, profileMode string, count int) *orderedCoverProfile {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.out")
	require.NoError(t, os.WriteFile(path, fmt.Appendf(nil, "mode: %s\npkg/source.go:1.1,1.2 1 %d\n", profileMode, count), 0o600))
	profile, err := parseOrderedCoverProfile(path)
	require.NoError(t, err)
	return profile
}

func TestProcessTestCoverageSupportsNestedInclusiveCollectors(t *testing.T) {
	oldMode, oldTearDown := mode, tearDown
	t.Cleanup(func() { mode, tearDown = oldMode, oldTearDown })
	mode = "count"
	counts := []int{0, 1, 2, 3}
	var snapshots int
	tearDown = func(path, _ string) (string, error) {
		count := counts[snapshots]
		snapshots++
		return path, os.WriteFile(path, fmt.Appendf(nil, "mode: count\npkg/source.go:1.1,1.2 1 %d\n", count), 0o600)
	}

	outer := BeginProcessTestCoverage("pkg/outer_test.go")
	inner := BeginProcessTestCoverage("pkg/inner_test.go")
	require.NotNil(t, outer)
	require.NotNil(t, inner)
	innerFiles := inner.Finish()
	outerFiles := outer.Finish()

	require.Equal(t, 4, snapshots)
	require.Equal(t, []ProcessTestCoverageFile{{Name: "pkg/inner_test.go"}, {Name: "pkg/source.go", Bitmap: []byte{0x80}}}, innerFiles)
	require.Equal(t, []ProcessTestCoverageFile{{Name: "pkg/outer_test.go"}, {Name: "pkg/source.go", Bitmap: []byte{0x80}}}, outerFiles)
}

func TestProcessTestCoveragePauseExcludesSuspendedCoverage(t *testing.T) {
	oldMode, oldTearDown := mode, tearDown
	t.Cleanup(func() { mode, tearDown = oldMode, oldTearDown })
	mode = "count"
	counts := [][3]int{
		{0, 0, 0}, // first segment starts
		{1, 0, 0}, // first segment finishes
		{1, 1, 0}, // another test runs while this collector is paused
		{1, 1, 1}, // resumed segment finishes
	}
	var snapshots int
	tearDown = func(path, _ string) (string, error) {
		count := counts[snapshots]
		snapshots++
		profile := fmt.Appendf(nil, "mode: count\npkg/source.go:1.1,1.2 1 %d\npkg/source.go:2.1,2.2 1 %d\npkg/source.go:3.1,3.2 1 %d\n", count[0], count[1], count[2])
		return path, os.WriteFile(path, profile, 0o600)
	}

	collector := BeginProcessTestCoverage("pkg/test_file.go")
	require.NotNil(t, collector)
	collector.Pause()
	collector.Resume()
	files := collector.Finish()

	require.Equal(t, 4, snapshots)
	require.Equal(t, []ProcessTestCoverageFile{{Name: "pkg/test_file.go"}, {Name: "pkg/source.go", Bitmap: []byte{0xa0}}}, files)
}

func TestSubmitProcessTestCoverageUsesParentEventIdentifiers(t *testing.T) {
	oldMode, oldUpload, oldWriter := mode, coverageUploadEnabled, covWriter
	t.Cleanup(func() { mode, coverageUploadEnabled, covWriter = oldMode, oldUpload, oldWriter })
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
}
