// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/telemetry"
)

// A linked worktree and a submodule have a .git file instead of a directory.
// Their own checkout root is the safe.directory that git asks for.
func TestSafeDirectoryConfigAcceptsGitFiles(t *testing.T) {
	isolateFixtureGit(t)
	root := t.TempDir()
	main := filepath.Join(root, "main")
	runFixtureGit(t, root, "init", main)
	runFixtureGit(t, main, "commit", "--allow-empty", "-m", "base")
	worktree := filepath.Join(root, "worktree")
	runFixtureGit(t, main, "worktree", "add", "--detach", worktree)
	library := filepath.Join(root, "library")
	runFixtureGit(t, root, "init", library)
	runFixtureGit(t, library, "commit", "--allow-empty", "-m", "library")
	runFixtureGit(t, main, "-c", "protocol.file.allow=always", "submodule", "add", library, "vendor/library")
	submodule := filepath.Join(main, "vendor", "library")

	for _, tc := range []struct{ name, checkout string }{
		{name: "repository", checkout: main},
		{name: "worktree", checkout: worktree},
		{name: "submodule", checkout: submodule},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nested := filepath.Join(tc.checkout, "pkg", "nested")
			require.NoError(t, os.MkdirAll(nested, 0o755))
			t.Chdir(nested)
			resetGitCommandCachesForTesting(t)
			got, err := filepath.EvalSymlinks(getSafeDirectoryConfig())
			require.NoError(t, err, "safe.directory %q", getSafeDirectoryConfig())
			want, err := filepath.EvalSymlinks(tc.checkout)
			require.NoError(t, err)
			require.Equal(t, want, got)
			_, err = execGitString(telemetry.NotSpecifiedCommandsType, "rev-parse", "--show-toplevel")
			require.NoError(t, err, "git with safe.directory")
		})
	}
}

// isolateFixtureGit makes git ignore the caller's configuration and the
// variables that select a repository, which git sets for hooks, so fixture
// commands never reach the caller's repository. t.Setenv restores them.
func isolateFixtureGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES"} {
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}
}

// runFixtureGit runs real git with isolated configuration for
// test setup.
func runFixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	args = append([]string{"-c", "user.name=CI Fixture", "-c", "user.email=ci-fixture@example.invalid", "-c", "commit.gpgsign=false", "-c", "init.templateDir=", "-c", "init.defaultBranch=main"}, args...)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}
