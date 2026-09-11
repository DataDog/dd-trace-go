// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"sort"
	"strings"
	"testing"
)

func testTable(t *testing.T) (*table, *workflowGraph, string) {
	t.Helper()
	root, err := repoRoot(".")
	if err != nil {
		t.Fatalf("repoRoot() = %v", err)
	}
	tab, err := loadTable(root)
	if err != nil {
		t.Fatalf("loadTable() = %v", err)
	}
	g, err := loadWorkflowGraph(root)
	if err != nil {
		t.Fatalf("loadWorkflowGraph() = %v", err)
	}
	return tab, g, root
}

func TestClassify(t *testing.T) {
	tab, graph, _ := testTable(t)

	tests := []struct {
		name    string
		files   []string
		wantAll bool
		// want and notWant are checked only when wantAll is false.
		want    []string
		notWant []string
	}{
		{
			name:    "leaf package runs unit tests but not the heavy suites",
			files:   []string{"openfeature/provider.go", "openfeature/remoteconfig.go"},
			want:    []string{"pull-request-tests", "generate", "codeql", "static-lint"},
			notWant: []string{"system-tests", "orchestrion", "parametric-tests"},
		},
		{
			name:  "contrib/os has no go.mod, so it is root-module code",
			files: []string{"contrib/os/os.go"},
			// Anything less than the full suite here is a correctness bug: a
			// contrib/** prefix rule would wrongly narrow this.
			wantAll: true,
		},
		{
			name:    "datastreams/options is reachable from 77 modules",
			files:   []string{"datastreams/options/options.go"},
			wantAll: true,
		},
		{
			name:    "internal/orchestrion is core, not the orchestrion suite",
			files:   []string{"internal/orchestrion/gls.go"},
			wantAll: true,
		},
		{
			name:    "the orchestrion integration suite is not core",
			files:   []string{"internal/orchestrion/_integration/gin/gin.go"},
			want:    []string{"orchestrion"},
			notWant: []string{"system-tests", "parametric-tests"},
		},
		{
			name:    "an aspect file needs orchestrion and regeneration only",
			files:   []string{"contrib/gin-gonic/gin/orchestrion.yml"},
			want:    []string{"orchestrion", "generate"},
			notWant: []string{"system-tests", "parametric-tests"},
		},
		{
			// The '**/orchestrion.yml' pattern in the orchestrion-aspects
			// component also matches this path. It must lose to the earlier
			// `workflows` component, or editing the workflow would look like
			// editing an aspect file. build-metrics.yml had exactly this bug.
			name:    "the orchestrion workflow is a workflow, not an aspect file",
			files:   []string{".github/workflows/orchestrion.yml"},
			want:    []string{"orchestrion", "static-actions"},
			notWant: []string{"generate", "pull-request-tests", "system-tests"},
		},
		{
			name:    "a submodule pointer bump is a bare gitlink path",
			files:   []string{"openfeature/ffe-system-test-data"},
			want:    []string{"pull-request-tests"},
			notWant: []string{"system-tests"},
		},
		{
			name:    "an unknown top-level directory runs everything",
			files:   []string{"zz-brand-new/thing.go"},
			wantAll: true,
		},
		{
			name:    "an unknown root-level file runs everything",
			files:   []string{"BRAND_NEW_THING"},
			wantAll: true,
		},
		{
			name:    "an empty change set is unknown, so it runs everything",
			files:   nil,
			wantAll: true,
		},
		{
			name:    "editing a workflow gates that workflow and actionlint",
			files:   []string{".github/workflows/system-tests.yml"},
			want:    []string{"system-tests", "static-actions"},
			notWant: []string{"pull-request-tests", "orchestrion", "static-lint"},
		},
		{
			name:  "editing a reusable workflow reaches its callers",
			files: []string{".github/workflows/unit-integration-tests.yml"},
			// pull-request.yml, main-branch-tests.yml and dynamic-checks.yml
			// all call it, so the PR test gate has to come on.
			want:    []string{"pull-request-tests", "static-actions"},
			notWant: []string{"system-tests"},
		},
		{
			name:    "docs only",
			files:   []string{"README.md", "contrib/README.md"},
			want:    []string{"static-docs"},
			notWant: []string{"pull-request-tests", "system-tests", "static-lint"},
		},
		{
			name:    "the gitlab pipeline does not drive GitHub Actions",
			files:   []string{".gitlab/benchmarks/micro/gitlab-ci.yml"},
			notWant: []string{"pull-request-tests", "system-tests", "static-lint"},
		},
		{
			name:    "go.work escalates",
			files:   []string{"go.work"},
			wantAll: true,
		},
		{
			name:    "a contrib go.mod does not escalate the whole repo",
			files:   []string{"contrib/gin-gonic/gin/go.mod"},
			want:    []string{"pull-request-tests"},
			notWant: []string{"parametric-tests"},
		},
		{
			name:    "the root go.mod does escalate",
			files:   []string{"go.mod"},
			wantAll: true,
		},
		{
			name:    "a composite action escalates",
			files:   []string{".github/actions/setup-go/action.yml"},
			wantAll: true,
		},
		{
			name:    "the rule table escalates",
			files:   []string{".github/ci-components.yml"},
			wantAll: true,
		},
		{
			name:    "the classifier escalates",
			files:   []string{"scripts/ciselect/rules.go"},
			wantAll: true,
		},
		{
			name:    "one core path poisons an otherwise narrow change set",
			files:   []string{"openfeature/provider.go", "ddtrace/tracer/tracer.go"},
			wantAll: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tab.classify(tt.files, graph)
			if err != nil {
				t.Fatalf("classify(%v) = %v", tt.files, err)
			}
			if got.RunAll != tt.wantAll {
				t.Fatalf("classify(%v).RunAll = %t, want %t (reasons: %v)",
					tt.files, got.RunAll, tt.wantAll, got.Reasons)
			}
			if tt.wantAll {
				for _, gate := range tab.Gates {
					if !got.Gates[gate] {
						t.Errorf("classify(%v).Gates[%q] = false, want true", tt.files, gate)
					}
				}
				return
			}
			for _, gate := range tt.want {
				if !got.Gates[gate] {
					t.Errorf("classify(%v).Gates[%q] = false, want true", tt.files, gate)
				}
			}
			for _, gate := range tt.notWant {
				if got.Gates[gate] {
					t.Errorf("classify(%v).Gates[%q] = true, want false", tt.files, gate)
				}
			}
		})
	}
}

