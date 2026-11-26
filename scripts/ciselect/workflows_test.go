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
// deleted `cache: "false"` literal resumes a light job's pool writes or
// restores potentially incompatible instrumented archives, and
// a deleted workflow_dispatch trigger removes manual re-seeding after a
// cache-key scheme change.
func TestCacheSeedAndDisableLiterals(t *testing.T) {
	root, _ := workflowPaths(t)
	seedLiterals := map[string]int{
		"appsec.yml":                 1, // The macOS matrix job; the other jobs keep the module-only cache.
		"multios-unit-tests.yml":     1,
		"smoke-tests.yml":            4,
		"unit-integration-tests.yml": 3,
		"orchestrion.yml":            1, // The integration-test job; gated on a non-Windows runner.
		"otelc.yml":                  1, // The integration-test job; gated on a non-Windows runner.
		"dynamic-checks.yml":         1, // test-locking-package matrix; the nightly seeds it
	}
	for name, want := range seedLiterals {
		body := readText(t, filepath.Join(root, workflowDir), name)
		if got := len(seedLiteral.FindAllString(body, -1)); got != want {
			t.Errorf("%s has %d seed literals, want %d", name, got, want)
		}
	}
	cacheFalse := map[string]int{
		"static-checks.yml":     5,
		"orchestrion.yml":       1,
		"otelc.yml":             1, // Matrix computation; both instrumented workloads keep caches on.
		"main-branch-tests.yml": 1, // ci-components-depgraph bootstrap; no caches for a go list walk.
	}
	for name, want := range cacheFalse {
		body := readText(t, filepath.Join(root, workflowDir), name)
		if got := len(cacheFalseLiteral.FindAllString(body, -1)); got != want {
			t.Errorf("%s has %d cache-disabled literals, want %d", name, got, want)
		}
	}
	// Manual re-seeding path: main-branch-tests.yml must keep its
	// workflow_dispatch trigger so seeds can be re-made by hand, for
	// example after a cache-key scheme change (cx1 → cx2) or a scheme
	// migration where waiting for the nightly would leave pull requests
	// cold for hours.
	_, on := parseWorkflow(t, filepath.Join(root, workflowDir, "main-branch-tests.yml"))
	if _, ok := on["workflow_dispatch"]; !ok {
		t.Error("main-branch-tests.yml lacks the workflow_dispatch trigger")
	}
}

// setupGoStep returns the `with` mapping of the job's Setup Go step, the
// step that uses the repository's setup-go composite action.
func setupGoStep(t *testing.T, path, jobID string) map[string]any {
	t.Helper()
	doc, _ := parseWorkflow(t, path)
	jobs, _ := doc["jobs"].(map[string]any)
	job, _ := jobs[jobID].(map[string]any)
	if job == nil {
		t.Fatalf("%s has no job %q", path, jobID)
	}
	steps, _ := job["steps"].([]any)
	for _, s := range steps {
		step, _ := s.(map[string]any)
		uses, _ := step["uses"].(string)
		if strings.HasSuffix(uses, "actions/setup-go") {
			with, _ := step["with"].(map[string]any)
			if with == nil {
				t.Fatalf("%s job %q: the setup-go step has no with block", path, jobID)
			}
			return with
		}
	}
	t.Fatalf("%s job %q has no step using the setup-go action", path, jobID)
	return nil
}

// TestGoCachePolicyPerOS pins the per-OS cache policy of APMLP-2249:
// every Windows stratum skips the isolated Go caches — the multios
// measurements in APMLP-2248 put a 1.4 GiB restore at 2.6-3.3m of
// tar.exe extraction for about 2m of saved build time — and the lanes
// whose Windows strata skip cache therefore gate both `cache` and
// `seed` on a non-Windows runner. The integration lanes additionally
// seed only their stable-alias strata, because the shared pool sat at
// its 100 GiB eviction boundary when this was decided, and a dispatch
// that overrides the pinned otelc version must stay restore-only
// because no ordinary pull request can restore that variant. A
// silently dropped condition reverts a stratum to a pointless restore
// or an unbounded seed, and actionlint cannot catch that.
func TestGoCachePolicyPerOS(t *testing.T) {
	root, _ := workflowPaths(t)
	join := func(name string) string {
		return filepath.Join(root, workflowDir, name)
	}

	// Lanes whose Windows matrix strata skip the Go caches. The runner
	// exclusion keys on what the matrix defines: matrix.runs-on for the
	// integration lanes, runner.os for the multios caller. The seed
	// gates carry each lane's extra bounds (stable alias, DRIVER mode,
	// pinned tool version).
	lanes := []struct{ name, jobID, cacheWant, seedWant string }{
		{
			name: "orchestrion.yml", jobID: "integration-test",
			cacheWant: "${{ matrix.runs-on != 'windows' }}",
			seedWant:  "${{ matrix.runs-on != 'windows' && matrix.go-version == 'stable' && matrix.mode == 'DRIVER' }}",
		},
		{
			name: "otelc.yml", jobID: "integration-test",
			cacheWant: "${{ matrix.runs-on != 'windows' }}",
			seedWant:  "${{ matrix.runs-on != 'windows' && matrix.go-version == 'stable' && inputs.otelc-version == '' }}",
		},
		{
			name: "multios-unit-tests.yml", jobID: "test-multi-os",
			cacheWant: "${{ runner.os != 'Windows' }}",
			seedWant:  "${{ runner.os != 'Windows' }}",
		},
	}
	for _, lane := range lanes {
		with := setupGoStep(t, join(lane.name), lane.jobID)
		for _, input := range []struct{ key, want string }{
			{"cache", lane.cacheWant},
			{"seed", lane.seedWant},
		} {
			got, _ := with[input.key].(string)
			if got != input.want {
				t.Errorf("%s job %q: setup-go %s = %q, want %q", lane.name, lane.jobID, input.key, got, input.want)
			}
		}
	}

	// cache: false without quotes decodes as a boolean, so a string-only
	// check can be silently bypassed.
	cacheDisabled := func(v any) bool {
		switch c := v.(type) {
		case bool:
			return !c
		case string:
			return c == "false"
		}
		return false
	}

	// The AppSec macOS matrix job seeds the isolated caches: the setup-go
	// step must keep the default (cloudx) provider and an unconditional
	// seed literal, or the strata silently stop saving on the nightly
	// run. It must also drop the restored test-result entries (go clean
	// -testcache): Go caches -v output and coverage profiles in GOCACHE,
	// so without the clean a package unchanged since the nightly seed
	// can pass without executing on the pull request's runner.
	with := setupGoStep(t, join("appsec.yml"), "macos")
	if got, _ := with["seed"].(string); got != "true" {
		t.Errorf("appsec.yml job macos: setup-go seed = %q, want the literal true", got)
	}
	if cacheDisabled(with["cache"]) {
		t.Error("appsec.yml job macos: setup-go cache is disabled, but the job needs the isolated caches")
	}
	if got, _ := with["cache-provider"].(string); got != "" {
		t.Errorf("appsec.yml job macos: setup-go cache-provider = %q, want the default cloudx provider", got)
	}
	if got, _ := with["workload"].(string); got == "" {
		t.Error("appsec.yml job macos: setup-go workload is empty, so the cache key family falls back to the job name")
	}
	if !jobRunsTestCacheClean(t, join("appsec.yml"), "macos") {
		t.Error("appsec.yml job macos: no go clean -testcache step, so a restored GOCACHE can replay cached test results")
	}
}

