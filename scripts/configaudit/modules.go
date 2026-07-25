// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

// Module identifies a Go module boundary discovered by the syntax scan.
type Module struct {
	Path string
	Dir  string
}

// discoverModules returns the module rooted at root and every nested module.
// Nested modules are independent build units and are intentionally not scanned
// as part of the root-module audit.
func discoverModules(root string) (rootModule Module, nested []Module, err error) {
	root, err = filepath.Abs(root)
	if err != nil {
		return Module{}, nil, fmt.Errorf("resolving root: %w", err)
	}
	rootModule, err = readModule(root)
	if err != nil {
		return Module{}, nil, err
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() || path == root {
			return nil
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		if _, statErr := os.Stat(filepath.Join(path, "go.mod")); statErr != nil {
			if os.IsNotExist(statErr) {
				return nil
			}
			return statErr
		}
		module, readErr := readModule(path)
		if readErr != nil {
			return readErr
		}
		nested = append(nested, module)
		return nil
	})
	if err != nil {
		return Module{}, nil, fmt.Errorf("discovering nested modules: %w", err)
	}
	return rootModule, nested, nil
}

func buildAuditScope(root string) (AuditScope, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return AuditScope{}, fmt.Errorf("resolving audit scope root: %w", err)
	}
	rootModule, nested, err := discoverModules(absoluteRoot)
	if err != nil {
		return AuditScope{}, err
	}
	scope := AuditScope{
		RootModule:      rootModule.Path,
		ExcludedModules: make([]ScopeModule, 0, len(nested)),
	}
	seenDirs := make(map[string]struct{}, len(nested))
	for _, module := range nested {
		rel, err := filepath.Rel(absoluteRoot, module.Dir)
		if err != nil {
			return AuditScope{}, fmt.Errorf("relativizing nested module %q: %w", module.Dir, err)
		}
		dir := filepath.ToSlash(rel)
		if module.Path == "" || dir == "" || dir == "." || filepath.IsAbs(rel) || strings.HasPrefix(dir, "../") {
			return AuditScope{}, fmt.Errorf("invalid nested module scope path=%q dir=%q", module.Path, dir)
		}
		if _, duplicate := seenDirs[dir]; duplicate {
			return AuditScope{}, fmt.Errorf("duplicate nested module scope directory %q", dir)
		}
		seenDirs[dir] = struct{}{}
		scope.ExcludedModules = append(scope.ExcludedModules, ScopeModule{Path: module.Path, Dir: dir})
	}
	sort.Slice(scope.ExcludedModules, func(i, j int) bool {
		return scope.ExcludedModules[i].Dir < scope.ExcludedModules[j].Dir
	})
	return scope, nil
}

func readModule(dir string) (Module, error) {
	path := filepath.Join(dir, "go.mod")
	contents, err := os.ReadFile(path)
	if err != nil {
		return Module{}, fmt.Errorf("reading %s: %w", path, err)
	}
	parsed, err := modfile.Parse(path, contents, nil)
	if err != nil {
		return Module{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if parsed.Module == nil {
		return Module{}, fmt.Errorf("%s has no module declaration", path)
	}
	return Module{Path: parsed.Module.Mod.Path, Dir: dir}, nil
}