// TestClassifyUnknownGateIsRejected guards the table itself: a typo in a gate
// name must fail loudly at load time rather than silently never matching.
func TestTableRejectsUnknownGate(t *testing.T) {
	tab, _, _ := testTable(t)
	if _, err := tab.expand([]string{"no-such-gate"}, nil); err == nil {
		t.Fatal("expand(unknown gate) = nil, want error")
	}
	if _, err := tab.expand([]string{"@no-such-group"}, nil); err == nil {
		t.Fatal("expand(unknown group) = nil, want error")
	}
}

// TestComponentOrdering asserts no component is shadowed by an earlier, broader
// one. Order is load-bearing: internal/orchestrion/_integration/** only works
// because it precedes core's internal/**.
func TestComponentOrdering(t *testing.T) {
	tab, _, _ := testTable(t)
	for i := range tab.Components {
		for _, p := range tab.Components[i].Paths {
			probe := probePath(p)
			if probe == "" {
				continue
			}
			got := tab.componentFor(probe)
			if got == nil {
				t.Errorf("component %q pattern %q: probe %q matches nothing",
					tab.Components[i].ID, p, probe)
				continue
			}
			if got.ID != tab.Components[i].ID {
				t.Errorf("component %q is shadowed: pattern %q probe %q is claimed by %q first",
					tab.Components[i].ID, p, probe, got.ID)
			}
		}
	}
}

// probePath turns a pattern into a representative path it must claim.
func probePath(raw string) string {
	switch {
	case strings.HasSuffix(raw, "/**"):
		return strings.TrimSuffix(raw, "/**") + "/probe-file.go"
	case strings.HasPrefix(raw, "**/"):
		glob := strings.TrimPrefix(raw, "**/")
		if strings.Contains(glob, "*") {
			return "" // e.g. **/*.md; ambiguous to probe, covered by TestClassify
		}
		return "probe-dir/" + glob
	case strings.Contains(raw, "*"):
		return "" // covered by TestClassify
	default:
		return raw
	}
}

func TestPatternForms(t *testing.T) {
	tests := []struct {
		pattern string
		match   []string
		noMatch []string
	}{
		{
			pattern: "contrib/**",
			match:   []string{"contrib/x.go", "contrib/a/b/c.go", "contrib"},
			noMatch: []string{"contribx/y.go", "internal/contrib/z.go"},
		},
		{
			pattern: "go.work",
			match:   []string{"go.work"},
			noMatch: []string{"go.work.sum", "a/go.work"},
		},
		{
			pattern: ".github/workflows/*.yml",
			match:   []string{".github/workflows/ci.yml"},
			noMatch: []string{".github/workflows/apps/x.yml", ".github/workflows/ci.yaml"},
		},
		{
			pattern: "**/orchestrion.yml",
			match:   []string{"orchestrion.yml", "contrib/gin-gonic/gin/orchestrion.yml"},
			noMatch: []string{"internal/orchestrion/gls.orchestrion.yml", "orchestrion.tool.go"},
		},
		{
			pattern: "**/*.md",
			match:   []string{"README.md", "contrib/README.md"},
			noMatch: []string{"README.mdx", "docs/readme"},
		},
		{
			pattern: "scripts/ci_*.sh",
			match:   []string{"scripts/ci_test_core.sh"},
			noMatch: []string{"scripts/lint.sh", "scripts/sub/ci_x.sh"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			p, err := parsePattern(tt.pattern)
			if err != nil {
				t.Fatalf("parsePattern(%q) = %v", tt.pattern, err)
			}
			for _, f := range tt.match {
				if !p.match(f) {
					t.Errorf("parsePattern(%q).match(%q) = false, want true", tt.pattern, f)
				}
			}
			for _, f := range tt.noMatch {
				if p.match(f) {
					t.Errorf("parsePattern(%q).match(%q) = true, want false", tt.pattern, f)
				}
			}
		})
	}
}

func TestPatternRejectsUnsupportedForms(t *testing.T) {
	for _, raw := range []string{"", "a/**/b", "a/*/b.go", "***", "**/a/b"} {
		if _, err := parsePattern(raw); err == nil {
			t.Errorf("parsePattern(%q) = nil, want error", raw)
		}
	}
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}
