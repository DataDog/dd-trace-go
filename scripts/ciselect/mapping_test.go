// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// goImportRef matches a quoted import path of this repository, in any of its
// modules: the root module, a contrib module, or an instrumentation module.
var goImportRef = regexp.MustCompile(`"(github\.com/DataDog/dd-trace-go/[^"]+)"`)

// owningModuleDir resolves an import path to the module directory publishing
// it, by longest module-path prefix. The root module is not a mapping key,
// so it reports false.
func owningModuleDir(byPath map[string]repoModule, importPath string) (string, bool) {
	bestDir, bestLen := "", -1
	for path, m := range byPath {
		if m.Dir == "." {
			continue
		}
		if (importPath == path || strings.HasPrefix(importPath, path+"/")) &&
			len(path) > bestLen {
			bestDir, bestLen = m.Dir, len(path)
		}
	}
	return bestDir, bestLen >= 0
}

// suiteModuleImports derives, for every suite, the contrib and
// instrumentation modules whose packages the suite's Go files import.
func suiteModuleImports(
	root string, byPath map[string]repoModule, suiteDirs map[string]bool,
) (map[string]map[string]bool, error) {
	derived := map[string]map[string]bool{}
	for suite := range suiteDirs {
		dir := filepath.Join(root, integrationRoot, suite)
		walkErr := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for _, m := range goImportRef.FindAllStringSubmatch(string(data), -1) {
				owner, ok := owningModuleDir(byPath, m[1])
				if !ok {
					continue
				}
				if derived[suite] == nil {
					derived[suite] = map[string]bool{}
				}
				derived[suite][owner] = true
			}
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}
	return derived, nil
}

// TestSuiteMappingCoversSuiteImports derives every (suite, module) pair from
// the suites' real imports and requires the module's entry to list the
// suite: emptying an entry silently stops the suites that exercise it. The
// shared-modules entries are excluded from this parity -- their selections
// are deliberately minimal, because a change there runs the full lane.
func TestSuiteMappingCoversSuiteImports(t *testing.T) {
	tab, _, root := testTable(t)
	byPath, _, err := loadModules(root)
	if err != nil {
		t.Fatalf("loadModules() = %v", err)
	}
	suiteDirs, err := loadSuiteDirs(root)
	if err != nil {
		t.Fatalf("loadSuiteDirs() = %v", err)
	}
	shared := map[string]bool{}
	for _, m := range tab.IntegrationSuites.SharedModules {
		shared[m] = true
	}

	derived, err := suiteModuleImports(root, byPath, suiteDirs)
	if err != nil {
		t.Fatalf("suiteModuleImports() = %v", err)
	}
	if len(derived) == 0 {
		t.Fatal("the import scan derived no (suite, module) pairs")
	}
	for suite, mods := range derived {
		for dir := range mods {
			if shared[dir] {
				continue
			}
			list, ok := tab.IntegrationSuites.Modules[dir]
			if !ok {
				t.Errorf("%s is imported by the %s suite but has no integration-suites.modules entry", dir, suite)
				continue
			}
			if !slices.Contains(list, suite) {
				t.Errorf("%s maps to %v, but the %s suite imports it", dir, list, suite)
			}
		}
	}
}
