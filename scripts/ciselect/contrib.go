// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

// Sentinels the contrib matrix reads. Both are needed: an empty list has to be
// distinguishable from an absent one, or a shell default like ${VAR:-ALL}
// silently turns "test nothing" into "test everything".
const (
	selectAll  = "ALL"
	selectNone = "NONE"
)

// repoModule maps a module path to its directory, relative to the repository
// root and slash-separated.
type repoModule struct {
	Path string
	Dir  string
}

// loadModules finds every go.mod under contrib/ and instrumentation/, plus the
// root module. Modules outside those trees are not testable by
// scripts/ci_test_contrib.sh, so they are not matrix candidates.
func loadModules(root string) (byPath map[string]repoModule, byDir map[string]repoModule, err error) {
	byPath = map[string]repoModule{}
	byDir = map[string]repoModule{}

	add := func(dir string) error {
		data, err := os.ReadFile(filepath.Join(root, dir, "go.mod"))
		if err != nil {
			return err
		}
		f, err := modfile.Parse(filepath.Join(dir, "go.mod"), data, nil)
		if err != nil {
			return err
		}
		if f.Module == nil {
			return fmt.Errorf("%s/go.mod has no module directive", dir)
		}
		m := repoModule{Path: f.Module.Mod.Path, Dir: dir}
		byPath[m.Path] = m
		byDir[m.Dir] = m
		return nil
	}

	if err := add("."); err != nil {
		return nil, nil, err
	}
	for _, tree := range []string{"contrib", "instrumentation"} {
		walkErr := filepath.WalkDir(filepath.Join(root, tree), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || d.Name() != "go.mod" {
				return nil
			}
			rel, err := filepath.Rel(root, filepath.Dir(p))
			if err != nil {
				return err
			}
			return add(filepath.ToSlash(rel))
		})
		if walkErr != nil {
			return nil, nil, walkErr
		}
	}
	return byPath, byDir, nil
}

// enclosingModule walks up from file to the nearest directory holding a
// candidate go.mod.
//
// The upward walk is what keeps contrib/os/ honest: it has no go.mod at any
// depth, so the walk reaches the root module and the caller escalates. A
// prefix match on "contrib/" would instead have classified root-module code as
// contrib-scoped.
func enclosingModule(byDir map[string]repoModule, file string) (repoModule, bool) {
	dir := filepath.ToSlash(filepath.Dir(file))
	for {
		if m, ok := byDir[dir]; ok {
			return m, m.Dir != "."
		}
		idx := strings.LastIndex(dir, "/")
		if idx < 0 {
			break
		}
		dir = dir[:idx]
	}
	// Nothing along the way owns it, so it belongs to the root module. The
	// second return value is what callers act on: false means "do not narrow".
	return byDir["."], false
}

// reverseDeps inverts the intra-repo require graph.
//
// The root-module edge is excluded on purpose: every contrib requires
// github.com/DataDog/dd-trace-go/v2, so keeping it would expand any seed to
// the entire matrix and make narrowing pointless. Root-module changes are
// already handled upstream -- they match the `core` component, which enables
// every gate and sets RunAll.
func reverseDeps(root string, byPath map[string]repoModule) (map[string][]string, error) {
	rootPath := ""
	if m, ok := byPathDir(byPath, "."); ok {
		rootPath = m.Path
	}
	rev := map[string][]string{}
	for _, m := range byPath {
		if m.Dir == "." {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, m.Dir, "go.mod"))
		if err != nil {
			return nil, err
		}
		f, err := modfile.Parse(filepath.Join(m.Dir, "go.mod"), data, nil)
		if err != nil {
			return nil, err
		}
		for _, req := range f.Require {
			if req.Mod.Path == rootPath {
				continue
			}
			dep, ok := byPath[req.Mod.Path]
			if !ok {
				continue
			}
			rev[dep.Dir] = append(rev[dep.Dir], m.Dir)
		}
	}
	for k := range rev {
		sort.Strings(rev[k])
	}
	return rev, nil
}

func byPathDir(byPath map[string]repoModule, dir string) (repoModule, bool) {
	for _, m := range byPath {
		if m.Dir == dir {
			return m, true
		}
	}
	return repoModule{}, false
}

// selectContribModules resolves a classification into the set of contrib and
// instrumentation module directories whose tests must run, or selectAll.
func selectContribModules(root string, r *result) ([]string, error) {
	if r.RunAll {
		return []string{selectAll}, nil
	}
	byPath, byDir, err := loadModules(root)
	if err != nil {
		return nil, err
	}

	seeds := map[string]bool{}
	for _, file := range r.SeedPaths {
		m, ok := enclosingModule(byDir, file)
		if !ok {
			// Inside a module-scoped tree but owned by no submodule, so it is
			// root-module code. Refuse to narrow.
			return []string{selectAll}, nil
		}
		seeds[m.Dir] = true
	}
	for _, dir := range r.DependentModules {
		if _, ok := byDir[dir]; ok {
			seeds[dir] = true
		}
	}
	if len(seeds) == 0 {
		return nil, nil
	}

	rev, err := reverseDeps(root, byPath)
	if err != nil {
		return nil, err
	}
	queue := make([]string, 0, len(seeds))
	for d := range seeds {
		queue = append(queue, d)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, dependent := range rev[cur] {
			if !seeds[dependent] {
				seeds[dependent] = true
				queue = append(queue, dependent)
			}
		}
	}

	out := make([]string, 0, len(seeds))
	for d := range seeds {
		if d == "." {
			return []string{selectAll}, nil
		}
		out = append(out, d)
	}
	sort.Strings(out)
	return out, nil
}