// TestMultiosWindowsKeepsAModuleCache pins the proxy-resilience half of
// the Windows policy: the multios lane skips the isolated caches on
// Windows because the combined restore measured net-negative there
// (APMLP-2248), but a fully cold runner also re-fetches the whole
// workspace dependency set from proxy.golang.org on every run, and a
// saturated proxy turns minutes into tens of minutes through the |direct
// fallback. The lane therefore restores a module-only archive on
// Windows (the AppSec go-pkg-mod pattern), seeded by the nightly run
// alone: the restore step must never save on pull requests, and the save
// step must stay gated on the seeding events. A dropped step pair
// silently re-exposes the lane to the proxy, and actionlint cannot
// catch that.
func TestMultiosWindowsKeepsAModuleCache(t *testing.T) {
	root, _ := workflowPaths(t)
	doc, _ := parseWorkflow(t, filepath.Join(root, workflowDir, "multios-unit-tests.yml"))
	jobs, _ := doc["jobs"].(map[string]any)
	job, _ := jobs["test-multi-os"].(map[string]any)
	if job == nil {
		t.Fatal("multios-unit-tests.yml has no job test-multi-os")
	}
	const wantKey = "go-pkg-mod-multios-${{ hashFiles('**/go.sum') }}"
	steps, _ := job["steps"].([]any)
	var restore, save map[string]any
	for _, s := range steps {
		step, _ := s.(map[string]any)
		uses, _ := step["uses"].(string)
		switch {
		case strings.Contains(uses, "cache/restore"):
			restore = step
		case strings.Contains(uses, "cache/save"):
			save = step
		}
	}
	if restore == nil {
		t.Fatal("multios test-multi-os has no actions/cache/restore step")
	}
	if save == nil {
		t.Fatal("multios test-multi-os has no actions/cache/save step")
	}
	for _, step := range []struct {
		name string
		m    map[string]any
	}{{"restore", restore}, {"save", save}} {
		with, _ := step.m["with"].(map[string]any)
		if got, _ := with["key"].(string); got != wantKey {
			t.Errorf("multios module-cache %s key = %q, want %q", step.name, got, wantKey)
		}
		// The archive must live outside the workspace: extracted module
		// trees under the workspace are swept up by the test step's
		// `go list ./...`, and their go.sum files re-key hashFiles between
		// the restore and the save.
		if got, _ := with["path"].(string); got != "${{ runner.temp }}/go_pkg_mod_cache" {
			t.Errorf("multios module-cache %s path = %q, want the RUNNER_TEMP path outside the workspace", step.name, got)
		}
		ifCond, _ := step.m["if"].(string)
		if !strings.Contains(ifCond, "runner.os == 'Windows'") {
			t.Errorf("multios module-cache %s gate %q must target the Windows stratum", step.name, ifCond)
		}
	}
	// The restore must exist on every event and never write: only the
	// save step writes, and only on the seeding events (the nightly
	// schedule through the main-branch-tests caller, or a dispatch on
	// the default branch).
	if got, _ := restore["if"].(string); strings.Contains(got, "github.event_name") {
		t.Errorf("multios module-cache restore gate %q must not exclude events: every run restores", got)
	}
	saveIf, _ := save["if"].(string)
	for _, want := range []string{
		"github.event_name == 'schedule'",
		"github.event_name == 'workflow_dispatch'",
		"github.ref == 'refs/heads/main'",
	} {
		if !strings.Contains(saveIf, want) {
			t.Errorf("multios module-cache save gate %q lacks %q: only seeding events may write", saveIf, want)
		}
	}
	// The save must not carry always(): a failed or cancelled run must not
	// publish a partial archive under an immutable key.
	if strings.Contains(saveIf, "always()") {
		t.Errorf("multios module-cache save gate %q must not force the save on failed runs", saveIf)
	}
	// The job must export GOMODCACHE at the restored directory through
	// GITHUB_ENV (an env var beats a `go env -w` default), before any go
	// command runs, or the restore is decoration.
	hasModcacheExport := false
	for _, s := range steps {
		step, _ := s.(map[string]any)
		run, _ := step["run"].(string)
		if strings.Contains(run, "GOMODCACHE=${RUNNER_TEMP}/go_pkg_mod_cache") && strings.Contains(run, "$GITHUB_ENV") {
			ifCond, _ := step["if"].(string)
			if strings.Contains(ifCond, "runner.os == 'Windows'") {
				hasModcacheExport = true
			}
		}
	}
	if !hasModcacheExport {
		t.Error("multios test-multi-os does not export GOMODCACHE at the RUNNER_TEMP module cache through GITHUB_ENV on Windows; the restored archive is unused")
	}
}

// jobRunsTestCacheClean reports whether the job runs go clean -testcache
// on its restored build cache.
func jobRunsTestCacheClean(t *testing.T, path, jobID string) bool {
	t.Helper()
	doc, _ := parseWorkflow(t, path)
	jobs, _ := doc["jobs"].(map[string]any)
	job, _ := jobs[jobID].(map[string]any)
	if job == nil {
		t.Fatalf("%s has no job %q", path, jobID)
	}
	steps, _ := job["steps"].([]any)
	for _, s := range steps {
		step, _ := s.(map[string]any)
		run, _ := step["run"].(string)
		if strings.Contains(run, "go clean -testcache") {
			return true
		}
	}
	return false
}

