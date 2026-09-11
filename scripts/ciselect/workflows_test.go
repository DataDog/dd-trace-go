// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// parseWorkflow returns the decoded workflow and its `on:` mapping.
//
// The `on` key needs care: YAML 1.1 parsers resolve a bare `on` to the boolean
// true, which is why .github/workflows/test-apps.yml writes `"on":`. Handle
// both spellings rather than depending on which rule the decoder follows.
func parseWorkflow(t *testing.T, path string) (map[string]any, map[string]any) {
	t.Helper()
	body := []byte(readText(t, filepath.Dir(path), filepath.Base(path)))
	var doc map[string]any
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var on map[string]any
	for _, key := range []any{"on", true} {
		if v, ok := doc[fmt.Sprint(key)]; ok {
			if m, ok := v.(map[string]any); ok {
				on = m
			}
		}
	}
	if on == nil {
		if v, ok := doc["true"]; ok {
			on, _ = v.(map[string]any)
		}
	}
	return doc, on
}

func workflowPaths(t *testing.T) (root string, files []string) {
	t.Helper()
	root, err := repoRoot(".")
	if err != nil {
		t.Fatalf("repoRoot() = %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, workflowDir))
	if err != nil {
		t.Fatalf("read %s: %v", workflowDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := filepath.Ext(e.Name()); ext != ".yml" && ext != ".yaml" {
			continue
		}
		files = append(files, e.Name())
	}
	return root, files
}

// TestEveryWorkflowIsRegistered keeps workflow-gates complete. An unregistered
// workflow escalates to the full suite when edited, which is safe but defeats
// the point, so it should be a test failure rather than a silent cost.
func TestEveryWorkflowIsRegistered(t *testing.T) {
	tab, _, _ := testTable(t)
	_, files := workflowPaths(t)

	for _, f := range files {
		if _, ok := tab.WorkflowGates[f]; !ok {
			t.Errorf("workflow %q is not listed under workflow-gates in %s; "+
				"add it (use [] if editing it should gate nothing)", f, tableRelPath)
		}
	}
	for f := range tab.WorkflowGates {
		if !slices.Contains(files, f) {
			t.Errorf("workflow-gates in %s lists %q, which does not exist", tableRelPath, f)
		}
	}
}

// TestNativePathFiltersAreDeclared pins down which workflows are allowed to
// filter themselves with a native `paths:` block. Native filters are an
// allowlist and therefore unsafe by default -- a new directory matches nothing
// and the workflow silently stops running -- so adding one has to be a
// deliberate edit to the table, not just to a workflow.
func TestNativePathFiltersAreDeclared(t *testing.T) {
	tab, _, root := testTable(t)
	_, files := workflowPaths(t)

	allowed := map[string]bool{}
	for _, f := range tab.NativePathFilters {
		allowed[f] = true
	}

	for _, f := range files {
		_, on := parseWorkflow(t, filepath.Join(root, workflowDir, f))
		pr, ok := on["pull_request"].(map[string]any)
		if !ok {
			continue
		}
		raw, hasPaths := pr["paths"]
		_, hasIgnore := pr["paths-ignore"]
		if hasPaths && hasIgnore {
			t.Errorf("%s sets both paths and paths-ignore on pull_request; GitHub rejects that", f)
		}
		if !hasPaths {
			if allowed[f] {
				t.Errorf("%s is listed under native-path-filters but has no pull_request paths block", f)
			}
			continue
		}
		if !allowed[f] {
			t.Errorf("%s uses a native pull_request paths filter but is not listed under "+
				"native-path-filters in %s. Either add it there with a rationale, or gate the "+
				"workflow through the changes job instead.", f, tableRelPath)
			continue
		}

		globs, _ := raw.([]any)
		selfReferenced := false
		for _, g := range globs {
			glob, _ := g.(string)
			if strings.HasSuffix(glob, "/"+f) || glob == f {
				selfReferenced = true
			}
		}
		if !selfReferenced {
			t.Errorf("%s does not list itself in its own paths filter, so editing it "+
				"cannot be validated by running it", f)
		}
	}
}

// TestNativePathFilterGlobsAreLive catches a filter that has outlived the
// directory it names. In an allowlist a dead glob quietly narrows coverage.
func TestNativePathFilterGlobsAreLive(t *testing.T) {
	tab, _, root := testTable(t)
	tracked := trackedFiles(t, root)

	for _, f := range tab.NativePathFilters {
		_, on := parseWorkflow(t, filepath.Join(root, workflowDir, f))
		pr, ok := on["pull_request"].(map[string]any)
		if !ok {
			continue
		}
		globs, _ := pr["paths"].([]any)
		for _, g := range globs {
			glob, _ := g.(string)
			// A leading '!' excludes; later patterns win. The exclusion still
			// has to name something real, or it is dead weight hiding the fact
			// that the pattern it was meant to narrow is now unbounded.
			glob = strings.TrimPrefix(glob, "!")
			p, err := parseGitHubGlob(glob)
			if err != nil {
				t.Errorf("%s: paths entry %q: %v", f, glob, err)
				continue
			}
			if !slices.ContainsFunc(tracked, p.match) {
				t.Errorf("%s: paths entry %q matches no tracked file", f, glob)
			}
		}
	}
}

// parseGitHubGlob accepts the handful of extra shapes the existing native
// filters use on top of the four forms the rule table allows.
func parseGitHubGlob(glob string) (pattern, error) {
	if p, err := parsePattern(glob); err == nil {
		return p, nil
	}
	// "contrib/**/appsec.go": any depth under a prefix, fixed basename.
	if i := strings.Index(glob, "/**/"); i > 0 {
		prefix := glob[:i]
		rest := glob[i+len("/**/"):]
		if !strings.Contains(rest, "/") && !strings.Contains(rest, "*") {
			return pattern{raw: glob, subtree: prefix, glob: rest, anyDepth: true}, nil
		}
	}
	return pattern{}, errors.New("unsupported glob shape")
}

// TestGatedWorkflowsHaveAChangesJob asserts the wiring is actually present: a
// workflow whose jobs reference needs.changes must define that job, and every
// join job must depend on it so a dead classifier cannot report green.
func TestGatedWorkflowsHaveAChangesJob(t *testing.T) {
	root, files := workflowPaths(t)

	joinJobs := map[string]string{
		"pull-request.yml":   "pull-request-tests-done",
		"system-tests.yml":   "system-tests-done",
		"orchestrion.yml":    "integration-test-done",
		"dynamic-checks.yml": "dynamic-checks-summary",
		"smoke-tests.yml":    "smoke-tests-done",
	}

	for _, f := range files {
		path := filepath.Join(root, workflowDir, f)
		usesChanges := strings.Contains(readText(t, root, workflowDir+"/"+f), "needs.changes.")
		doc, _ := parseWorkflow(t, path)
		jobs, _ := doc["jobs"].(map[string]any)

		if usesChanges {
			if _, ok := jobs["changes"]; !ok {
				t.Errorf("%s references needs.changes but defines no `changes` job", f)
			}
		}

		join, expected := joinJobs[f]
		if !expected {
			continue
		}
		jobRaw, ok := jobs[join]
		if !ok {
			t.Errorf("%s: join job %q is missing; it mints a stable check name and must not be renamed", f, join)
			continue
		}
		if !usesChanges {
			continue
		}
		job, _ := jobRaw.(map[string]any)
		needs := needsList(job["needs"])
		if !contains(needs, "changes") {
			t.Errorf("%s: join job %q does not list `changes` in needs. Without it a failed "+
				"classifier leaves every dependency 'skipped' and the gate reports green.", f, join)
		}
		if cond, _ := job["if"].(string); usesChanges && !strings.Contains(cond, "cancelled") {
			t.Errorf("%s: join job %q has if: %q; it must run unless cancelled so the check "+
				"name always reports", f, join, cond)
		}
	}
}

func needsList(v any) []string {
	switch n := v.(type) {
	case string:
		return []string{n}
	case []any:
		var out []string
		for _, e := range n {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func contains(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}
