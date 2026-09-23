// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"maps"
	"path/filepath"
	"slices"
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

// classifyCase is one row of TestClassify. It lives at package scope so
// TestEveryGoBearingComponentIsPinned can assert that every component
// carrying Go code has at least one row constraining its gates.
type classifyCase struct {
	name    string
	files   []string
	wantAll bool
	// want and notWant are checked only when wantAll is false.
	want    []string
	notWant []string
}

var classifyCases = []classifyCase{
	{
		// system-tests stays on: the FEATURE_FLAGGING_AND_EXPERIMENTATION
		// scenario is this feature's only end-to-end coverage. Orchestrion
		// and parametric have nothing to do with it.
		name:    "leaf package skips the suites that cannot see it",
		files:   []string{"openfeature/provider.go", "openfeature/remoteconfig.go"},
		want:    []string{"pull-request-tests", "generate", "codeql", "static-lint", "system-tests"},
		notWant: []string{"orchestrion", "parametric-tests"},
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
		// The gitlink has no trailing slash and no children, so the
		// openfeature/** pattern has to match the bare path. This fixture
		// *is* the FFE scenario's data, so it must reach system-tests.
		name:    "a submodule pointer bump is a bare gitlink path",
		files:   []string{"openfeature/ffe-system-test-data"},
		want:    []string{"pull-request-tests", "system-tests"},
		notWant: []string{"orchestrion", "parametric-tests"},
	},
	{
		// Only the orchestrion/ subtree of these fixtures has a go.mod. The
		// rest are root-module packages, and the tests that run them --
		// internal/civisibility/.../itrbackfillfixture -- are root-module
		// too, so test-core is the only place any of it executes.
		name:  "ITR backfill fixtures still need the tests that drive them",
		files: []string{"internal/civisibility/integrations/gotesting/fixtures/itrbackfill/manual/lib/lib.go"},
		want:  []string{"pull-request-tests", "static-lint"},
		// Nothing ships these and no weblog or parametric scenario builds
		// them.
		notWant: []string{"system-tests", "parametric-tests"},
	},
	{
		name:    "an unknown top-level directory runs everything",
		files:   []string{"zz-brand-new/thing.go"},
		wantAll: true,
	},
	{
		// Its own go.mod, so nothing in the root module can import it. Only
		// unit tests (test-telemetry-errors-e2e lives here) plus hygiene.
		name:    "a submodule under internal/ is not core",
		files:   []string{"internal/apps/apps.go"},
		want:    []string{"pull-request-tests", "static-lint"},
		notWant: []string{"system-tests", "orchestrion", "parametric-tests"},
	},
	{
		// orchestrion.yml pre-pulls the pinned images from this module's
		// docker-compose.yaml, so it is the one submodule that keeps that gate.
		name:    "the testcontainers module keeps the orchestrion gate",
		files:   []string{"instrumentation/testutils/containers/images/docker-compose.yaml"},
		want:    []string{"orchestrion", "pull-request-tests"},
		notWant: []string{"system-tests", "parametric-tests"},
	},
	{
		// Referenced only by smoke-tests.yml, which has no pull_request trigger.
		name:    "a submodule no pull-request workflow builds needs only hygiene",
		files:   []string{"internal/setup-smoke-test/main.go"},
		want:    []string{"static-lint", "generate"},
		notWant: []string{"pull-request-tests", "system-tests", "orchestrion"},
	},
	{
		// ci_test_core.sh runs this module's tests, so pull-request-tests must stay on.
		name:    "the exectracetest module keeps pull-request-tests",
		files:   []string{"internal/exectracetest/exectrace_test.go"},
		want:    []string{"pull-request-tests", "static-lint"},
		notWant: []string{"system-tests", "orchestrion", "parametric-tests"},
	},
	{
		// namingschematest and validationtest run in the contrib matrix,
		// which pull-request-tests drives.
		name:    "the matrix member modules keep pull-request-tests",
		files:   []string{"instrumentation/internal/namingschematest/valkey_test.go"},
		want:    []string{"pull-request-tests", "static-lint"},
		notWant: []string{"system-tests", "orchestrion", "parametric-tests"},
	},
	{
		// No pull-request workflow builds this module, so it stays hygiene-only.
		name:    "the traceproftest module stays hygiene-only",
		files:   []string{"internal/traceprof/traceproftest/testapp/test_app.go"},
		want:    []string{"static-lint", "generate"},
		notWant: []string{"pull-request-tests", "system-tests", "orchestrion"},
	},
	{
		// profiler/orchestrion.yml is an injected aspect, so orchestrion runs.
		// One component per case here on purpose: a case unions the gates of
		// every file in it, so pairing two components would let one mask a
		// wrong trim in the other.
		name:    "profiler keeps the orchestrion gate",
		files:   []string{"profiler/profiler.go"},
		want:    []string{"pull-request-tests", "orchestrion", "static-lint"},
		notWant: []string{"system-tests", "parametric-tests"},
	},
	{
		// crashtracker/orchestrion.yml is an injected aspect too.
		name:    "crashtracker keeps the orchestrion gate",
		files:   []string{"crashtracker/crashtracker.go"},
		want:    []string{"pull-request-tests", "orchestrion", "static-lint"},
		notWant: []string{"system-tests", "parametric-tests"},
	},
	{
		// Public wrapper with no aspect file and no weblog behind it.
		name:    "the civisibility wrapper needs unit tests only",
		files:   []string{"civisibility/linker.go"},
		want:    []string{"pull-request-tests", "static-lint"},
		notWant: []string{"system-tests", "orchestrion", "parametric-tests"},
	},
	{
		// Two contrib modules import llmobs; the matrix picks those up from the
		// reverse-dependency closure, so no extra gate is needed here.
		name:    "llmobs needs unit tests only",
		files:   []string{"llmobs/llmobs.go"},
		want:    []string{"pull-request-tests", "static-lint"},
		notWant: []string{"system-tests", "orchestrion", "parametric-tests"},
	},
	{
		// The public orchestrion/ package is not the aspect-only component:
		// it holds real root-module code, so it keeps unit tests as well as
		// the orchestrion gate.
		name:    "the public orchestrion package keeps both gates",
		files:   []string{"orchestrion/orchestrion.go"},
		want:    []string{"pull-request-tests", "orchestrion", "static-lint"},
		notWant: []string{"system-tests", "parametric-tests"},
	},
	{
		// The public otelc/ package mirrors orchestrion/: it holds real
		// root-module code, so it keeps unit tests as well as the otelc gate.
		name:    "the public otelc package keeps both gates",
		files:   []string{"otelc/all/otel.instrumentation.go"},
		want:    []string{"pull-request-tests", "otelc", "static-lint"},
		notWant: []string{"system-tests", "orchestrion", "parametric-tests"},
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
		// Release and module tooling. Its own tests run under test-core, so
		// pull-request-tests stays on; nothing ships it. scripts/ciselect and
		// scripts/ci_*.sh are carved out into `universal`, which runs everything.
		name:    "release tooling needs its own tests and nothing else",
		files:   []string{"scripts/autoreleasetagger/main.go"},
		want:    []string{"pull-request-tests", "static-lint"},
		notWant: []string{"system-tests", "orchestrion", "parametric-tests"},
	},
	{
		name:    "one core path poisons an otherwise narrow change set",
		files:   []string{"openfeature/provider.go", "ddtrace/tracer/tracer.go"},
		wantAll: true,
	},
}

