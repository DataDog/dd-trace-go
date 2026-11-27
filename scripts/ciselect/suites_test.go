// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestOtelcSuiteSetCoversRealSuites reads the otelc lane's suite set from
// .github/ci-components.yml, derived from otelc-extra-suites plus every
// /otelc module entry: it must be non-empty and every entry must be an
// existing suite directory.
func TestOtelcSuiteSetCoversRealSuites(t *testing.T) {
	tab, _, root := testTable(t)

	otelcSuites, err := tab.otelcSuites()
	if err != nil {
		t.Fatalf("otelcSuites() = %v", err)
	}
	if len(otelcSuites) == 0 {
		t.Fatal("otelc suite set is empty")
	}
	suiteDirs, err := loadSuiteDirs(root)
	if err != nil {
		t.Fatalf("loadSuiteDirs() = %v", err)
	}
	for name := range otelcSuites {
		if !suiteDirs[name] {
			t.Errorf("otelc suite set names %q, which is not a suite under %s", name, integrationRoot)
		}
	}
}

// TestEveryContribModuleHasASuitesEntry walks the real contrib and
// instrumentation trees: every go.mod directory needs an entry, even an
// empty one, and no entry may name a directory without a go.mod.
func TestEveryContribModuleHasASuitesEntry(t *testing.T) {
	tab, _, root := testTable(t)

	want := map[string]bool{}
	for _, tree := range []string{"contrib", "instrumentation"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && d.Name() == "go.mod" {
				rel, err := filepath.Rel(root, filepath.Dir(p))
				if err != nil {
					return err
				}
				want[filepath.ToSlash(rel)] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", tree, err)
		}
	}
	if len(want) == 0 {
		t.Fatal("found no go.mod under contrib/ or instrumentation/")
	}

	for dir := range want {
		if _, ok := tab.IntegrationSuites.Modules[dir]; !ok {
			t.Errorf("%s has a go.mod but no integration-suites.modules entry", dir)
		}
	}
	for dir := range tab.IntegrationSuites.Modules {
		if !want[dir] {
			t.Errorf("integration-suites.modules lists %q, which has no go.mod under contrib/ or instrumentation/", dir)
		}
	}
}

// TestNamedSuitesExist checks every suite named in otelc-extra-suites or in
// a modules value against the real tree: it must be a directory directly
// under internal/orchestrion/_integration containing Go files.
func TestNamedSuitesExist(t *testing.T) {
	tab, _, root := testTable(t)

	otelcSuites, err := tab.otelcSuites()
	if err != nil {
		t.Fatalf("otelcSuites() = %v", err)
	}
	suites := make(map[string]bool, len(otelcSuites))
	for s := range otelcSuites {
		suites[s] = true
	}
	for _, list := range tab.IntegrationSuites.Modules {
		for _, s := range list {
			suites[s] = true
		}
	}
	if len(suites) == 0 {
		t.Fatal("no suite named anywhere in integration-suites")
	}
	for name := range suites {
		if name == "internal" {
			t.Errorf("the internal helper tree is not a suite, but it is named")
			continue
		}
		dir := filepath.Join(root, integrationRoot, name)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			t.Errorf("suite %q is not a directory under %s", name, integrationRoot)
			continue
		}
		hasGo, err := dirHasGoFiles(dir)
		if err != nil {
			t.Errorf("suite %q: %v", name, err)
			continue
		}
		if !hasGo {
			t.Errorf("suite %q holds no Go files", name)
		}
	}
}

