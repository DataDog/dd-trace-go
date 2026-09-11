// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build depgraph

// This file carries the `depgraph` build tag because it shells out to
// `go list -deps` in every workspace module. That is ~15s warm and minutes
// cold, which is fine nightly and far too slow to sit on the pull request path.
//
//	go test -tags depgraph ./scripts/ciselect/
package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestDependentModulesAreAccurate re-derives what the table claims.
//
// `dependent-modules` encodes a measured fact: which workspace modules import
// anything under a leaf component's directory. Recording it rather than
// computing it live keeps the gate job fast, but a recorded fact rots the
// moment someone adds an import. This is the check that catches that.
func TestDependentModulesAreAccurate(t *testing.T) {
	tab, _, root := testTable(t)

	// Take the root module path from go.mod, not `go list -m`: inside a
	// workspace that command prints every module.
	_, byDir, err := loadModules(root)
	if err != nil {
		t.Fatalf("loadModules() = %v", err)
	}
	rootPath := byDir["."].Path
	if rootPath == "" {
		t.Fatal("could not determine the root module path")
	}
	dirs := workspaceModuleDirs(t, root)

	// importers[dir] is every dd-trace-go package that module reaches.
	importers := make(map[string]map[string]bool, len(dirs))
	for _, dir := range dirs {
		importers[dir] = ddPackages(t, root, dir)
	}

	for i := range tab.Components {
		c := &tab.Components[i]
		if c.DependentModules == nil {
			continue // no claim to verify
		}

		// Root-module import path prefixes covered by this component.
		var prefixes []string
		for _, p := range c.patterns {
			if p.subtree == "" {
				continue
			}
			prefixes = append(prefixes, rootPath+"/"+p.subtree)
		}
		if len(prefixes) == 0 {
			continue
		}

		var measured []string
		for _, dir := range dirs {
			if dir == "." {
				continue
			}
			for pkg := range importers[dir] {
				if matchesAnyPrefix(pkg, prefixes) {
					measured = append(measured, dir)
					break
				}
			}
		}
		sort.Strings(measured)

		claimed := append([]string(nil), c.DependentModules...)
		sort.Strings(claimed)
		if claimed == nil {
			claimed = []string{}
		}
		if measured == nil {
			measured = []string{}
		}

		if !reflect.DeepEqual(claimed, measured) {
			t.Errorf("component %q dependent-modules is stale.\n claimed: %v\nmeasured: %v\n"+
				"Update %s (and re-check the component's gates while you are there).",
				c.ID, claimed, measured, tableRelPath)
		}
	}
}

func matchesAnyPrefix(pkg string, prefixes []string) bool {
	for _, pre := range prefixes {
		if pkg == pre || strings.HasPrefix(pkg, pre+"/") {
			return true
		}
	}
	return false
}

func workspaceModuleDirs(t *testing.T, root string) []string {
	t.Helper()
	out := goCmd(t, root, "list", "-m", "-f", "{{.Dir}}")
	var dirs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rel, err := filepath.Rel(root, line)
		if err != nil {
			continue
		}
		dirs = append(dirs, filepath.ToSlash(rel))
	}
	sort.Strings(dirs)
	return dirs
}

// ddPackages returns every dd-trace-go import path reachable from dir.
func ddPackages(t *testing.T, root, dir string) map[string]bool {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "./...")
	cmd.Dir = filepath.Join(root, dir)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// A module that does not build is not a reason to fail this check; it will
	// fail its own tests. Use whatever `go list` managed to resolve.
	_ = cmd.Run()

	out := map[string]bool{}
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "DataDog/dd-trace-go") {
			out[line] = true
		}
	}
	return out
}

func goCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go %s in %s: %v", strings.Join(args, " "), dir, err)
	}
	return string(out)
}
