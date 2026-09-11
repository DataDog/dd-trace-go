// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const microBenchPath = ".gitlab/benchmarks/micro/gitlab-ci.yml"

// knownMissingBenchmarks are names listed in a BENCHMARKS variable for which no
// function exists, so the runner's `go test -bench ^Name$` matches nothing and
// the benchmark silently does not run.
//
// This is a pre-existing gap, not something this change introduced. Removing an
// entry here is the fix; see the tracked follow-up.
var knownMissingBenchmarks = map[string]bool{
	"BenchmarkAgentTraceWriter": true,
}

var (
	benchmarksVar = regexp.MustCompile(`BENCHMARKS: "([^"]*)"`)
	changesPaths  = regexp.MustCompile(`(?s)\.benchmark-changes: &benchmark-changes\n  paths:\n(.*?)\n  compare_to:`)
	pathEntry     = regexp.MustCompile(`(?m)^\s+- (.+)$`)
)

// TestBenchmarkChangesCoverEveryBenchmark is the reason the GitLab path list
// can be trusted.
//
// The microbenchmarks measure hot paths that live in specific packages, and
// three of them live in contrib modules. Hand-maintaining the path list would
// mean a benchmark could move to a new package and quietly stop being gated,
// with the only symptom a performance regression nobody caught. So the list is
// checked against where the functions actually are.
func TestBenchmarkChangesCoverEveryBenchmark(t *testing.T) {
	_, _, root := testTable(t)

	body, err := os.ReadFile(filepath.Join(root, microBenchPath))
	if err != nil {
		t.Fatalf("read %s: %v", microBenchPath, err)
	}
	src := string(body)

	names := benchmarkNames(src)
	if len(names) == 0 {
		t.Fatalf("%s: found no BENCHMARKS variables", microBenchPath)
	}
	patterns := changesPatterns(t, src)

	tracked := trackedFiles(t, root)
	owners := benchmarkOwners(t, root, tracked)

	var missing []string
	for _, name := range names {
		pkgs := owners[name]
		if len(pkgs) == 0 {
			if !knownMissingBenchmarks[name] {
				missing = append(missing, name)
			}
			continue
		}
		if knownMissingBenchmarks[name] {
			t.Errorf("%q is in knownMissingBenchmarks but now exists in %v; remove the exception",
				name, pkgs)
		}
		for _, pkg := range pkgs {
			probe := pkg + "/probe_test.go"
			covered := false
			for _, p := range patterns {
				if p.match(probe) {
					covered = true
					break
				}
			}
			if !covered {
				t.Errorf("benchmark %s lives in %s, which no .benchmark-changes path covers. "+
					"A change there would skip the benchmark that measures it.", name, pkg)
			}
		}
	}
	sort.Strings(missing)
	for _, name := range missing {
		t.Errorf("%s lists benchmark %q, but no `func %s(` exists. The runner matches with "+
			"`-bench ^%s$`, so it measures nothing.", microBenchPath, name, name, name)
	}
}

func benchmarkNames(src string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range benchmarksVar.FindAllStringSubmatch(src, -1) {
		for name := range strings.SplitSeq(m[1], "|") {
			name = strings.TrimSpace(name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// changesPatterns parses the `.benchmark-changes` path list. GitLab writes
// subtrees as "dir/**/*", which the rule table's matcher spells "dir/**".
func changesPatterns(t *testing.T, src string) []pattern {
	t.Helper()
	m := changesPaths.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("%s: could not find the .benchmark-changes paths block", microBenchPath)
	}
	entries := pathEntry.FindAllStringSubmatch(m[1], -1)
	out := make([]pattern, 0, len(entries))
	for _, e := range entries {
		raw := strings.TrimSpace(e[1])
		raw = strings.TrimSuffix(raw, "/**/*")
		if raw != strings.TrimSpace(e[1]) {
			raw += "/**"
		}
		p, err := parsePattern(raw)
		if err != nil {
			t.Fatalf("%s: path entry %q: %v", microBenchPath, e[1], err)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		t.Fatalf("%s: .benchmark-changes has no paths", microBenchPath)
	}
	return out
}

// benchmarkOwners maps a benchmark function name to the package directories
// declaring it. A name can legitimately appear in more than one package -- the
// runner's regex matches all of them.
func benchmarkOwners(t *testing.T, root string, tracked []string) map[string][]string {
	t.Helper()
	decl := regexp.MustCompile(`(?m)^func (Benchmark[A-Za-z0-9_]*)\(`)
	owners := map[string][]string{}
	for _, f := range tracked {
		if !strings.HasSuffix(f, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue // a gitlink or a file removed since ls-files ran
		}
		dir := filepath.ToSlash(filepath.Dir(f))
		for _, m := range decl.FindAllStringSubmatch(string(body), -1) {
			name := m[1]
			if !contains(owners[name], dir) {
				owners[name] = append(owners[name], dir)
			}
		}
	}
	return owners
}
