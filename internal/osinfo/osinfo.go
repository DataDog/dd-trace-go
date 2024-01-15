// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package osinfo provides information about the current operating system release
package osinfo

import (
	"io/fs"
	"os"
	"path"
	"runtime"
	"sync"
)

// OSName returns the name of the operating system, including the distribution
// for Linux when possible.
func OSName() string {
	// call out to OS-specific implementation
	return osName()
}

// OSVersion returns the operating system release, e.g. major/minor version
// number and build ID.
func OSVersion() string {
	// call out to OS-specific implementation
	return osVersion()
}

var (
	detectLibDlOnce sync.Once
	detectedLibDl   string
)

// DetectLibDl returns the path to libdl.so if present under one of the
// traditional library paths (under `/lib`, `/lib64`, `/usr/lib`, etc...). This
// is a no-op on platforms other than Linux. The lookup is done exactly once,
// and the result will be re-used by subsequent calls.
func DetectLibDl(root string) string {
	if runtime.GOOS != "linux" {
		return ""
	}

	detectLibDlOnce.Do(func() { detectedLibDl = detectLibDl(root, os.DirFS(root)) })
	return detectedLibDl
}

// detectLibDl is the testable implementation of DetectLibDl.
func detectLibDl(root string, fsys fs.FS) string {
	prefixes := [...]string{
		"lib",
		"lib64",
		"usr/lib",
		"usr/lib64",
		"usr/local/lib",
		"usr/local/lib64",
	}

	var result string
	for _, prefix := range prefixes {
		_ = fs.WalkDir(fsys, prefix, func(path string, dir fs.DirEntry, err error) error {
			if dir == nil || dir.IsDir() || dir.Name() != "libdl.so.2" {
				return nil
			}
			// Found it!
			result = path
			return fs.SkipDir // TODO: fs.SkipAll once 1.20+ is the minimum supported Go version
		})
		if result != "" {
			return path.Join(root, result)
		}
	}
	return ""
}
