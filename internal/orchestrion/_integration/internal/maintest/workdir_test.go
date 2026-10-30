// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package maintest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubOtelcScript emulates the part of otelc that matters for nested-build
// serialization: each invocation takes a lock in its work directory, holds it
// for the whole build, and records in OTELC_STUB_LOG when it enters and
// leaves the locked section. The real build lock lives in the same place,
// next to .otelc-build inside the work directory.
const stubOtelcScript = `#!/usr/bin/env bash
# Stub otelc for tests: lock the work dir, log enter and exit, sleep.
wd=""
prev=""
for arg in "$@"; do
  if [[ "$prev" == "--work-dir" ]]; then
    wd="$arg"
  fi
  prev="$arg"
done
mkdir -p "$wd"
tries=0
until mkdir "$wd/stub-lock" 2>/dev/null; do
  tries=$((tries + 1))
  if [[ "$tries" -gt 200 ]]; then
    echo "stub otelc: lock timeout in $wd" >&2
    exit 1
  fi
  sleep 0.05
done
trap 'rmdir "$wd/stub-lock" 2>/dev/null' EXIT
printf 'enter %s\n' "$wd" >> "$OTELC_STUB_LOG"
sleep 1
printf 'exit %s\n' "$wd" >> "$OTELC_STUB_LOG"
exit 0
`

// installStubOtelc writes the stub otelc into a directory on PATH and returns
// the path of the log the stub appends to.
func installStubOtelc(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}
	binDir := t.TempDir()
	stub := filepath.Join(binDir, "otelc")
	require.NoError(t, os.WriteFile(stub, []byte(stubOtelcScript), 0o755))
	log := filepath.Join(t.TempDir(), "stub.log")
	require.NoError(t, os.WriteFile(log, nil, 0o644))
	t.Setenv("OTELC_STUB_LOG", log)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// readStubLog returns the enter and exit lines the stub otelc wrote.
func readStubLog(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	require.NoError(t, err)
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// assertSerialInvocations fails when two invocations held a work-directory
// lock at the same time: the enter and exit lines must alternate.
func assertSerialInvocations(t *testing.T, lines []string) {
	t.Helper()
	if len(lines)%2 != 0 {
		t.Fatalf("the stub wrote %d lines, want an even number of enter and exit lines: %q", len(lines), lines)
	}
	var workDirs []string
	for i, line := range lines {
		want := "enter"
		if i%2 == 1 {
			want = "exit"
		}
		require.Truef(t, strings.HasPrefix(line, want+" "),
			"invocation %d wrote %q, want %s: two invocations ran at the same time", i, line, want)
		workDirs = append(workDirs, strings.TrimPrefix(line, want+" "))
	}
	for _, wd := range workDirs[1:] {
		require.Equal(t, workDirs[0], wd, "nested builds used more than one work directory")
	}
}

// TestSharedWorkDir checks the work-directory rules that make the nested
// builds serialize: one stable directory per module, outside the module,
// shared by every path that names the module, and distinct per module.
func TestSharedWorkDir(t *testing.T) {
	moduleDir := t.TempDir()
	got := sharedWorkDir(moduleDir)

	require.Equal(t, got, sharedWorkDir(moduleDir), "the work directory must be stable")
	require.NotEqual(t, moduleDir, got, "the work directory must stay outside the module")
	require.Truef(t, filepath.IsAbs(got), "the work directory must be absolute: %s", got)

	link := filepath.Join(t.TempDir(), "module-alias")
	require.NoError(t, os.Symlink(moduleDir, link))
	require.Equal(t, got, sharedWorkDir(link),
		"two paths that name one module must share one work directory")

	other := t.TempDir()
	require.NotEqual(t, got, sharedWorkDir(other), "two modules must not share one work directory")
}

// TestNestedBuildsSerialize runs two nested otelc builds at the same time and
// checks that they serialize. The suites call Build from parallel test
// binaries, and every nested build of one module mutates and restores the
// module's go.mod and go.sum, so only one nested build may run at a time.
func TestNestedBuildsSerialize(t *testing.T) {
	log := installStubOtelc(t)

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Build(t, "./chi/mainapp", true)
		}()
	}
	wg.Wait()

	lines := readStubLog(t, log)
	require.Len(t, lines, 4, "two builds must produce two enter and exit pairs")
	assertSerialInvocations(t, lines)

	// The shared work directory must stay outside the module directory: the
	// outer test invocation holds the module directory's own build lock for
	// the whole test run, and nested builds must never wait on that lock.
	gomod, err := exec.Command("go", "env", "GOMOD").Output()
	require.NoError(t, err)
	moduleDir := filepath.Dir(strings.TrimSpace(string(gomod)))
	wd := strings.TrimPrefix(lines[0], "enter ")
	require.Falsef(t, strings.HasPrefix(wd, moduleDir),
		"nested builds share the module directory %s as work directory", moduleDir)
}

// TestStubDetectsOverlappingInvocations is the negative control for
// TestNestedBuildsSerialize: two stub invocations with private work
// directories overlap, and the enter and exit lines above do not alternate.
// This proves the alternation check detects a real overlap.
func TestStubDetectsOverlappingInvocations(t *testing.T) {
	log := installStubOtelc(t)

	var wg sync.WaitGroup
	for i := range 2 {
		wd := filepath.Join(t.TempDir(), fmt.Sprintf("wd-%d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command("otelc", "--work-dir", wd, "go", "build", "./x")
			out, err := cmd.CombinedOutput()
			require.NoErrorf(t, err, "stub otelc failed:\n%s", out)
		}()
	}
	wg.Wait()

	lines := readStubLog(t, log)
	require.Len(t, lines, 4)
	require.Truef(t, strings.HasPrefix(lines[1], "enter "),
		"private work directories must overlap, got %q", lines[1])
}
