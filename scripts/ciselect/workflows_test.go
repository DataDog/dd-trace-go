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
	"regexp"
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

// TestCacheSeedAndDisableLiterals pins the workflow literals the Go
// cache seeding program depends on. actionlint checks syntax only: a
// deleted seed literal silently reverts that workload to per-run saves, a
// deleted `cache: "false"` literal resumes a light job's pool writes, and
// a deleted workflow_dispatch trigger removes manual re-seeding after a
// Go patch release.
func TestCacheSeedAndDisableLiterals(t *testing.T) {
	root, _ := workflowPaths(t)
	seedLiterals := map[string]int{
		"multios-unit-tests.yml":     1,
		"smoke-tests.yml":            4,
		"unit-integration-tests.yml": 3,
	}
	for name, want := range seedLiterals {
		body := readText(t, filepath.Join(root, workflowDir), name)
		if got := len(seedLiteral.FindAllString(body, -1)); got != want {
			t.Errorf("%s has %d seed literals, want %d", name, got, want)
		}
	}
	cacheFalse := map[string]int{
		"static-checks.yml": 5,
		"orchestrion.yml":   1,
		"otelc.yml":         1,
	}
	for name, want := range cacheFalse {
		body := readText(t, filepath.Join(root, workflowDir), name)
		if got := len(cacheFalseLiteral.FindAllString(body, -1)); got != want {
			t.Errorf("%s has %d cache-disabled literals, want %d", name, got, want)
		}
	}
	// Manual re-seeding path: main-branch-tests.yml must keep its
	// workflow_dispatch trigger so seeds can be re-made by hand after a
	// Go patch release changes every key.
	_, on := parseWorkflow(t, filepath.Join(root, workflowDir, "main-branch-tests.yml"))
	if _, ok := on["workflow_dispatch"]; !ok {
		t.Error("main-branch-tests.yml lacks the workflow_dispatch trigger")
	}
}

var (
	seedLiteral       = regexp.MustCompile(`(?m)^\s+seed:`)
	cacheFalseLiteral = regexp.MustCompile(`(?m)^\s+cache: "false"$`)
	// bareRefGroup matches a concurrency group that is nothing but the ref,
	// with any spacing between the handlebars.
	bareRefGroup = regexp.MustCompile(`^\s*\$\{\{\s*github\.ref\s*\}\}\s*$`)
)

// TestConcurrencyGroupsAreNamespaced rejects a concurrency group that is
// nothing but the ref.
//
// Group names are repository-scoped, not workflow-scoped, so two workflows
// with a bare-ref group collide on every shared ref. On 2026-10-30 that
// exact collision made the smoke-tests nightly cancel the main-branch-tests
// nightly -- both ran on refs/heads/main with cancel-in-progress -- and
// neither workflow can be distinguished from a future workflow reintroducing
// the same mistake by review alone. A group is therefore only valid when it
// carries something workflow-identifying (a literal prefix, github.workflow,
// or any other expression) alongside the ref.
func TestConcurrencyGroupsAreNamespaced(t *testing.T) {
	root, files := workflowPaths(t)

	for _, f := range files {
		doc, _ := parseWorkflow(t, filepath.Join(root, workflowDir, f))
		for where, group := range concurrencyGroups(doc) {
			if err := bareRefGroupError(f, where, group); err != nil {
				t.Error(err)
			}
		}
	}
}

// concurrencyGroups collects every concurrency group declared in a parsed
// workflow: the workflow-level one under "workflow", job-level ones under
// the job's name. Both declaration forms count -- the mapping with a group
// key, and the scalar shorthand `concurrency: <group>`.
func concurrencyGroups(doc map[string]any) map[string]string {
	groups := map[string]string{}
	add := func(where string, conc any) {
		switch c := conc.(type) {
		case map[string]any:
			group, _ := c["group"].(string)
			groups[where] = group
		case string:
			groups[where] = c
		}
	}
	add("workflow", doc["concurrency"])
	jobs, _ := doc["jobs"].(map[string]any)
	for name, raw := range jobs {
		job, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		add("job "+name, job["concurrency"])
	}
	return groups
}

// bareRefGroupError reports a group that is nothing but the ref, or a
// declared concurrency block with no group at all.
func bareRefGroupError(f, where, group string) error {
	if group == "" {
		return fmt.Errorf("%s: %s declares concurrency without a group", f, where)
	}
	if bareRefGroup.MatchString(group) {
		return fmt.Errorf("%s: %s concurrency group %q is a bare ref. Group names are "+
			"repository-scoped, so a second workflow with the same group on the "+
			"same ref would cancel this one's runs (the 2026-10-30 nightly "+
			"collision). Prefix the group with github.workflow or a literal, "+
			"e.g. ${{ github.workflow }}-%s", f, where, group, group)
	}
	return nil
}