// TestSuiteSelection is the behavioural core: per change set, the verdict each
// lane reaches and what happens to its gates.
func TestSuiteSelection(t *testing.T) {
	tab, graph, root := testTable(t)

	tests := []struct {
		name         string
		files        []string
		otelc        SuiteVerdict
		orchestrion  SuiteVerdict
		orchContains []string
		wantOtelc    bool
		wantOrch     bool
	}{
		{
			name:  "a docs change runs no suite on either lane",
			files: []string{"README.md"},
		},
		{
			// The docs component claims README.md everywhere, including inside
			// code directories; static-docs gates neither lane, so the path
			// contributes nothing even though fiber.v2 has a suite.
			name:  "a doc edit inside a contrib module runs no suite",
			files: []string{"contrib/gofiber/fiber.v2/README.md"},
		},
		{
			name:  "a doc edit inside the integration module runs no suite",
			files: []string{"internal/orchestrion/_integration/README.md"},
		},
		{
			name:        "an otelc harness change runs the gin suite on both lanes",
			files:       []string{"contrib/gin-gonic/gin/otelc/hooks.go"},
			otelc:       SuiteVerdict{Suites: []string{"gin"}},
			orchestrion: SuiteVerdict{Suites: []string{"gin"}},
			wantOtelc:   true,
			wantOrch:    true,
		},
		{
			// The orchestrion-aspects component claims this path and does not
			// gate otelc; the suite verdict propagates the gate. rules_test.go
			// names the same path "an aspect file needs orchestrion and
			// regeneration only".
			name:        "an aspect file propagates the otelc gate through its suite",
			files:       []string{"contrib/gin-gonic/gin/orchestrion.yml"},
			otelc:       SuiteVerdict{Suites: []string{"gin"}},
			orchestrion: SuiteVerdict{Suites: []string{"gin"}},
			wantOtelc:   true,
			wantOrch:    true,
		},
		{
			// An ALL verdict propagates the lane's gate too.
			name:        "core runs every suite on both lanes",
			files:       []string{"ddtrace/tracer/tracer.go"},
			otelc:       SuiteVerdict{All: true},
			orchestrion: SuiteVerdict{All: true},
			wantOtelc:   true,
			wantOrch:    true,
		},
		{
			name:        "a change inside a suite names that suite",
			files:       []string{"internal/orchestrion/_integration/gin/base.go"},
			otelc:       SuiteVerdict{Suites: []string{"gin"}},
			orchestrion: SuiteVerdict{Suites: []string{"gin"}},
			wantOtelc:   true,
			wantOrch:    true,
		},
		{
			name:        "a change inside a suite names that suite, aws edition",
			files:       []string{"internal/orchestrion/_integration/aws.v1/aws.go"},
			otelc:       SuiteVerdict{Suites: []string{"aws.v1"}},
			orchestrion: SuiteVerdict{Suites: []string{"aws.v1"}},
			wantOtelc:   true,
			wantOrch:    true,
		},
		{
			// The shared module file compiles into every suite, so the
			// shared-module rule runs everything on both lanes. The
			// orchestrion-suite component also gates the orchestrion lane
			// directly; otelc comes only from the shared-module escalation.
			name:        "the shared _integration module file runs every suite",
			files:       []string{"internal/orchestrion/_integration/go.mod"},
			otelc:       SuiteVerdict{All: true},
			orchestrion: SuiteVerdict{All: true},
			wantOtelc:   true,
			wantOrch:    true,
		},
		{
			name:        "the public otelc package escalates only the otelc lane",
			files:       []string{"otelc/all/otel.instrumentation.go"},
			otelc:       SuiteVerdict{All: true},
			orchestrion: SuiteVerdict{},
			wantOtelc:   true,
		},
		{
			name:        "the generator escalates only the orchestrion lane",
			files:       []string{"internal/orchestrion/generator/main.go"},
			otelc:       SuiteVerdict{},
			orchestrion: SuiteVerdict{All: true},
			wantOrch:    true,
		},
		{
			// The external-app job consumes these inputs: no suite, no
			// fail-open, but the otelc gate stays on so the build still runs.
			name:        "an external-app input runs no suite but keeps the gate",
			files:       []string{"internal/apps/otelc-external-app/main.go"},
			otelc:       SuiteVerdict{},
			orchestrion: SuiteVerdict{},
			wantOtelc:   true,
		},
		{
			name:        "the external-app build script runs no suite but keeps the gate",
			files:       []string{"scripts/build_otelc_external_app.sh"},
			otelc:       SuiteVerdict{},
			orchestrion: SuiteVerdict{},
			wantOtelc:   true,
		},
		{
			// The workflow-gates self-reference turns the otelc gate on with
			// no suite information, so fail-open applies.
			name:        "editing the otelc workflow escalates the otelc lane",
			files:       []string{".github/workflows/otelc.yml"},
			otelc:       SuiteVerdict{All: true},
			orchestrion: SuiteVerdict{},
			wantOtelc:   true,
		},
		{
			// contrib gates orchestrion but the module maps to no suite and no
			// module requires it, so fail-open widens the orchestrion lane;
			// otelc stays off.
			name:        "a contrib module with an empty mapping fails open",
			files:       []string{"contrib/miekg/dns/dns.go"},
			otelc:       SuiteVerdict{},
			orchestrion: SuiteVerdict{All: true},
			wantOrch:    true,
		},
		{
			// The vault suite is not in the otelc lane's list and nothing in
			// the otelc lane imports it, so the suite's import closure proves the
			// otelc lane unaffected: the coarse orchestrion-suite gate is
			// suppressed instead of failing the lane open.
			name:        "a suite outside the otelc lane does not turn otelc on",
			files:       []string{"internal/orchestrion/_integration/vault/x.go"},
			otelc:       SuiteVerdict{},
			orchestrion: SuiteVerdict{Suites: []string{"vault"}},
			wantOrch:    true,
		},
		{
			// Suppression only refines the suppressed path's own vote: an
			// otelc-relevant change in the same change set still fails the
			// otelc lane open.
			name: "a suppressed suite and an otelc change fail the otelc lane open",
			files: []string{
				"internal/orchestrion/_integration/vault/x.go",
				"otelc/all/otel.instrumentation.go",
			},
			otelc:       SuiteVerdict{All: true},
			orchestrion: SuiteVerdict{Suites: []string{"vault"}},
			wantOtelc:   true,
			wantOrch:    true,
		},
		{
			// dns contributes nothing and fails the orchestrion lane open;
			// gin keeps the otelc lane narrowed to its own suite.
			name:        "a narrow change and a fail-open change union per lane",
			files:       []string{"contrib/gin-gonic/gin/gin.go", "contrib/miekg/dns/dns.go"},
			otelc:       SuiteVerdict{Suites: []string{"gin"}},
			orchestrion: SuiteVerdict{All: true},
			wantOtelc:   true,
			wantOrch:    true,
		},
		{
			// The weavers inject net/http spans into suites that never import
			// the module, so it is a shared-modules entry: full lane, no
			// narrowing, even though 20+ suites could be named.
			name:        "a shared-modules change runs the full lane",
			files:       []string{"contrib/net/http/http.go"},
			otelc:       SuiteVerdict{All: true},
			orchestrion: SuiteVerdict{All: true},
			wantOtelc:   true,
			wantOrch:    true,
		},
		{
			// The slog aspect links WrapHandler into every slog.New caller
			// (the echo.v5 suite calls it) with no import the graphs follow,
			// so slog is a shared-modules entry too: the orchestrion lane runs
			// in full, and otelc stays off because nothing otelc-side weaves
			// it. A shared entry reaches only the lanes its paths gate.
			name:        "an injected-aspect module runs the orchestrion lane in full",
			files:       []string{"contrib/log/slog/slog.go"},
			otelc:       SuiteVerdict{},
			orchestrion: SuiteVerdict{All: true},
			wantOrch:    true,
		},
		{
			// A manifest change can upgrade an external dependency for every
			// module that requires it (pubsub.v1 and pubsub.v2 both require
			// gRPC), and the repo-internal require graph cannot see that, so
			// the gated lanes fail open. grpc gates both lanes.
			name:        "a workspace manifest change fails the gated lanes open",
			files:       []string{"contrib/google.golang.org/grpc/go.mod"},
			otelc:       SuiteVerdict{All: true},
			orchestrion: SuiteVerdict{All: true},
			wantOtelc:   true,
			wantOrch:    true,
		},
		{
			// The manifest rule scoped per lane: dns gates orchestrion only,
			// so only that lane fails open.
			name:        "a manifest change fails only the gated lane open",
			files:       []string{"contrib/miekg/dns/go.sum"},
			otelc:       SuiteVerdict{},
			orchestrion: SuiteVerdict{All: true},
			wantOrch:    true,
		},
		{
			// database/sql is required by gorm.v1 and sqlx, so the require
			// closure pulls the gorm suite in next to sql, in both lanes.
			name:         "a required module's closure widens the selection",
			files:        []string{"contrib/database/sql/sql.go"},
			orchestrion:  SuiteVerdict{},
			orchContains: []string{"gorm", "sql"},
			otelc:        SuiteVerdict{Suites: []string{"gorm", "sql"}},
			wantOtelc:    true,
			wantOrch:     true,
		},
		{
			// The otelc harness mapping follows its parent: the mongo suite
			// exercises both driver generations (both_drivers_test.go).
			name:        "the mongo v2 otelc harness runs both mongo suites",
			files:       []string{"contrib/go.mongodb.org/mongo-driver.v2/otelc/otelc.yaml"},
			otelc:       SuiteVerdict{Suites: []string{"mongo", "mongo.v2"}},
			orchestrion: SuiteVerdict{Suites: []string{"mongo", "mongo.v2"}},
			wantOtelc:   true,
			wantOrch:    true,
		},
		{
			name:         "a vault contrib change names the vault suite",
			files:        []string{"contrib/hashicorp/vault/vault.go"},
			orchContains: []string{"vault"},
			wantOrch:     true,
		},
		{
			// Suites import helper suites: parallel_testing_retries (plus the
			// civisibility_failnow variants) import civisibilitytest, so a
			// change to the helper runs the importers too.
			name:         "an imported helper suite pulls in its importers",
			files:        []string{"internal/orchestrion/_integration/civisibilitytest/payloads.go"},
			orchContains: []string{"civisibilitytest", "parallel_testing_retries", "civisibility_failnow", "civisibility_failnow_benchmark", "civisibility_failnow_retry"},
			wantOrch:     true,
		},
		{
			name:        "an imported suite pulls in its importer",
			files:       []string{"internal/orchestrion/_integration/twirp/base.go"},
			otelc:       SuiteVerdict{Suites: []string{"twirp", "twirp_quantize"}},
			orchestrion: SuiteVerdict{Suites: []string{"twirp", "twirp_quantize"}},
			wantOtelc:   true,
			wantOrch:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := tab.classify(tt.files, graph)
			if err != nil {
				t.Fatalf("classify(%v) = %v", tt.files, err)
			}
			if err := tab.selectSuites(root, res); err != nil {
				t.Fatalf("selectSuites(%v) = %v", tt.files, err)
			}
			if !reflect.DeepEqual(res.Suites[laneOtelc], tt.otelc) {
				t.Errorf("otelc suites = %+v, want %+v", res.Suites[laneOtelc], tt.otelc)
			}
			// orchContains replaces the exact orchestrion assertion when the
			// closure's exact membership is not worth pinning.
			if len(tt.orchContains) == 0 && !reflect.DeepEqual(res.Suites[laneOrchestrion], tt.orchestrion) {
				t.Errorf("orchestrion suites = %+v, want %+v", res.Suites[laneOrchestrion], tt.orchestrion)
			}
			for _, want := range tt.orchContains {
				if res.Suites[laneOrchestrion].All {
					t.Errorf("orchContains %q asserted but the orchestrion verdict is ALL", want)
					continue
				}
				if !slices.Contains(res.Suites[laneOrchestrion].Suites, want) {
					t.Errorf("orchestrion suites %v miss %q", res.Suites[laneOrchestrion].Suites, want)
				}
			}
			if res.Gates[laneOtelc] != tt.wantOtelc {
				t.Errorf("Gates[%q] = %t, want %t", laneOtelc, res.Gates[laneOtelc], tt.wantOtelc)
			}
			if res.Gates[laneOrchestrion] != tt.wantOrch {
				t.Errorf("Gates[%q] = %t, want %t", laneOrchestrion, res.Gates[laneOrchestrion], tt.wantOrch)
			}
		})
	}
}

