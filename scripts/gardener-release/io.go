// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"errors"
	"io/fs"
	"os"
)

type FileSystem interface {
	ReadFile(name string) ([]byte, error)
}

type OSFileSystem struct{}

func (OSFileSystem) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func ReadBoundedFile(files FileSystem, path string, maxBytes int) ([]byte, error) {
	data, err := files.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, newReleaseError(ErrorClassEvidenceIncomplete, "file_not_found")
		}
		return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "file_read_failed", err)
	}
	if len(data) > maxBytes {
		return nil, newReleaseError(ErrorClassEvidenceIncomplete, "file_too_large")
	}
	return data, nil
}
