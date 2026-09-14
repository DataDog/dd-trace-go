// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"strings"
)

// GitBlobReader reads a path's blob content at a specific commit through
// `git cat-file`/`git show` plumbing only, using dir as a Git repository
// that already has both commits available (a checkout produced by
// Generate satisfies this for both the source and generated commit, since
// Generate fetches the source commit into the same local repository it
// commits into). It never checks out, executes, or interprets the blob's
// contents.
type GitBlobReader struct {
	Runner CommandRunner
	Dir    string
}

// Read implements modFileReader.
func (r GitBlobReader) Read(ctx context.Context, commitSHA, path string) ([]byte, bool, error) {
	if !ValidGitObjectID(commitSHA) {
		return nil, false, typedContractError("invalid_git_object")
	}
	if strings.Contains(path, "..") || strings.HasPrefix(path, "/") {
		return nil, false, typedContractError("unsafe_path")
	}
	existsResult, existsErr := r.Runner.Run(ctx, Command{
		Path: "git",
		Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "cat-file", "-e", commitSHA + ":" + path},
		Env:  gitStateEnv(),
		Dir:  r.Dir,
	})
	_ = existsResult
	if existsErr != nil {
		return nil, false, nil
	}
	result, err := r.Runner.Run(ctx, Command{
		Path: "git",
		Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "show", commitSHA + ":" + path},
		Env:  gitStateEnv(),
		Dir:  r.Dir,
	})
	if err != nil {
		return nil, false, wrapReleaseError(ErrorClassEvidenceIncomplete, "git_read_failed", err)
	}
	return []byte(result.Stdout), true, nil
}
