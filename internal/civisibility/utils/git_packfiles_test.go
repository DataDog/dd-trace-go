// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pack files must not outlive their upload, and their temporary directory must
// never be created in the working tree: a temporary directory on another device
// falls back to the git objects directory, where git writes the pack it moves.

// usePackFilesGitFixture changes to a new repository with one commit, so the
// tests do not depend on the checkout's history or layout.
func usePackFilesGitFixture(t *testing.T) (repo string) {
	t.Helper()
	isolateFixtureGit(t)
	repo = t.TempDir()
	runFixtureGit(t, repo, "init")
	runFixtureGit(t, repo, "commit", "--allow-empty", "-m", "fixture")
	t.Chdir(repo)
	resetGitCommandCachesForTesting(t)
	return repo
}

// useTempDir makes os.TempDir return dir on every platform.
func useTempDir(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, dir)
	}
}

// packDirectories lists the pack-file temporary directories directly in dir.
func packDirectories(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var found []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".dd-pack-objects") {
			found = append(found, entry.Name())
		}
	}
	return found
}

func sameDirectory(t *testing.T, a, b string) bool {
	t.Helper()
	a, err := filepath.EvalSymlinks(a)
	require.NoError(t, err)
	b, err = filepath.EvalSymlinks(b)
	require.NoError(t, err)
	return a == b
}

func TestRemovePackFilesRemovesTemporaryDirectory(t *testing.T) {
	repo := usePackFilesGitFixture(t)
	tmp := t.TempDir()
	useTempDir(t, tmp)

	files := CreatePackFiles(GetLastLocalGitCommitShas(), nil)
	require.NotEmpty(t, files)
	for _, file := range files {
		require.FileExists(t, file)
	}
	assert.True(t, sameDirectory(t, tmp, filepath.Dir(filepath.Dir(files[0]))))
	assert.Empty(t, packDirectories(t, repo), "working tree received a pack directory")

	// git also writes index files next to each pack; nothing may remain.
	RemovePackFiles(files)
	assert.Empty(t, packDirectories(t, tmp))
}

func TestPackFilesFallBackToObjectsDirectory(t *testing.T) {
	repo := usePackFilesGitFixture(t)
	// The temporary directory cannot be created, as when git cannot move its
	// pack there from another device.
	useTempDir(t, filepath.Join(t.TempDir(), "missing"))

	files := CreatePackFiles(GetLastLocalGitCommitShas(), nil)
	require.NotEmpty(t, files)
	objectsDir := filepath.Join(repo, ".git", "objects")
	assert.True(t, sameDirectory(t, objectsDir, filepath.Dir(filepath.Dir(files[0]))))
	assert.Empty(t, packDirectories(t, repo), "working tree received a pack directory")

	RemovePackFiles(files)
	assert.Empty(t, packDirectories(t, objectsDir))
}

// GIT_OBJECT_DIRECTORY can put the objects on another filesystem than the git
// directory; the fallback follows the objects.
func TestPackFilesFallBackToMovedObjectsDirectory(t *testing.T) {
	repo := usePackFilesGitFixture(t)
	objectsDir := filepath.Join(t.TempDir(), "objects")
	require.NoError(t, os.Rename(filepath.Join(repo, ".git", "objects"), objectsDir))
	t.Setenv("GIT_OBJECT_DIRECTORY", objectsDir)
	useTempDir(t, filepath.Join(t.TempDir(), "missing"))

	files := CreatePackFiles(GetLastLocalGitCommitShas(), nil)
	require.NotEmpty(t, files)
	assert.True(t, sameDirectory(t, objectsDir, filepath.Dir(filepath.Dir(files[0]))))
	assert.Empty(t, packDirectories(t, filepath.Join(repo, ".git")))

	RemovePackFiles(files)
	assert.Empty(t, packDirectories(t, objectsDir))
}

