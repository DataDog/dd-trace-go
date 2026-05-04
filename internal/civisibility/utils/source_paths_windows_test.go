//go:build windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

import (
	"path/filepath"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"

	"github.com/stretchr/testify/assert"
)

func TestResolveSourceFilePathWindows(t *testing.T) {
	tests := []struct {
		name               string
		workspace          string
		runtimePath        string
		expectedRelative   string
		expectedFilesystem string
	}{
		{
			name:               "same drive inside workspace",
			workspace:          `C:\workspace`,
			runtimePath:        `C:\workspace\pkg\foo_test.go`,
			expectedRelative:   "pkg/foo_test.go",
			expectedFilesystem: `C:\workspace\pkg\foo_test.go`,
		},
		{
			name:               "different drive outside workspace",
			workspace:          `C:\workspace`,
			runtimePath:        `D:\src\pkg\foo_test.go`,
			expectedRelative:   "D:/src/pkg/foo_test.go",
			expectedFilesystem: `D:\src\pkg\foo_test.go`,
		},
		{
			name:               "same UNC share inside workspace",
			workspace:          `\\server\share\workspace`,
			runtimePath:        `\\server\share\workspace\pkg\foo_test.go`,
			expectedRelative:   "pkg/foo_test.go",
			expectedFilesystem: `\\server\share\workspace\pkg\foo_test.go`,
		},
		{
			name:               "different UNC share outside workspace",
			workspace:          `\\server\share\workspace`,
			runtimePath:        `\\server\other\workspace\pkg\foo_test.go`,
			expectedRelative:   "//server/other/workspace/pkg/foo_test.go",
			expectedFilesystem: `\\server\other\workspace\pkg\foo_test.go`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveSourceFilePath(tt.runtimePath, map[string]string{constants.CIWorkspacePath: tt.workspace}, "")

			assert.Equal(t, tt.expectedRelative, result.RelativePath)
			assert.Equal(t, filepath.Clean(tt.expectedFilesystem), filepath.Clean(result.FilesystemPath))
			assert.True(t, result.FilesystemKnown)
		})
	}
}
