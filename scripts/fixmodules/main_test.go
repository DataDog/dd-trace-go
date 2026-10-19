// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product contains software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build scripts

package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestGetModGoFilesNestedModules checks that the walk survives nested modules
// regardless of where they sort relative to the parent's files. scripts/errtrackaudit
// was the first module with several nested testdata modules; the previous walk
// returned fs.SkipAll at the first nested go.mod, aborting the whole walk, so
// parent files sorting after it were dropped and later nested modules were
// never recorded.
func TestGetModGoFilesNestedModules(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Parent module. "aaa" sorts before "go.mod" and "z.go", so the walk meets
	// the first nested module before the parent's own go.mod.
	write("go.mod", "module example.com/m\n\ngo 1.26\n")
	write("main.go", "package m\n")
	write("z.go", "package m\n")

	// First nested module, met before the parent's go.mod.
	write("aaa/go.mod", "module example.com/m/aaa\n\ngo 1.26\n")
	write("aaa/a.go", "package aaa\n") // sorts before aaa/go.mod, filtered by prefix
	write("aaa/zz.go", "package aaa\n") // sorts after aaa/go.mod, skipped by fs.SkipDir
	write("aaa/sub/s.go", "package sub\n") // in a skipped subtree

	// Second nested module, sorting after every parent file: the old fs.SkipAll
	// abort recorded only the first nested module, never this one.
	write("zzz/go.mod", "module example.com/m/zzz\n\ngo 1.26\n")
	write("zzz/z.go", "package zzz\n")

	// A sibling directory sharing the first nested module's name prefix but
	// without its own go.mod: its files belong to the parent module.
	write("aaaextra/x.go", "package main\n")

	files, err := getModGoFiles(GoMod{dir: dir})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		filepath.Join(dir, "aaaextra", "x.go"),
		filepath.Join(dir, "main.go"),
		filepath.Join(dir, "z.go"),
	}
	slices.Sort(files)
	if !slices.Equal(files, want) {
		t.Errorf("getModGoFiles() = %v\nwant %v", files, want)
	}
}