// installFakeGit puts a fake git first in PATH and returns the file where it
// records each command. It skips the options before the command, which cmd
// also splits at '=' for git.bat, answers rev-list with one object and
// rev-parse with a temporary objects directory, and for pack-objects either
// fails or names a pack without writing it.
func installFakeGit(t *testing.T, packObjectsFails bool) (log, objectsDir string) {
	t.Helper()
	bin := t.TempDir()
	objectsDir, log = t.TempDir(), filepath.Join(t.TempDir(), "commands")
	t.Setenv("FAKE_GIT_OBJECTS_DIR", objectsDir)
	t.Setenv("FAKE_GIT_LOG", log)
	shPack, batPack := `printf 'packhash\n'`, "echo packhash"
	if packObjectsFails {
		shPack, batPack = "exit 1", "exit /b 1"
	}
	scripts := map[string]string{
		"git": `#!/bin/sh
while [ $# -gt 0 ]; do
	case "$1" in
		rev-list|rev-parse|pack-objects) break ;;
	esac
	shift
done
[ $# -gt 0 ] || exit 0
printf '%s\n' "$1" >> "$FAKE_GIT_LOG"
case "$1" in
	rev-list) printf 'abc123\n' ;;
	rev-parse) printf '%s\n' "$FAKE_GIT_OBJECTS_DIR" ;;
	pack-objects) ` + shPack + ` ;;
esac
exit 0
`,
		"git.bat": `@echo off
:next
if "%~1"=="" exit /b 0
if "%~1"=="rev-list" goto found
if "%~1"=="rev-parse" goto found
if "%~1"=="pack-objects" goto found
shift
goto next
:found
>>"%FAKE_GIT_LOG%" echo %~1
if "%~1"=="rev-list" echo abc123
if "%~1"=="rev-parse" echo %FAKE_GIT_OBJECTS_DIR%
if "%~1"=="pack-objects" ` + batPack + `
exit /b 0
`,
	}
	for name, script := range scripts {
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755))
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log, objectsDir
}

// fakeGitCommands counts the commands the fake git recorded.
func fakeGitCommands(t *testing.T, log string) map[string]int {
	t.Helper()
	data, err := os.ReadFile(log)
	require.NoError(t, err)
	counts := map[string]int{}
	for command := range strings.FieldsSeq(string(data)) {
		counts[command]++
	}
	return counts
}

func TestFailedPackFilesLeaveNoDirectory(t *testing.T) {
	resetGitCommandCachesForTesting(t)
	tmp := t.TempDir()
	useTempDir(t, tmp)
	log, objectsDir := installFakeGit(t, true)

	// Both attempts fail: each removes its directory.
	assert.Empty(t, CreatePackFiles([]string{"HEAD"}, nil))
	assert.Equal(t, 2, fakeGitCommands(t, log)["pack-objects"], "both attempts must run")
	assert.Empty(t, packDirectories(t, tmp))
	assert.Empty(t, packDirectories(t, objectsDir))
}

func TestMissingPackFilesLeaveNoDirectory(t *testing.T) {
	resetGitCommandCachesForTesting(t)
	tmp := t.TempDir()
	useTempDir(t, tmp)
	log, _ := installFakeGit(t, false)

	// pack-objects names a pack that it did not write.
	assert.Empty(t, CreatePackFiles([]string{"HEAD"}, nil))
	assert.Equal(t, 1, fakeGitCommands(t, log)["pack-objects"])
	assert.Empty(t, packDirectories(t, tmp))
}

// pack-objects moves each pack out of the git objects directory. A temporary
// directory on another filesystem would fail first and repeat the whole pack,
// so the objects directory is used directly.
func TestPackObjectsSkipsTemporaryDirectoryOnAnotherFilesystem(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs two filesystems; /proc is one on Linux")
	}
	repo := usePackFilesGitFixture(t)
	objectsDir := filepath.Join(repo, ".git", "objects")

	useTempDir(t, t.TempDir())
	folders := packObjectsFolders()
	require.Len(t, folders, 2)
	assert.True(t, sameDirectory(t, objectsDir, folders[1]))

	useTempDir(t, "/proc")
	folders = packObjectsFolders()
	require.Len(t, folders, 1)
	assert.True(t, sameDirectory(t, objectsDir, folders[0]))
}