// TestAppSecWindowsDevDriveWiring pins the Windows-speedup wiring of the
// one Windows job that does not go through the setup-go action (which
// provisions the Dev Drive itself): the `disabled` job restores the module
// cache into the workspace, so its Dev Drive step must run before that
// restore (the build cache then lives on the Dev Drive before the first
// compile) and redirect only the build cache, because GOMODCACHE is
// managed by the workflow.
func TestAppSecWindowsDevDriveWiring(t *testing.T) {
	root, _ := workflowPaths(t)
	doc, _ := parseWorkflow(t, filepath.Join(root, workflowDir, "appsec.yml"))
	jobs, ok := doc["jobs"].(map[string]any)
	if !ok {
		t.Fatal("appsec.yml has no jobs")
	}
	disabled, ok := jobs["disabled"].(map[string]any)
	if !ok {
		t.Fatal("appsec.yml has no `disabled` job (renamed?)")
	}
	// The Dev Drive step is Windows-only: without a Windows runner in the
	// matrix the step is unreachable and the wiring above is dead.
	strategy, _ := disabled["strategy"].(map[string]any)
	matrix, _ := strategy["matrix"].(map[string]any)
	runsOn, _ := matrix["runs-on"].([]any)
	hasWindows := slices.ContainsFunc(runsOn, func(runner any) bool {
		label, ok := runner.(string)
		return ok && strings.Contains(strings.ToLower(label), "windows")
	})
	if !hasWindows {
		t.Error("appsec.yml `disabled` job needs a Windows runner to reach the Dev Drive step")
	}
	steps, ok := disabled["steps"].([]any)
	if !ok {
		t.Fatal("appsec.yml `disabled` job has no steps")
	}
	indexOf := func(match func(map[string]any) bool) int {
		for i, raw := range steps {
			if step, ok := raw.(map[string]any); ok && match(step) {
				return i
			}
		}
		return -1
	}
	devDrive := indexOf(func(step map[string]any) bool { return step["uses"] == "./.github/actions/windows-dev-drive" })
	if devDrive < 0 {
		t.Fatal("appsec.yml `disabled` job does not use ./.github/actions/windows-dev-drive; " +
			"its Windows builds and cache restore stay on the slow default volume")
	}
	step, _ := steps[devDrive].(map[string]any)
	if got := step["if"]; got != "runner.os == 'Windows'" {
		t.Errorf("the AppSec Dev Drive step has condition %v, want runner.os == 'Windows'", got)
	}
	if with, _ := step["with"].(map[string]any); with["redirect"] != "gocache" {
		t.Errorf("the Dev Drive step must redirect gocache only (GOMODCACHE is restored into the workspace by this job), got with %v", with)
	}
	restore := indexOf(func(step map[string]any) bool { return step["name"] == "Restore Go modules cache" })
	if restore < 0 {
		t.Error("appsec.yml `disabled` job has no `Restore Go modules cache` step (renamed?)")
	}
	if restore >= 0 && restore < devDrive {
		t.Error("the Dev Drive step runs after the module-cache restore; it must come first so the build cache lands on the Dev Drive before the first compile")
	}
}