func TestClassify(t *testing.T) {
	tab, graph, _ := testTable(t)

	for _, tt := range classifyCases {
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

// TestNoSubmoduleIsClassifiedAsCore is a structural rule, not a judgement.
//
// A directory with its own go.mod is not part of the root module, so no
// root-module package -- and therefore no contrib module -- can import it. It
// cannot be `core`, whose whole meaning is "reachable from everything". Several
// such directories live under internal/ and instrumentation/, which the core
// component claims with a broad prefix, so each needs its own earlier entry.
//
// Measured on the 120 merged pull requests before this rule existed: nine
// submodules were swallowed by `core`, and four of those pull requests ran the
// entire suite solely because of it.
func TestNoSubmoduleIsClassifiedAsCore(t *testing.T) {
	tab, _, root := testTable(t)

	var offenders []string
	for _, f := range trackedFiles(t, root) {
		if filepath.Base(f) != "go.mod" {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(f))
		if dir == "." {
			continue // the root module is core by definition
		}
		c := tab.componentFor(dir + "/probe_test.go")
		if c != nil && c.ID == "core" {
			offenders = append(offenders, dir)
		}
	}
	sort.Strings(offenders)
	for _, d := range offenders {
		t.Errorf("%s has its own go.mod but classifies as `core`. Nothing in the root module "+
			"can import it, so it cannot need the full suite. Add a component for it in %s, "+
			"ordered before `core`.", d, tableRelPath)
	}
}

// Root-module tests keep the pull-request-tests gate.
//
// scripts/ci_test_core.sh decides what to run with `go list ./...` over the
// root module, inside the test-core job that the pull-request-tests gate owns.
// test-core and multios-unit-tests are the only jobs that execute root-module
// tests, and both sit behind that one gate. So a component claiming a
// root-module _test.go while dropping the gate describes tests that no longer
// run for a change confined to it -- including tests that exist purely to
// drive a neighbouring fixture.
//
// Scoped to _test.go on purpose. A root-module package with no tests of its own
// is still type-checked by static-lint and built by static-cross-compile, both
// in @go-hygiene, so dropping pull-request-tests loses nothing for it. That is
// why internal/orchestrion/generator and matrix legitimately sit on the
// orchestrion gate alone.
//
// This is the mirror of TestNoSubmoduleIsClassifiedAsCore. That rule keeps
// submodules out of `core`; this one keeps root-module tests inside
// pull-request-tests. civisibility-fixtures broke it: it was filed under the
// submodule section, but only its orchestrion/ subtree has a go.mod, so
// test-core had been running the other three all along.
func TestRootModuleTestsKeepPullRequestTests(t *testing.T) {
	tab, _, root := testTable(t)

	tracked := trackedFiles(t, root)

	// Every directory that owns a go.mod is a module boundary: files below it
	// belong to that module, not the root one.
	var submodules []string
	for _, f := range tracked {
		if filepath.Base(f) != "go.mod" {
			continue
		}
		if dir := filepath.ToSlash(filepath.Dir(f)); dir != "." {
			submodules = append(submodules, dir+"/")
		}
	}

	offenders := map[string][]string{}
	for _, f := range tracked {
		f = filepath.ToSlash(f)
		if !strings.HasSuffix(f, "_test.go") {
			continue
		}
		// `go list ./...` never descends into testdata, so those files are not
		// packages of the root module however they classify.
		if strings.Contains("/"+f, "/testdata/") {
			continue
		}
		if inSubmodule(f, submodules) {
			continue
		}
		c := tab.componentFor(f)
		if c == nil {
			continue // unclassified already escalates to every gate
		}
		gates, err := tab.expand(c.Gates, nil)
		if err != nil {
			t.Fatalf("component %q: expand(%v) = %v", c.ID, c.Gates, err)
		}
		if slices.Contains(gates, "pull-request-tests") {
			continue
		}
		offenders[c.ID] = append(offenders[c.ID], f)
	}

	for _, id := range slices.Sorted(maps.Keys(offenders)) {
		files := offenders[id]
		t.Errorf("component %q drops the pull-request-tests gate but claims %d root-module "+
			"test file(s), e.g. %s. test-core is the only job that runs them, so a change "+
			"confined to this component would not run its own tests. Either add "+
			"pull-request-tests to the component in %s, or give the directory its own go.mod "+
			"if it really is outside the root module.",
			id, len(files), files[0], tableRelPath)
	}
}

// inSubmodule reports whether file lives under one of dirs, each of which must
// end in "/".
func inSubmodule(file string, dirs []string) bool {
	for _, d := range dirs {
		if strings.HasPrefix(file, d) {
			return true
		}
	}
	return false
}

// Every component carrying Go code has its gates pinned by a TestClassify row.
//
// The gate set of each component was hand-derived by grepping .github/workflows
// and scripts for where the paths are actually referenced. Nothing in the
// classifier can re-derive that, so the only thing standing between a wrong
// trim and a silent loss of test coverage is a TestClassify row asserting the
// gates in the direction that matters for that component.
//
// Enumerating the components here would rot the same way the table would.
// Instead: if a component claims any tracked .go file, some row must exercise
// a path it claims. A new component is a failure until someone writes one.
//
// Measured when this guard was added: 19 components carried Go code and 5 of
// them -- civisibility, crashtracker, llmobs, orchestrion-public, profiler --
// had no row at all. Dropping pull-request-tests from exectracetest or
// instrumentation-testmodules passed the entire suite.
func TestEveryGoBearingComponentIsPinned(t *testing.T) {
	tab, _, root := testTable(t)

	goBearing := map[string]bool{}
	for _, f := range trackedFiles(t, root) {
		f = filepath.ToSlash(f)
		if filepath.Ext(f) != ".go" {
			continue
		}
		if c := tab.componentFor(f); c != nil {
			goBearing[c.ID] = true
		}
	}
	if len(goBearing) == 0 {
		t.Fatal("no component claims a Go file; the table or the matcher is broken")
	}

	// A case only pins a component when every file in it belongs to that
	// component. classify unions the gates of all the files, so a mixed case
	// lets one component supply a gate that another was wrongly trimmed of.
	pinned := map[string]bool{}
	for _, tc := range classifyCases {
		ids := map[string]bool{}
		for _, f := range tc.files {
			c := tab.componentFor(f)
			if c == nil {
				ids["<unclassified>"] = true
				continue
			}
			ids[c.ID] = true
		}
		if len(ids) != 1 {
			continue
		}
		for id := range ids {
			pinned[id] = true
		}
	}

	var gap []string
	for id := range goBearing {
		if !pinned[id] {
			gap = append(gap, id)
		}
	}
	for _, id := range slices.Sorted(slices.Values(gap)) {
		t.Errorf("component %q claims Go files but no TestClassify row exercises it. Its "+
			"gates in %s were derived by hand and nothing re-derives them, so a wrong trim "+
			"would pass every test. Add a row asserting what this component must and must "+
			"not run.", id, tableRelPath)
	}
}