// TestSuiteJSONPayload pins the shape of the -json output: the per-lane suite
// verdicts and the per-path component gates must both be present.
func TestSuiteJSONPayload(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{"-json"}, strings.NewReader("contrib/gin-gonic/gin/gin.go\n"), &stdout)
	if err != nil {
		t.Fatalf("run(-json) = %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode -json output: %v", err)
	}
	for _, key := range []string{"suites", "path_gates"} {
		if _, ok := payload[key]; !ok {
			t.Errorf("-json output misses the %q key; got %v", key, payload)
		}
	}
}

// TestGitHubOutputPropagatesSuiteGates checks the composite action's inputs:
// the -github-output gates must carry the gate a suite verdict propagates.
func TestGitHubOutputPropagatesSuiteGates(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{"-github-output"}, strings.NewReader("contrib/gin-gonic/gin/gin.go\n"), &stdout)
	if err != nil {
		t.Fatalf("run(-github-output) = %v", err)
	}
	if !strings.Contains(stdout.String(), "otelc=true\n") {
		t.Errorf("-github-output misses the propagated otelc gate:\n%s", stdout.String())
	}
}

// TestListOtelcModules checks that -list-otelc-modules returns exactly the
// same sorted list as discoverOtelcModules, and that every entry has a
// go.mod on disk.
func TestListOtelcModules(t *testing.T) {
	_, _, root := testTable(t)
	var stdout bytes.Buffer
	if err := run([]string{"-list-otelc-modules"}, strings.NewReader(""), &stdout); err != nil {
		t.Fatalf("run(-list-otelc-modules) = %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("-list-otelc-modules returned an empty list")
	}
	expected, err := discoverOtelcModules(root)
	if err != nil {
		t.Fatalf("discoverOtelcModules() = %v", err)
	}
	if !slices.Equal(lines, expected) {
		t.Errorf("-list-otelc-modules = %v, discoverOtelcModules = %v", lines, expected)
	}
	for _, m := range lines {
		if _, err := os.Stat(filepath.Join(root, m, "go.mod")); err != nil {
			t.Errorf("-list-otelc-modules %q: %v", m, err)
		}
	}
}

// TestListOtelcSuites checks that -list-otelc-suites returns a sorted,
// non-empty list that matches what otelcSuites() returns.
func TestListOtelcSuites(t *testing.T) {
	tab, _, _ := testTable(t)
	var stdout bytes.Buffer
	if err := run([]string{"-list-otelc-suites"}, strings.NewReader(""), &stdout); err != nil {
		t.Fatalf("run(-list-otelc-suites) = %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("-list-otelc-suites returned an empty list")
	}
	if !slices.IsSorted(lines) {
		t.Errorf("-list-otelc-suites output is not sorted: %v", lines)
	}
	suites, err := tab.otelcSuites()
	if err != nil {
		t.Fatalf("otelcSuites() = %v", err)
	}
	for _, s := range lines {
		if !suites[s] {
			t.Errorf("-list-otelc-suites returned %q, not in otelcSuites()", s)
		}
	}
	if len(lines) != len(suites) {
		t.Errorf("-list-otelc-suites returned %d suites, otelcSuites() has %d", len(lines), len(suites))
	}
}

// TestLoadOtelcSuitesOnlyIgnoresInvalidComponentRules verifies that the
// fail-open ALL path still resolves otelc suites even when an unrelated gate or
// component rule in .github/ci-components.yml fails loadTable validation.
func TestLoadOtelcSuitesOnlyIgnoresInvalidComponentRules(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	brokenTable := `
gates:
  - otelc
components:
  broken:
    paths: ["["]
    gates: [nonexistent-gate]
integration-suites:
  otelc-extra-suites:
    - otelc
    - gls
  modules:
    contrib/gin-gonic/gin/otelc: [ gin ]
    contrib/hashicorp/vault: [ vault ]
`
	if err := os.WriteFile(filepath.Join(dir, tableRelPath), []byte(brokenTable), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTable(dir); err == nil {
		t.Fatal("expected loadTable to reject the invalid component rule")
	}
	got, err := loadOtelcSuitesOnly(dir)
	if err != nil {
		t.Fatalf("loadOtelcSuitesOnly() = %v", err)
	}
	want := map[string]bool{"otelc": true, "gls": true, "gin": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loadOtelcSuitesOnly() = %v, want %v", got, want)
	}

	t.Run("rejects misspelled key inside integration-suites", func(t *testing.T) {
		typoTable := `
integration-suites:
  otelc-extra-suite:
    - otelc
  modules:
    contrib/gin-gonic/gin/otelc: [ gin ]
`
		if err := os.WriteFile(filepath.Join(dir, tableRelPath), []byte(typoTable), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadOtelcSuitesOnly(dir); err == nil {
			t.Fatal("expected loadOtelcSuitesOnly to reject misspelled otelc-extra-suite key")
		}
	})

	t.Run("rejects missing integration-suites section", func(t *testing.T) {
		noSectionTable := `
gates:
  - otelc
`
		if err := os.WriteFile(filepath.Join(dir, tableRelPath), []byte(noSectionTable), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadOtelcSuitesOnly(dir); err == nil {
			t.Fatal("expected loadOtelcSuitesOnly to fail when integration-suites section is missing")
		}
	})

	t.Run("rejects empty otelc suite set", func(t *testing.T) {
		emptyTable := `
integration-suites:
  otelc-extra-suites: []
  modules:
    contrib/gin-gonic/gin: [ gin ]
`
		if err := os.WriteFile(filepath.Join(dir, tableRelPath), []byte(emptyTable), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadOtelcSuitesOnly(dir); err == nil {
			t.Fatal("expected loadOtelcSuitesOnly to fail when otelc suite set is empty")
		}
	})
}

// TestDiscoverOtelcModulesEdgeCases verifies that discoverOtelcModules skips
// parent directories without a go.mod (such as contrib/os), discovers otelc
// modules outside contrib/, and ignores dot-prefixed and testdata directories.
func TestDiscoverOtelcModulesEdgeCases(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string) {
		t.Helper()
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("module example.com/test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("otelc/all/go.mod")
	write("contrib/gin/go.mod")
	write("contrib/gin/otelc/go.mod")
	write("contrib/os/otelc/go.mod")     // parent contrib/os has no go.mod
	write("ddtrace/tracer/otelc/go.mod") // non-contrib otelc module, parent has no go.mod
	write("testdata/fake/otelc/go.mod")  // must be skipped
	write(".worktrees/wt/otelc/go.mod")  // must be skipped

	got, err := discoverOtelcModules(dir)
	if err != nil {
		t.Fatalf("discoverOtelcModules() = %v", err)
	}
	want := []string{
		"contrib/gin",
		"contrib/gin/otelc",
		"contrib/os/otelc",
		"ddtrace/tracer/otelc",
		"otelc/all",
	}
	if !slices.Equal(got, want) {
		t.Errorf("discoverOtelcModules() = %v, want %v", got, want)
	}

	t.Run("missing otelc/all/go.mod", func(t *testing.T) {
		noAllDir := t.TempDir()
		writeIn := func(rel string) {
			t.Helper()
			abs := filepath.Join(noAllDir, rel)
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(abs, []byte("module example.com/test\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		writeIn("contrib/gin/otelc/go.mod")
		if _, err := discoverOtelcModules(noAllDir); err == nil {
			t.Fatal("expected discoverOtelcModules to fail when otelc/all/go.mod is missing")
		}
	})
}

// TestEveryOtelcModuleMapsToASuite prevents a contrib/**/otelc entry with an
// empty list [] from silently dropping its integration suite from the otelc lane.
func TestEveryOtelcModuleMapsToASuite(t *testing.T) {
	tab, _, _ := testTable(t)
	var count int
	for dir, suites := range tab.IntegrationSuites.Modules {
		if !strings.HasSuffix(dir, "/otelc") {
			continue
		}
		count++
		if len(suites) == 0 {
			t.Errorf("%s has an empty suite list in %s; map it to at least one suite so the otelc lane runs it", dir, tableRelPath)
		}
	}
	if count == 0 {
		t.Fatal("found no */otelc entries in integration-suites.modules")
	}
}

// TestSuiteFlagsPinTheSentinels locks in the CLI contract the composite
// action consumes: suite names one per line, ALL and NONE as sentinels.
func TestSuiteFlagsPinTheSentinels(t *testing.T) {
	tests := []struct {
		name string
		flag string
		in   string
		want string
	}{
		{name: "otelc narrowed", flag: "-otelc-suites", in: "contrib/gin-gonic/gin/otelc/otelc.yaml\n", want: "gin\n"},
		{name: "otelc multi-suite", flag: "-otelc-suites", in: "contrib/go.mongodb.org/mongo-driver.v2/otelc/otelc.yaml\n", want: "mongo\nmongo.v2\n"},
		{name: "otelc all", flag: "-otelc-suites", in: "ddtrace/tracer/tracer.go\n", want: "ALL\n"},
		{name: "orchestrion none", flag: "-orchestrion-suites", in: "README.md\n", want: "NONE\n"},
		{name: "orchestrion fail-open", flag: "-orchestrion-suites", in: "contrib/miekg/dns/dns.go\n", want: "ALL\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			if err := run([]string{tt.flag}, strings.NewReader(tt.in), &stdout); err != nil {
				t.Fatalf("run(%s) = %v", tt.flag, err)
			}
			if stdout.String() != tt.want {
				t.Errorf("run(%s, %q) = %q, want %q", tt.flag, tt.in, stdout.String(), tt.want)
			}
		})
	}
}
