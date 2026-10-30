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
// .github/workflows/otelc.yml, the single reviewable copy: it must be
// non-empty and every entry must be an existing suite directory.
func TestOtelcSuiteSetCoversRealSuites(t *testing.T) {
	_, _, root := testTable(t)

	otelcSuites, err := loadOtelcSuites(root)
	if err != nil {
		t.Fatalf("loadOtelcSuites() = %v", err)
	}
	if len(otelcSuites) == 0 {
		t.Fatal("OTELC_TEST_PACKAGES parsed to an empty set")
	}
	suiteDirs, err := loadSuiteDirs(root)
	if err != nil {
		t.Fatalf("loadSuiteDirs() = %v", err)
	}
	for name := range otelcSuites {
		if !suiteDirs[name] {
			t.Errorf("OTELC_TEST_PACKAGES names %q, which is not a suite under %s", name, integrationRoot)
		}
	}
}

// TestEveryContribModuleHasASuitesEntry walks the real contrib tree: every
// go.mod directory needs an entry, even an empty one, and no entry may name a
// directory without a go.mod.
func TestEveryContribModuleHasASuitesEntry(t *testing.T) {
	tab, _, root := testTable(t)

	want := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, "contrib"), func(p string, d os.DirEntry, err error) error {
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
		t.Fatalf("walk contrib: %v", err)
	}
	if len(want) == 0 {
		t.Fatal("found no go.mod under contrib/")
	}

	for dir := range want {
		if _, ok := tab.IntegrationSuites.Modules[dir]; !ok {
			t.Errorf("%s has a go.mod but no integration-suites.modules entry", dir)
		}
	}
	for dir := range tab.IntegrationSuites.Modules {
		if !want[dir] {
			t.Errorf("integration-suites.modules lists %q, which has no go.mod under contrib/", dir)
		}
	}
}

// TestNamedSuitesExist checks every suite named in OTELC_TEST_PACKAGES or in
// a modules value against the real tree: it must be a directory directly
// under internal/orchestrion/_integration containing Go files.
func TestNamedSuitesExist(t *testing.T) {
	tab, _, root := testTable(t)

	otelcSuites, err := loadOtelcSuites(root)
	if err != nil {
		t.Fatalf("loadOtelcSuites() = %v", err)
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
			files:       []string{"internal/orchestrion/_integration/gin/main_test.go"},
			otelc:       SuiteVerdict{Suites: []string{"gin"}},
			orchestrion: SuiteVerdict{Suites: []string{"gin"}},
			wantOtelc:   true,
			wantOrch:    true,
		},
		{
			name:        "a change inside a suite names that suite, aws edition",
			files:       []string{"internal/orchestrion/_integration/aws.v1/main_test.go"},
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
			// database/sql is required by gorm.v1 and sqlx, so the require
			// closure pulls the gorm suite in next to sql.
			name:         "a required module's closure widens the selection",
			files:        []string{"contrib/database/sql/sql.go"},
			orchestrion:  SuiteVerdict{},
			orchContains: []string{"gorm", "sql"},
			otelc:        SuiteVerdict{Suites: []string{"sql"}},
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
			otelc:       SuiteVerdict{},
			orchestrion: SuiteVerdict{Suites: []string{"twirp", "twirp_quantize"}},
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
