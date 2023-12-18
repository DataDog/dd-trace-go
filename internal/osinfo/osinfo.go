// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package osinfo provides information about the current operating system release
package osinfo

import (
	"fmt"
	"io/fs"
	"os"
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

// DetectLibDl returns the path to libdl.so.2 if present in /lib or /lib64,
// otherwise returns an empty string.
func DetectLibDl(root string) (path string) {
	return detectLibDl(root, os.DirFS(root))
}

// detectLibDl is the testable implementation of DetectLibDl.
func detectLibDl(root string, fsys fs.FS) (path string) {
	dirs := [...]string{
		"lib",
		"lib64",
	}
	for _, dir := range dirs {
		path := fmt.Sprintf("%s/libdl.so.2", dir)
		if _, err := fs.Stat(fsys, path); os.IsNotExist(err) || err != nil {
			continue
		}
		return root + path
	}
	return ""
}