// TestConcurrencyGroupForms covers every declaration shape the invariant
// must police. The scalar shorthand `concurrency: <group>` is valid GitHub
// Actions syntax and parses as a plain string, so a check that reads only the
// mapping form would let a bare-ref group slip through it.
func TestConcurrencyGroupForms(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name:    "scalar bare ref",
			yaml:    "concurrency: ${{ github.ref }}",
			wantErr: true,
		},
		{
			name: "scalar namespaced",
			yaml: "concurrency: smoke-${{ github.ref }}",
		},
		{
			name:    "mapping bare ref",
			yaml:    "concurrency:\n  group: ${{ github.ref }}\n  cancel-in-progress: true",
			wantErr: true,
		},
		{
			name: "mapping namespaced",
			yaml: "concurrency:\n  group: ${{ github.workflow }}-${{ github.ref }}\n  cancel-in-progress: true",
		},
		{
			name:    "job-level scalar bare ref",
			yaml:    "jobs:\n  test:\n    concurrency: ${{ github.ref }}",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var doc map[string]any
			if err := yaml.Unmarshal([]byte(tt.yaml), &doc); err != nil {
				t.Fatalf("parse: %v", err)
			}
			groups := concurrencyGroups(doc)
			if len(groups) == 0 {
				t.Fatalf("no concurrency group collected from %q", tt.yaml)
			}
			for where, group := range groups {
				err := bareRefGroupError("test.yml", where, group)
				if got := err != nil; got != tt.wantErr {
					t.Errorf("bareRefGroupError(%q) = %v, want error: %t", group, err, tt.wantErr)
				}
			}
		})
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

// TestCIConfigGateRunsTheseTests closes the loop opened by the component
// table: .github/workflows/** and .gitlab/** enable `static-actions` so these
// invariants run when the files they check are edited. That only works if the
// job behind that gate actually runs them.
//
// Without this, a workflow-only pull request enables static-actions and nothing
// else -- not static-copyright, whose `make lint/misc` is the other caller --
// so workflows_test.go and gitlab_test.go would silently not run on precisely
// the changes they exist to police.
func TestCIConfigGateRunsTheseTests(t *testing.T) {
	tab, _, root := testTable(t)

	const gate = "static-actions"
	for _, id := range []string{"workflows", "workflow-codegen", "gitlab"} {
		var found *component
		for i := range tab.Components {
			if tab.Components[i].ID == id {
				found = &tab.Components[i]
				break
			}
		}
		if found == nil {
			t.Errorf("component %q is missing from %s", id, tableRelPath)
			continue
		}
		gates, err := tab.expand(found.Gates, nil)
		if err != nil {
			t.Fatalf("component %q: %v", id, err)
		}
		if !contains(gates, gate) {
			t.Errorf("component %q does not enable %q, so editing its paths would not run "+
				"the scripts/ciselect invariants that check those very files", id, gate)
		}
	}

	// The job behind the gate has to run them.
	body := readText(t, root, workflowDir+"/static-checks.yml")
	job := body[strings.Index(body, "  check-github-actions:"):]
	if end := strings.Index(job, "\n  check-modules:"); end > 0 {
		job = job[:end]
	}
	if !strings.Contains(job, "go test ./scripts/ciselect/") {
		t.Errorf("static-checks.yml job check-github-actions (gated on %q) does not run "+
			"`go test ./scripts/ciselect/`; the gate would then be decorative", gate)
	}
}

