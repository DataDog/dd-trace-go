// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"os/exec"
	"slices"
	"sort"
	"strings"
	"testing"
)

// trackedFiles lists every file git knows about, from the repository root.
func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var files []string
	for f := range strings.SplitSeq(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		t.Fatal("git ls-files returned nothing")
	}
	return files
}

// TestEveryTrackedPathIsClassified is the drift guard. An unclassified path is
// not a silent loss of coverage at runtime -- ciselect escalates to the full
// suite -- but it does mean the table has fallen behind the repository, which
// is a review problem and should surface in `make lint/misc`.
func TestEveryTrackedPathIsClassified(t *testing.T) {
	tab, _, root := testTable(t)

	unmatched := map[string][]string{}
	for _, f := range trackedFiles(t, root) {
		if tab.componentFor(f) == nil {
			top := topLevel(f)
			unmatched[top] = append(unmatched[top], f)
		}
	}
	if len(unmatched) == 0 {
		return
	}
	tops := make([]string, 0, len(unmatched))
	for k := range unmatched {
		tops = append(tops, k)
	}
	sort.Strings(tops)
	for _, top := range tops {
		files := unmatched[top]
		sample := files
		if len(sample) > 5 {
			sample = sample[:5]
		}
		t.Errorf("%d tracked path(s) under %q match no component in %s; add an entry. Examples: %v",
			len(files), top, tableRelPath, sample)
	}
}

// TestEveryTopLevelEntryIsNamed is the stronger half of the guard: a new
// top-level directory must be classified deliberately, not swept up by a
// repo-wide pattern like **/*.md. Only anchored patterns count as naming it.
func TestEveryTopLevelEntryIsNamed(t *testing.T) {
	tab, _, root := testTable(t)

	named := map[string]bool{}
	for i := range tab.Components {
		for _, p := range tab.Components[i].patterns {
			anchor := p.subtree
			if anchor == "" {
				anchor = p.exact
			}
			if anchor == "" && !p.anyDepth {
				anchor = p.dir
			}
			if anchor == "" {
				continue // **/glob names nothing in particular
			}
			named[topLevel(anchor)] = true
		}
	}

	seen := map[string]bool{}
	var missing []string
	for _, f := range trackedFiles(t, root) {
		top := topLevel(f)
		if seen[top] {
			continue
		}
		seen[top] = true
		if !named[top] {
			missing = append(missing, top)
		}
	}
	sort.Strings(missing)
	for _, top := range missing {
		t.Errorf("top-level entry %q is not named by any anchored pattern in %s. "+
			"Add it explicitly -- relying on a repo-wide pattern hides new code from review.",
			top, tableRelPath)
	}
}

func topLevel(p string) string {
	top, _, _ := strings.Cut(p, "/")
	return top
}

// TestNoDeadPatterns keeps the table honest in the other direction: a pattern
// matching nothing is either a typo or a leftover from a deleted directory,
// and in the allowlist half of the table that silently widens what escalates.
func TestNoDeadPatterns(t *testing.T) {
	tab, _, root := testTable(t)
	files := trackedFiles(t, root)

	for i := range tab.Components {
		c := &tab.Components[i]
		for j, p := range c.patterns {
			if !slices.ContainsFunc(files, p.match) {
				t.Errorf("component %q pattern %q matches no tracked file", c.ID, c.Paths[j])
			}
		}
	}
}
