// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package osinfo

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestDetectLibDl(t *testing.T) {
	testCases := []struct {
		expectedPath string
		testFs       fstest.MapFS
	}{
		{
			expectedPath: "/lib/libdl.so.2",
			testFs: fstest.MapFS{
				"lib/libdl.so.2": &fstest.MapFile{},
			},
		},
		{
			expectedPath: "/lib64/libdl.so.2",
			testFs: fstest.MapFS{
				"lib64/libdl.so.2": &fstest.MapFile{},
			},
		},
		{
			expectedPath: "",
			testFs: fstest.MapFS{
				"nope/libdl.so.2": &fstest.MapFile{},
			},
		},
		{
			expectedPath: "/lib/arm64-linux-gnu/libdl.so.2",
			testFs: fstest.MapFS{
				"lib/arm64-linux-gnu/libdl.so.2": &fstest.MapFile{},
			},
		},
	}
	for _, tc := range testCases {
		got := detectLibDl("/", tc.testFs)
		require.Equal(t, tc.expectedPath, got)
	}
}