// TestJoinJobsRejectDependencyInducedSkips pins the correction to the join
// jobs.
//
// GitHub applies an implicit success() to every `needs:`, so a job whose
// prerequisite failed reports 'skipped' -- the same conclusion a deliberately
// gated-off job reports. A join job that accepts every skip therefore goes
// green when an upstream job failed, which is strictly worse than the
// `!= 'success'` test it replaced. The fix is a clause requiring real success
// whenever the classifier says the gate is on, and it is easy to drop while
// editing these conditions, so assert its shape.
func TestJoinJobsRejectDependencyInducedSkips(t *testing.T) {
	_, _, root := testTable(t)

	tests := []struct {
		workflow string
		job      string
		// strict is the guard that must appear in the failure condition.
		strict []string
		// deps must all be listed in `needs:`, so a direct failure is caught
		// even when it is not upstream of the gated job.
		deps []string
	}{
		{
			workflow: "pull-request.yml",
			job:      "pull-request-tests-done",
			strict:   []string{"needs.changes.outputs.pull-request-tests == 'true'"},
			deps:     []string{"changes", "warm-repo-cache", "unit-integration-tests", "multios-unit-tests"},
		},
		{
			workflow: "system-tests.yml",
			job:      "system-tests-done",
			strict:   []string{"needs.changes.outputs.system-tests == 'true'"},
			deps: []string{
				"changes", "warm-repo-cache", "build-weblog-images",
				"build-service-extensions-callout", "build-haproxy",
				"build-apim-callout", "system-tests", "tracer-release",
			},
		},
		{
			workflow: "orchestrion.yml",
			job:      "integration-test-done",
			strict:   []string{"needs.changes.outputs.orchestrion == 'true'"},
			deps: []string{
				"changes", "generate", "go-versions-matrix",
				"service-containers", "integration-test",
			},
		},
		{
			workflow: "benchmark-apps.yml",
			job:      "benchmark-apps-done",
			strict:   []string{"needs.changes.outputs.benchmark-apps == 'true'"},
			deps:     []string{"changes", "benchmark-apps"},
		},
		{
			workflow: "unit-integration-tests.yml",
			job:      "test-contrib",
			strict:   []string{"needs.set-up.outputs.has-contribs == 'true'"},
			deps:     []string{"set-up", "test-contrib-matrix"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.workflow+"/"+tt.job, func(t *testing.T) {
			path := filepath.Join(root, workflowDir, tt.workflow)
			doc, _ := parseWorkflow(t, path)
			jobs, _ := doc["jobs"].(map[string]any)
			job, ok := jobs[tt.job].(map[string]any)
			if !ok {
				t.Fatalf("%s: job %q is missing", tt.workflow, tt.job)
			}

			needs := needsList(job["needs"])
			for _, d := range tt.deps {
				if !contains(needs, d) {
					t.Errorf("%s job %q does not list %q in needs; a direct failure there "+
						"would not be caught", tt.workflow, tt.job, d)
				}
			}

			// Find the Failure step's condition.
			steps, _ := job["steps"].([]any)
			var cond string
			for _, raw := range steps {
				step, _ := raw.(map[string]any)
				if name, _ := step["name"].(string); name == "Failure" {
					cond, _ = step["if"].(string)
				}
			}
			if cond == "" {
				t.Fatalf("%s job %q has no Failure step with an if: condition", tt.workflow, tt.job)
			}
			for _, want := range tt.strict {
				if !strings.Contains(cond, want) {
					t.Errorf("%s job %q failure condition is missing %q. Without it a "+
						"prerequisite failure reports 'skipped' and this gate reports green.",
						tt.workflow, tt.job, want)
				}
			}
			for _, d := range tt.deps {
				if d == "changes" || d == "set-up" {
					continue // checked by their own explicit result != 'success' clause
				}
				marker := "needs." + d + ".result"
				if !strings.Contains(cond, marker) {
					t.Errorf("%s job %q failure condition never inspects %s", tt.workflow, tt.job, marker)
				}
			}
		})
	}
}

// TestCoverageUploadsRespectRetries pins the guard that keeps a retried run
// from publishing coverage.
//
// gotestsum's --rerun-fails overwrites -coverprofile with the partial profile
// of the rerun, so a job that retried has a collapsed coverage figure. Both
// Upload Coverage steps therefore have to skip when retries are on. They run
// `if: always()`, so dropping the clause does not fail anything: the upload
// just silently publishes the wrong number against the pull request's SHA.
// That is the bug this guard exists to prevent from coming back.
//
// The RERUN_FAILS env assertion is the other half. Without it the scripts
// never see the input, so retries would be off while the upload was skipped --
// the inverse mistake, and equally silent.
func TestCoverageUploadsRespectRetries(t *testing.T) {
	_, _, root := testTable(t)

	const (
		workflow = "unit-integration-tests.yml"
		stepName = "Upload Coverage to Datadog"
	)
	// Both values the scripts treat as "retries off" must appear, or a caller
	// passing the other one loses its coverage upload while running no retries.
	wantClauses := []string{
		"inputs.rerun-fails == ''",
		"inputs.rerun-fails == '0'",
	}

	doc, _ := parseWorkflow(t, filepath.Join(root, workflowDir, workflow))
	jobs, _ := doc["jobs"].(map[string]any)

	found := 0
	for _, jobName := range []string{"test-core", "test-contrib-matrix"} {
		job, ok := jobs[jobName].(map[string]any)
		if !ok {
			t.Errorf("%s: job %q is missing", workflow, jobName)
			continue
		}
		steps, _ := job["steps"].([]any)

		var sawRerunEnv bool
		for _, raw := range steps {
			step, _ := raw.(map[string]any)
			if env, ok := step["env"].(map[string]any); ok {
				if v, ok := env["RERUN_FAILS"].(string); ok && strings.Contains(v, "inputs.rerun-fails") {
					sawRerunEnv = true
				}
			}
			name, _ := step["name"].(string)
			if name != stepName {
				continue
			}
			found++
			cond, _ := step["if"].(string)
			if cond == "" {
				t.Errorf("%s job %q: step %q has no if: condition, so it uploads coverage "+
					"even from a run that retried", workflow, jobName, stepName)
				continue
			}
			for _, want := range wantClauses {
				if !strings.Contains(cond, want) {
					t.Errorf("%s job %q: step %q condition %q is missing %q. A retried run "+
						"would publish the rerun's partial coverprofile for this SHA.",
						workflow, jobName, stepName, cond, want)
				}
			}
		}
		if !sawRerunEnv {
			t.Errorf("%s job %q never passes inputs.rerun-fails through as RERUN_FAILS, so "+
				"the test scripts cannot act on it", workflow, jobName)
		}
	}
	if found != 2 {
		t.Errorf("found %d %q steps in %s, want 2 (one per test job); a renamed or added "+
			"upload step needs this guard too", found, stepName, workflow)
	}
}