// TestSeedGatesAndTraceCheckPairing pins the literals that decide whether
// the nightly run seeds: the three seed gates must stay identical and
// unconditional (any go-version spelling seeds; the setup-go action keys on
// the resolved Go minor and floors unsupported toolchains itself), and the
// push-gated version matrix must keep the single highest version on pushes.
// It also pins the trace-check artifact
// name/pattern pairing: a one-sided edit passes every check and silently
// drops a lane's results.
func TestSeedGatesAndTraceCheckPairing(t *testing.T) {
	root, _ := workflowPaths(t)

	// The three seed gates are identical and unconditional: the
	// resolved-minor keying moved version policy into the action.
	ui := readText(t, filepath.Join(root, workflowDir), "unit-integration-tests.yml")
	gates := regexp.MustCompile(`(?m)^\s+seed: .+$`).FindAllString(ui, -1)
	const wantGate = `seed: "true"`
	if len(gates) != 3 {
		t.Fatalf("unit-integration-tests.yml has %d seed literals, want 3", len(gates))
	}
	for i, gate := range gates {
		if got := strings.TrimSpace(gate); got != wantGate {
			t.Errorf("seed gate %d = %q, want %q", i+1, got, wantGate)
		}
	}

	// The push matrix keeps the single highest version; every other
	// event runs both supported versions.
	mb := readText(t, filepath.Join(root, workflowDir), "main-branch-tests.yml")
	const wantMatrix = `go-version: ${{ fromJSON(github.event_name == 'push' && '["stable"]' || '["oldstable", "stable"]') }}`
	if !strings.Contains(mb, wantMatrix) {
		t.Errorf("main-branch-tests.yml lacks the push-gated version matrix %q", wantMatrix)
	}

	// The trace-check download pattern pairs with the upload name.
	doc, _ := parseWorkflow(t, filepath.Join(root, workflowDir, "unit-integration-tests.yml"))
	jobs, _ := doc["jobs"].(map[string]any)
	var name, pattern string
	for _, jobAny := range []any{jobs["test-contrib-matrix"], jobs["test-contrib"]} {
		job, _ := jobAny.(map[string]any)
		steps, _ := job["steps"].([]any)
		for _, s := range steps {
			step, _ := s.(map[string]any)
			uses, _ := step["uses"].(string)
			with, _ := step["with"].(map[string]any)
			switch {
			case strings.Contains(uses, "upload-artifact"):
				name, _ = with["name"].(string)
			case strings.Contains(uses, "download-artifact"):
				pattern, _ = with["pattern"].(string)
			}
		}
	}
	if name == "" || pattern == "" {
		t.Fatalf("trace-check upload name %q or download pattern %q not found", name, pattern)
	}
	if !strings.HasPrefix(pattern, strings.TrimSuffix(name, "-${{ strategy.job-index }}")) {
		t.Errorf("pattern %q does not pair with upload name %q", pattern, name)
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
		"otelc.yml":          "integration-test-done",
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
				"build-apim-callout", "system-tests", "build-system-tests-binaries",
				"tracer-release",
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
			workflow: "otelc.yml",
			job:      "integration-test-done",
			strict: []string{
				"needs.changes.outputs.otelc == 'true'",
				"needs.changes.outputs.otelc-suites != 'NONE'",
				"needs.integration-test.result != 'success'",
				"needs.external-app.result != 'success'",
			},
			deps: []string{"changes", "go-versions-matrix", "service-containers", "integration-test", "external-app"},
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

// TestOTelcGatedJobsKeepTheClassifierCondition pins the if: on the expensive
// jobs. Nothing else asserts it: deleting the condition re-runs the full
// three-OS matrix on every pull request, and the suite stays green.
func TestOTelcGatedJobsKeepTheClassifierCondition(t *testing.T) {
	_, _, root := testTable(t)
	path := filepath.Join(root, workflowDir, "otelc.yml")
	doc, _ := parseWorkflow(t, path)
	jobs, _ := doc["jobs"].(map[string]any)
	for _, job := range []string{"go-versions-matrix", "integration-test", "external-app"} {
		j, _ := jobs[job].(map[string]any)
		cond, _ := j["if"].(string)
		if !strings.Contains(cond, "needs.changes.outputs.otelc == 'true'") {
			t.Errorf("%s lost the classifier condition", job)
		}
	}
}

// TestOrchestrionRunsOnTheMergeQueue pins the merge-queue push trigger on the
// orchestrion lane: it is what makes a narrowed pull-request run safe, and
// removing it stops the full lane run before landing on main with no test
// failing.
func TestOrchestrionRunsOnTheMergeQueue(t *testing.T) {
	_, _, root := testTable(t)
	body := readText(t, root, workflowDir+"/orchestrion.yml")
	if !strings.Contains(body, "- mq-working-branch-**") {
		t.Errorf("orchestrion.yml stopped triggering on merge-queue pushes")
	}
}

// TestOTelcSkipsTheMergeQueue is the counterpart of
// TestOrchestrionRunsOnTheMergeQueue: the merge queue deliberately excludes
// the OTelc lane (see .github/AGENTS.md), so the on.push.branches list must
// keep every merge-queue branch out. GitHub evaluates the entries in order,
// so a "!" entry after a positive entry legitimately excludes the queue, and
// the check honors that ordering. The trigger is decoded as YAML rather than
// searched as text, because the explanatory comment in otelc.yml itself names
// the forbidden pattern.
func TestOTelcSkipsTheMergeQueue(t *testing.T) {
	_, _, root := testTable(t)
	doc, _ := parseWorkflow(t, filepath.Join(root, workflowDir, "otelc.yml"))

	// parseWorkflow handles the decoder's boolean-key rules for the "on"
	// mapping, but the event list ("on: [push, schedule]") and the single
	// event ("on: push") shapes need the raw value.
	var onVal any
	for _, key := range []any{"on", true} {
		if v, ok := doc[fmt.Sprint(key)]; ok {
			onVal = v
		}
	}
	unfiltered := func(why string) {
		t.Errorf("otelc.yml: %s, so push events run on every branch, including "+
			"the queue's. The OTelc lane is deliberately excluded from the merge "+
			"queue (see .github/AGENTS.md).", why)
	}
	var patterns []string // on.push.branches entries, if the push event is filtered
	switch on := onVal.(type) {
	case nil:
		return // no event triggers: nothing runs on merge-queue branches
	case string:
		if on == "push" {
			unfiltered(`on: push carries no branch filter`)
		}
		return
	case []any:
		if slices.ContainsFunc(on, func(v any) bool {
			s, _ := v.(string)
			return s == "push"
		}) {
			unfiltered(`the push event in the "on" list carries no branch filter`)
		}
		return
	case map[string]any:
		pushRaw, has := on["push"]
		if !has {
			return // no push trigger: nothing runs on merge-queue pushes
		}
		push, ok := pushRaw.(map[string]any)
		if !ok {
			unfiltered("on.push is a bare event, not a mapping with a branches filter")
			return
		}
		raw, has := push["branches"]
		if !has || raw == nil {
			// A branchless push event still never runs on a queue branch when
			// branches-ignore excludes the whole queue namespace, or - only
			// without a branches-ignore - when its tags filter keeps it to tag
			// pushes (GitHub: tags or tags-ignore alone suppresses branch pushes;
			// with branches-ignore also defined, branch pushes follow the
			// branches-ignore list instead).
			if ignored := push["branches-ignore"]; ignored != nil {
				for _, pat := range patternList(ignored) {
					wide, err := queueWideBranchFilter(pat)
					if err != nil {
						t.Errorf("otelc.yml: on.push.branches-ignore %q: %v", pat, err)
						continue
					}
					if wide {
						return
					}
				}
				t.Errorf("otelc.yml: on.push.branches-ignore has no queue-wide pattern " +
					"(one of the shape mq-working-branch-** or shorter, such as mq-**; " +
					"a single '*' cannot cross '/'). Branch pushes the list does not " +
					"ignore run the OTelc lane on a merge-queue branch. The lane is " +
					"deliberately excluded from the merge queue (see .github/AGENTS.md).")
				return
			}
			if push["tags"] != nil || push["tags-ignore"] != nil {
				return // tag-only: branch pushes never trigger this workflow
			}
			unfiltered("on.push has no branches filter")
			return
		}
		patterns = patternList(raw)
	default:
		t.Errorf("otelc.yml: the on key decoded to %T, want a mapping or a list", onVal)
		return
	}
	if offender, err := branchListRunsQueueBranch(patterns); err != nil {
		t.Errorf("otelc.yml: %v", err)
	} else if offender != "" {
		t.Errorf("otelc.yml: on.push.branches %q would run the OTelc lane on a "+
			"merge-queue branch that no later \"!\" entry excludes. The lane is "+
			"deliberately excluded from the merge queue (see .github/AGENTS.md).",
			offender)
	}
}

// branchFilterToken is one element of a parsed GitHub branch-filter pattern.
// Per GitHub's filter-pattern rules: "*" matches zero or more characters but
// stops at "/", "**" matches anything, and "?" makes the preceding character
// optional (it matches zero or one of it).
type branchFilterToken struct {
	lit   byte // literal character; 0 for wildcards
	star  bool // "*"
	dstar bool // "**"
	opt   bool // a trailing "?" made this literal optional
}

// parseBranchFilter compiles a branch-filter pattern. Syntax the matchers do
// not support ("!", "+", "[", "\\", a "?" with nothing to modify) is an error,
// so callers fail rather than guess.
func parseBranchFilter(pat string) ([]branchFilterToken, error) {
	if pat == "" {
		return nil, errors.New("empty pattern")
	}
	for _, bad := range []string{"!", "+", "[", "\\"} {
		if strings.Contains(pat, bad) {
			return nil, fmt.Errorf("pattern %q uses unsupported syntax %q", pat, bad)
		}
	}
	var toks []branchFilterToken
	for i := 0; i < len(pat); i++ {
		switch c := pat[i]; c {
		case '*':
			if i+1 < len(pat) && pat[i+1] == '*' {
				toks = append(toks, branchFilterToken{dstar: true})
				i++
			} else {
				toks = append(toks, branchFilterToken{star: true})
			}
		case '?':
			last := len(toks) - 1
			if last < 0 || toks[last].lit == 0 || toks[last].opt {
				return nil, fmt.Errorf("pattern %q: '?' must follow a plain literal character", pat)
			}
			toks[last].opt = true
		default:
			toks = append(toks, branchFilterToken{lit: c})
		}
	}
	return toks, nil
}

// branchPattern is one parsed on.push.branches entry. A leading "!" marks a
// negative pattern: GitHub evaluates the entries in order, a matching
// negative pattern after a positive match excludes the branch, and a
// matching positive pattern after a negative match re-includes the branch.
type branchPattern struct {
	negative bool
	toks     []branchFilterToken
}

// parseBranchPattern parses one branches entry, with the leading "!" of a
// negative entry stripped. A "!" anywhere else stays unsupported syntax, and
// so does a "!" in the -ignore lists: GitHub documents negation for branches
// and paths only.
func parseBranchPattern(pat string) (branchPattern, error) {
	p := branchPattern{}
	if strings.HasPrefix(pat, "!") {
		p.negative = true
		pat = pat[1:]
	}
	toks, err := parseBranchFilter(pat)
	if err != nil {
		return branchPattern{}, err
	}
	p.toks = toks
	return p, nil
}

// matchesBranchFilter reports whether one pattern matches one branch name.
func matchesBranchFilter(pat, branch string) (bool, error) {
	toks, err := parseBranchFilter(pat)
	if err != nil {
		return false, err
	}
	var re strings.Builder
	re.WriteString("^")
	for _, t := range toks {
		switch {
		case t.dstar:
			re.WriteString(".*")
		case t.star:
			re.WriteString("[^/]*")
		default:
			q := regexp.QuoteMeta(string(t.lit))
			if t.opt {
				re.WriteString("(?:" + q + ")?")
			} else {
				re.WriteString(q)
			}
		}
	}
	re.WriteString("$")
	return regexp.MustCompile(re.String()).MatchString(branch), nil
}

func TestMatchesBranchFilter(t *testing.T) {
	for _, tt := range []struct {
		pattern, branch string
		want            bool
	}{
		{"release-v*", "mq-working-branch-main-1a2b3c4d", false},
		{"release-v*", "release-v1.72", true},
		{"mq-working-branch-**", "mq-working-branch-main-1a2b3c4d", true},
		{"mq-*", "mq-working-branch-main-1a2b3c4d", true},
		{"**", "mq-working-branch-main-1a2b3c4d", true},
		// A queue branch has no "/", so a bare "*" matches it; a slash stops "*".
		{"*", "mq-working-branch-main-1a2b3c4d", true},
		{"*", "topic/branch", false},
		{"mq-working-branch-*", "mq-working-branch/main", false},
		// "?" matches zero or one of the preceding character.
		{"colou?r", "color", true},
		{"colou?r", "colour", true},
		{"colou?r", "colouur", false},
		{"mq-working-branch-main-**d?", "mq-working-branch-main-1a2b3c4d", true},
		{"mq-working-branch-main-**d?", "mq-working-branch-main-1a2b3c4", true},
	} {
		got, err := matchesBranchFilter(tt.pattern, tt.branch)
		if err != nil {
			t.Errorf("matchesBranchFilter(%q, %q) unexpected error: %v", tt.pattern, tt.branch, err)
			continue
		}
		if got != tt.want {
			t.Errorf("matchesBranchFilter(%q, %q) = %v, want %v", tt.pattern, tt.branch, got, tt.want)
		}
	}
	for _, pat := range []string{"!mq-*", "mq-+", "mq-[w]*", `mq-\*`, "*?", "a??", "?"} {
		if _, err := matchesBranchFilter(pat, "main"); err == nil {
			t.Errorf("matchesBranchFilter(%q, ...) succeeded, want an error for unsupported syntax", pat)
		}
	}
}

// matchesAnyQueueBranch reports whether a pattern can match any branch the
// merge queue builds on, not just one sample: the queue names them
// mq-working-branch-<target>-<hash>, where <target> is the merged-into
// branch (any character a git ref name allows — including "/", so a
// target such as release/1.x is covered — of any length; modeled
// permissively so a pattern that pins an unlikely target still counts as a
// queue match) and <hash> is a hexadecimal string. Queue branches contain no
// "/", so "*" and "**" agree on them; what separates patterns is where
// their literals can land.
func matchesAnyQueueBranch(pat string) (bool, error) {
	toks, err := parseBranchFilter(pat)
	if err != nil {
		return false, err
	}
	// Forward reachability over (token index, queue position) states, each
	// step consuming zero or one character. Iterating instead of recursing
	// keeps the inTarget→inTarget and inHash→inHash loops from exhausting
	// the stack.
	seen := map[[2]int]bool{{0, 0}: true}
	frontier := [][2]int{{0, 0}}
	for len(frontier) > 0 {
		st := frontier[0]
		frontier = frontier[1:]
		j, s := st[0], st[1]
		if j == len(toks) {
			// The branch may end only once a hash character is consumed.
			if s == inHash {
				return true, nil
			}
			continue
		}
		push := func(nj, ns int) {
			if !seen[[2]int{nj, ns}] {
				seen[[2]int{nj, ns}] = true
				frontier = append(frontier, [2]int{nj, ns})
			}
		}
		t := toks[j]
		switch {
		case t.star || t.dstar:
			// A wildcard may match zero characters, one and stop, or one
			// and keep consuming.
			push(j+1, s)
			for _, ns := range queueStep(s, 0, true) {
				push(j+1, ns)
				push(j, ns)
			}
		case t.opt:
			push(j+1, s) // the optional literal matches nothing
			for _, ns := range queueStep(s, t.lit, false) {
				push(j+1, ns)
			}
		default:
			for _, ns := range queueStep(s, t.lit, false) {
				push(j+1, ns)
			}
		}
	}
	return false, nil
}

// Positions of the symbolic queue branch mq-working-branch-<target>-<hash>:
// each character of the fixed prefix, then the first target character
// (targets are never empty), the remaining target characters, and the hash
// characters (hexadecimal, never empty). The target and the hash have no
// length bound — a real ref may use any of the 255 bytes GitHub allows — so
// the matcher walks these positions instead of enumerating lengths. The
// branch ends once inHash is reached: the "-" separator is behind us by then
// and at least one hash character is consumed.
const queuePrefix = "mq-working-branch-"

const (
	firstTarget = iota + len(queuePrefix) // 18: first <target> character
	inTarget                              // a further <target> character, or the "-" separator
	firstHash                             // first <hash> character
	inHash                                // a further <hash> character; the branch may end here
)

// queueStep returns the positions reachable by consuming one character at
// position s: the literal lit when wild is false, or any character a wildcard
// can match when wild is true (both "*" and "**" match every character of a
// queue branch that a valid ref name can hold: the witness string chooses
// where to place a "/", so "*" never needs to cross one).
func queueStep(s int, lit byte, wild bool) []int {
	switch {
	case s < len(queuePrefix):
		if wild || lit == queuePrefix[s] {
			return []int{s + 1}
		}
	case s == firstTarget:
		// A ref name cannot start with "/".
		if wild || (isRefNameChar(lit) && lit != '/') {
			return []int{inTarget}
		}
	case s == inTarget:
		// The target itself may contain "-" and "/", so one of those is
		// either a target character or (for "-") the separator before <hash>.
		var next []int
		if wild || isRefNameChar(lit) {
			next = append(next, inTarget)
		}
		if wild || lit == '-' {
			next = append(next, firstHash)
		}
		return next
	case s == firstHash, s == inHash:
		if wild || isHexDigit(lit) {
			return []int{inHash}
		}
	}
	return nil
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f'
}

// isRefNameChar reports whether c may appear in a git ref name: git rejects
// ASCII control characters, space, and "~", "^", ":", "?", "*", "[", "\".
// Positional rules (no leading or trailing "/", no "//", no "..", ...) are
// deliberately not modeled: the matcher stays permissive, which can only fire
// the guard on a pattern no real queue branch satisfies, never miss one.
func isRefNameChar(c byte) bool {
	return c > 0x20 && c != '~' && c != '^' && c != ':' && c != '?' &&
		c != '*' && c != '[' && c != '\\'
}

// patternList decodes a branches, branches-ignore, or tags filter entry: a
// single pattern or a list of them.
func patternList(v any) []string {
	switch v := v.(type) {
	case string:
		return []string{v}
	case []any:
		pats := make([]string, 0, len(v))
		for _, p := range v {
			s, _ := p.(string)
			pats = append(pats, s)
		}
		return pats
	default:
		return nil
	}
}

// queueWideBranchFilter reports whether a branches-ignore pattern excludes
// every merge-queue branch, so a branchless push filtered only by
// branches-ignore still keeps the queue out. Only the shape
// <a prefix of mq-working-branch->** qualifies: a "**" matches any
// continuation of the prefix, while anything else (a single "*", which
// cannot cross "/"; trailing literals; a prefix of its own) can leave a queue
// branch unignored. Shapes the helper cannot prove wide fail the guard
// rather than guess.
func queueWideBranchFilter(pat string) (bool, error) {
	toks, err := parseBranchFilter(pat)
	if err != nil {
		return false, err
	}
	k := 0
	for k < len(toks) && k < len(queuePrefix) &&
		toks[k].lit == queuePrefix[k] && !toks[k].opt && !toks[k].star && !toks[k].dstar {
		k++
	}
	return k < len(toks) && toks[k].dstar && !toks[k].opt && k+1 == len(toks), nil
}

func TestQueueWideBranchFilter(t *testing.T) {
	for _, tt := range []struct {
		pattern string
		want    bool
	}{
		{"**", true},
		{"mq-**", true},
		{"mq-working-**", true},
		{"mq-working-branch-**", true},
		{"mq-working-branch-*", false}, // a single "*" cannot cross "/"
		{"mq-working-branch-main-**", false},
		{"release-v**", false},
		{"mq-working-branch-**-x", false},
	} {
		got, err := queueWideBranchFilter(tt.pattern)
		if err != nil {
			t.Errorf("queueWideBranchFilter(%q) unexpected error: %v", tt.pattern, err)
			continue
		}
		if got != tt.want {
			t.Errorf("queueWideBranchFilter(%q) = %v, want %v", tt.pattern, got, tt.want)
		}
	}
}

func TestMatchesAnyQueueBranch(t *testing.T) {
	for _, tt := range []struct {
		pattern string
		want    bool
	}{
		// Patterns are anchored, so anything not naming the queue prefix
		// matches no queue branch.
		{"release-v*", false},
		{"main-*", false},
		{"**", true},
		{"*", true}, // queue branches contain no "/"
		{"mq-*", true},
		{"mq-working-branch-**", true},
		{"mq-working-branch-main-*", true},
		// A single sample branch cannot stand in for the namespace: some hash
		// ends in "e" or "cd", and GitHub's "?" keeps the last "d" optional.
		{"mq-working-branch-main-*e", true},
		{"mq-working-branch-main-*cd", true},
		{"mq-working-branch-main-**d?", true},
		{"mq-working-branch-release-v*-**", true},
		// Targets may contain "/" (a target such as release/1.x names the
		// branch mq-working-branch-release/1.x-<hash>); "*" cannot cross it,
		// "**" can, and a target never starts or ends with a hash.
		{"mq-working-branch-release/**-*", true},
		{"mq-working-branch-release/1.x-*", true},
		{"mq-working-branch-release/1.x", false},
		{"mq-working-branch-/x-*", false},    // a ref name cannot start with "/"
		{"mq-working-branch-main~-*", false}, // "~" is invalid in ref names
		// Neither the target nor the hash has a length bound.
		{"mq-working-branch-" + strings.Repeat("a", 40) + "-*", true},
		{"mq-working-branch-main-" + strings.Repeat("0", 45), true},
		{"mq-working-branch-" + strings.Repeat("b", 60), false}, // never reaches "-<hash>"
		// The queue always appends "-<hash>", so the bare target never matches.
		{"mq-working-branch-main", false},
	} {
		got, err := matchesAnyQueueBranch(tt.pattern)
		if err != nil {
			t.Errorf("matchesAnyQueueBranch(%q) unexpected error: %v", tt.pattern, err)
			continue
		}
		if got != tt.want {
			t.Errorf("matchesAnyQueueBranch(%q) = %v, want %v", tt.pattern, got, tt.want)
		}
	}
}

// branchListRunsQueueBranch evaluates an ordered on.push.branches list the
// way GitHub does: the last matching entry decides whether a branch runs, so
// a negative entry excludes only the branches that no later positive entry
// re-includes. A merge-queue branch runs the workflow when some positive entry
// matches the branch and every negative entry after that entry fails to
// match it. The function returns that entry, or "" when the list keeps every
// queue branch out.
func branchListRunsQueueBranch(pats []string) (string, error) {
	parsed := make([]branchPattern, len(pats))
	for i, pat := range pats {
		p, err := parseBranchPattern(pat)
		if err != nil {
			return "", fmt.Errorf("on.push.branches %q: %w", pat, err)
		}
		parsed[i] = p
	}
	for i, p := range parsed {
		if p.negative {
			continue
		}
		var negs []branchPattern
		for _, later := range parsed[i+1:] {
			if later.negative {
				negs = append(negs, later)
			}
		}
		var runs bool
		var err error
		if len(negs) == 0 {
			runs, err = matchesAnyQueueBranch(pats[i])
		} else {
			runs, err = queueBranchSurvivesNegatives(p, negs)
		}
		if err != nil {
			return "", err
		}
		if runs {
			return pats[i], nil
		}
	}
	return "", nil
}

// queueBranchSurvivesNegatives reports whether some merge-queue branch
// matches the positive pattern while no negative pattern in negs matches
// the branch. Per GitHub's ordered rules, only such a branch runs the
// workflow. The search builds the witness one character at a time over the
// symbolic queue branch, tracking the token states of every pattern at
// once: the positive pattern must consume its last token exactly when the
// branch ends in its hash, and every negative pattern must fail to.
//
// The token states of a pattern form a bitmask: bit k holds when token k can
// still match the witness consumed so far, and bit len(toks) holds when the
// pattern already matched the witness. Negative patterns match permissively
// here exactly as GitHub does: a single "*" cannot cross "/", so a "!"
// entry over-excludes nothing.
func queueBranchSurvivesNegatives(positive branchPattern, negs []branchPattern) (bool, error) {
	all := append([]branchPattern{positive}, negs...)
	for _, p := range all {
		if len(p.toks) > 63 {
			return false, fmt.Errorf("on.push.branches entry with %d tokens: too many to model", len(p.toks))
		}
	}
	lits := map[byte]bool{}
	for _, p := range all {
		for _, t := range p.toks {
			if t.lit != 0 {
				lits[t.lit] = true
			}
		}
	}
	generic, err := genericRefNameChar(lits)
	if err != nil {
		return false, err
	}
	accept := make([]uint64, len(all))
	for i, p := range all {
		accept[i] = 1 << len(p.toks)
	}
	start := make([]uint64, len(all))
	for i, p := range all {
		start[i] = closeStates(p.toks, 1)
	}
	type witness struct {
		s     int
		masks []uint64
	}
	seen := map[string]bool{fmt.Sprintf("%d/%v", 0, start): true}
	frontier := []witness{{0, start}}
	for len(frontier) > 0 {
		w := frontier[0]
		frontier = frontier[1:]
		// The witness ends here when the positive pattern matched and every
		// negative pattern did not.
		if w.s == inHash && w.masks[0]&accept[0] != 0 {
			excluded := false
			for i := 1; i < len(all); i++ {
				if w.masks[i]&accept[i] != 0 {
					excluded = true
					break
				}
			}
			if !excluded {
				return true, nil
			}
		}
		for _, c := range queueEdgeChars(w.s, lits, generic) {
			next := queueNext(w.s, c)
			if len(next) == 0 {
				continue // a merge-queue branch holds no such character here
			}
			masks := make([]uint64, len(all))
			for i, p := range all {
				masks[i] = advanceStates(p.toks, w.masks[i], c)
			}
			if masks[0] == 0 {
				continue // the witness stopped matching the positive pattern
			}
			for _, s := range next {
				key := fmt.Sprintf("%d/%v", s, masks)
				if !seen[key] {
					seen[key] = true
					frontier = append(frontier, witness{s, masks})
				}
			}
		}
	}
	return false, nil
}

// closeStates adds the token states a pattern reaches without consuming a
// character: a wildcard can also match nothing, and a trailing "?" keeps its
// literal optional.
func closeStates(toks []branchFilterToken, states uint64) uint64 {
	for changed := true; changed; {
		changed = false
		for k := range toks {
			t := toks[k]
			if states&(1<<k) != 0 && (t.star || t.dstar || t.opt) && states&(1<<(k+1)) == 0 {
				states |= 1 << (k + 1)
				changed = true
			}
		}
	}
	return states
}

// advanceStates returns the token states reachable after the witness
// consumes character c. A literal consumes c when both are equal, a single
// "*" keeps its state when c is not "/", and a "**" keeps its state on every
// character.
func advanceStates(toks []branchFilterToken, states uint64, c byte) uint64 {
	var next uint64
	for k := range toks {
		if states&(1<<k) == 0 {
			continue
		}
		t := toks[k]
		switch {
		case t.star:
			if c != '/' {
				next |= 1 << k
			}
		case t.dstar:
			next |= 1 << k
		case t.lit == c:
			next |= 1 << (k + 1)
		}
	}
	return closeStates(toks, next)
}

// queueNext returns the positions a merge-queue branch reaches by holding
// character c at position s: one fixed prefix character, a character a ref
// name allows for the target, the "-" separator, or one hexadecimal
// character for the hash.
func queueNext(s int, c byte) []int {
	switch {
	case s < len(queuePrefix):
		if c == queuePrefix[s] {
			return []int{s + 1}
		}
	case s == firstTarget:
		if isRefNameChar(c) && c != '/' {
			return []int{inTarget}
		}
	case s == inTarget:
		var next []int
		if isRefNameChar(c) {
			next = append(next, inTarget)
		}
		if c == '-' {
			next = append(next, firstHash)
		}
		return next
	default: // firstHash and inHash hold hexadecimal hash characters
		if isHexDigit(c) {
			return []int{inHash}
		}
	}
	return nil
}

// queueEdgeChars returns the characters the witness search may place at
// position s. Every prefix position holds one fixed character and the hash
// positions hold hexadecimal characters; a target position holds any
// character a ref name allows. Characters that no pattern distinguishes
// behave the same, so the literals of the patterns plus one representative
// per remaining class suffice: "-", the character that separates the target
// from the hash or continues the target, "/", the one character a single
// "*" cannot cross, and one character no pattern pins.
func queueEdgeChars(s int, lits map[byte]bool, generic byte) []byte {
	switch {
	case s < len(queuePrefix):
		return []byte{queuePrefix[s]}
	case s == firstHash, s == inHash:
		return []byte("0123456789abcdef")
	}
	cs := make([]byte, 0, len(lits)+3)
	for c := range lits {
		cs = append(cs, c)
	}
	slices.Sort(cs)
	return append(cs, '-', '/', generic)
}

// genericRefNameChar returns a character that no pattern pins. Characters
// that equal no literal of any pattern behave the same for every pattern
// and for the queue branch itself, so one representative covers them all.
func genericRefNameChar(lits map[byte]bool) (byte, error) {
	for c := byte('g'); c <= '~'; c++ {
		if !lits[c] && c != '-' && c != '/' && isRefNameChar(c) {
			return c, nil
		}
	}
	return 0, errors.New("no unpinned ref-name character is left to witness with")
}

func TestBranchListRunsQueueBranch(t *testing.T) {
	for _, tt := range []struct {
		patterns []string
		want     string // the entry that runs the lane on a queue branch, "" when none does
		wantErr  bool
	}{
		// A list of positive entries alone: the entry that can match a queue
		// branch runs the lane on it.
		{patterns: []string{"release-v*"}},
		{patterns: []string{"mq-working-branch-**"}, want: "mq-working-branch-**"},
		{patterns: []string{"mq-*"}, want: "mq-*"}, // a target without "/" lets a single "*" match
		// An ordered "!mq-working-branch-**" entry keeps every queue branch out;
		// a "!mq-*" entry does not, because a single "*" cannot cross the "/" of
		// a target such as release/1.x.
		{patterns: []string{"**", "!mq-working-branch-**"}},
		{patterns: []string{"**", "!mq-*"}, want: "**"},
		{patterns: []string{"**", "!**"}},
		// The entries evaluate in order: a negative entry excludes only the
		// branches of earlier positive entries, and a later positive entry
		// re-includes.
		{patterns: []string{"!mq-**", "**"}, want: "**"},
		{patterns: []string{"**", "!mq-working-branch-**", "mq-**"}, want: "mq-**"},
		{patterns: []string{"mq-working-branch-main-**", "!mq-working-branch-main-**"}},
		// A hash that does not end in "e" escapes the negative entry.
		{patterns: []string{"mq-working-branch-main-*", "!mq-working-branch-main-*e"}, want: "mq-working-branch-main-*"},
		// The negative entries together cover the main and release targets
		// only; every other target survives.
		{patterns: []string{"**", "!mq-working-branch-main-**", "!mq-working-branch-release-**"}, want: "**"},
		{patterns: []string{"**", "!mq-working-branch-release/1.x-**"}, want: "**"},
		// A trailing "?" keeps its literal optional, so "mq-**d?" still matches
		// every queue branch: with "d" absent, "**" swallows the rest.
		{patterns: []string{"**", "!mq-**d?"}},
		// No positive entry: no branch runs at all (GitHub requires a positive
		// entry before a negative one).
		{patterns: []string{"!**", "!mq-**"}},
		// Unsupported syntax fails the check rather than guess: "!" only at
		// the front, and the rest of the syntax rules still hold.
		{patterns: []string{"**", "!mq-["}, wantErr: true},
		{patterns: []string{"**", "mq!x"}, wantErr: true},
		{patterns: []string{"!"}, wantErr: true},
		// More tokens than the witness search tracks.
		{patterns: []string{"**", "!" + strings.Repeat("a", 64)}, wantErr: true},
	} {
		got, err := branchListRunsQueueBranch(tt.patterns)
		if tt.wantErr {
			if err == nil {
				t.Errorf("branchListRunsQueueBranch(%q) succeeded, want an error for unsupported syntax", tt.patterns)
			}
			continue
		}
		if err != nil {
			t.Errorf("branchListRunsQueueBranch(%q) unexpected error: %v", tt.patterns, err)
			continue
		}
		if got != tt.want {
			t.Errorf("branchListRunsQueueBranch(%q) = %q, want %q", tt.patterns, got, tt.want)
		}
	}
}

// TestToolsCacheVersionKeyComesFromRepoAction pins the wiring a past
// regression showed: the tools cache keys on setup-go's go-version-key
// output, which only the repository's setup-go action provides. A
// tools-cache caller that reads it from the upstream actions/setup-go
// keys every restore on an empty version.
func TestToolsCacheVersionKeyComesFromRepoAction(t *testing.T) {
	root, _ := workflowPaths(t)
	files, err := filepath.Glob(filepath.Join(root, workflowDir, "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	refRe := regexp.MustCompile(`steps\.([A-Za-z0-9_-]+)\.outputs\.go-version-key`)
	for _, file := range files {
		doc, _ := parseWorkflow(t, file)
		jobs, _ := doc["jobs"].(map[string]any)
		for jobName, jobAny := range jobs {
			job, _ := jobAny.(map[string]any)
			steps, _ := job["steps"].([]any)
			usesByID := map[string]string{}
			for _, s := range steps {
				step, _ := s.(map[string]any)
				id, _ := step["id"].(string)
				uses, _ := step["uses"].(string)
				if id != "" {
					usesByID[id] = uses
				}
			}
			for _, s := range steps {
				step, _ := s.(map[string]any)
				uses, _ := step["uses"].(string)
				if !strings.Contains(uses, "actions/tools-cache") {
					continue
				}
				with, _ := step["with"].(map[string]any)
				version, _ := with["go-version-key"].(string)
				m := refRe.FindStringSubmatch(version)
				if m == nil {
					t.Errorf("%s job %s: tools-cache go-version-key = %q, want steps.<id>.outputs.go-version-key",
						filepath.Base(file), jobName, version)
					continue
				}
				source, ok := usesByID[m[1]]
				if !ok {
					t.Errorf("%s job %s: tools-cache reads go-version-key from missing step %q",
						filepath.Base(file), jobName, m[1])
					continue
				}
				if !strings.HasPrefix(source, "./.github/actions/setup-go") {
					t.Errorf("%s job %s: tools-cache reads go-version-key from %q, want the repository's setup-go action",
						filepath.Base(file), jobName, source)
				}
			}
		}
	}
}

// TestGoFloorPinsMatchGoModDirective checks the deliberate numeric
// floor pins (appsec Docker matrices and the smoke floor probe) against
// the root go.mod go directive. When a Go release moves oldstable, these
// pins and the directive move together on release day; a silent drift
// leaves the floor untested.
func TestGoFloorPinsMatchGoModDirective(t *testing.T) {
	root, _ := workflowPaths(t)
	directiveBytes, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	directive := regexp.MustCompile(`(?m)^go (1\.[0-9]+)`).FindSubmatch(directiveBytes)
	if directive == nil {
		t.Fatal("root go.mod has no go directive")
	}
	floor := string(directive[1])
	pins := map[string]string{
		"appsec.yml":      `(?m)^\s*go-version: \[ "1\.27", "(1\.[0-9]+)" \]$`,
		"smoke-tests.yml": `(?m)^\s*go-version: "(1\.[0-9]+)" # lowest supported version`,
	}
	for name, pinRe := range pins {
		body := readText(t, filepath.Join(root, workflowDir), name)
		re := regexp.MustCompile(pinRe)
		m := re.FindAllStringSubmatch(body, -1)
		if len(m) == 0 {
			t.Errorf("%s has no numeric floor pin matching %s", name, pinRe)
			continue
		}
		for _, match := range m {
			if match[1] != floor {
				t.Errorf("%s floor pin %q does not match the go.mod directive %q", name, match[1], floor)
			}
		}
	}
}
