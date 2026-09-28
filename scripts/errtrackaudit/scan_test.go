// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// fixtureLogPath is the import path of the fixture module's stubbed log
// package; tests pass it where production passes productionLogPackagePath.
const fixtureLogPath = "example.com/fixture/internal/log"

// siteKey identifies a scanned site by everything the audit reports, ignoring
// line numbers so the fixture stays diff-friendly.
type siteKey struct {
	File    string
	Func    string
	Level   string
	Message string
	Ignored bool
}

func key(s Site) siteKey {
	return siteKey{File: s.File, Func: s.Func, Level: s.Level, Message: s.Message, Ignored: s.Ignored}
}

func TestScan_Fixture(t *testing.T) {
	dir := filepath.Join("testdata", "fixture")
	sites, err := scan(dir, scanOptions{
		logPackagePath: fixtureLogPath,
		platforms:      []buildPlatform{{goos: "linux", goarch: "amd64"}},
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := map[siteKey]bool{
		// Plain and aliased imports of the log package are identified through
		// type information, not the textual selector.
		{"app/app.go", "Reads", levelError, "failed to marshal agent payload: %s", false}: true,
		{"app/app.go", "Reads", levelWarn, "recovered panic in poll loop: %v", false}:     true,
		{"app/app.go", "Reads", levelError, "invalid value for DD_FOO: %s", false}:        true,
		{"app/app.go", "Reads", levelWarn, "unsupported mode, ignoring it", false}:        true,
		{"app/app.go", "Reads", levelError, "dot-imported error: %s", false}:              true,
		// Constant messages resolve through named constants, not just
		// literals; non-constant messages are reported as such.
		{"app/app.go", "Reads", levelError, "const message: %s", false}: true,
		{"app/app.go", "Reads", levelError, "(non-constant)", false}:    true,
		{"app/app.go", "Reads", levelError, "kept", false}:              true,
		// Directive semantics: a trailing comment on the call line, and a
		// comment inside a multi-line call's span, both suppress the site
		// (kept in the scan, flagged Ignored).
		{"app/app.go", "Reads", levelError, "suppressed by directive: %s", true}: true,
		{"app/app.go", "Reads", levelError, "suppressed multi-line: %s", true}:   true,
		// A call inside a package-level initializer's function literal is
		// audited with "(package-init)" as its enclosing function, mirroring
		// ddtrace/tracer/time_windows.go.
		{"app/app.go", packageInit, levelError, "in package initializer: %s", false}: true,
	}
	if len(sites) != len(want) {
		for _, s := range sites {
			t.Logf("scanned: %+v", s)
		}
		t.Fatalf("scan found %d sites, want %d", len(sites), len(want))
	}
	for _, s := range sites {
		if !want[key(s)] {
			t.Errorf("unexpected site: %+v", s)
		}
		if s.Package != "app" {
			t.Errorf("site %+v has package %q, want %q (module-relative)", s, s.Package, "app")
		}
	}

	// Sites are sorted by file path and line number.
	for i := 1; i < len(sites); i++ {
		if sites[i-1].File > sites[i].File ||
			(sites[i-1].File == sites[i].File && sites[i-1].Line > sites[i].Line) {
			t.Errorf("sites not sorted: %+v before %+v", sites[i-1], sites[i])
		}
	}
}

func TestScan_MergesPlatforms(t *testing.T) {
	dir := filepath.Join("testdata", "fixture")
	sites, err := scan(dir, scanOptions{
		logPackagePath: fixtureLogPath,
		platforms: []buildPlatform{
			{goos: "linux", goarch: "amd64"},
			{goos: "windows", goarch: "amd64"},
		},
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	var windowsSites int
	for _, site := range sites {
		if site.File == "app/windows.go" && site.Message == "windows-only warning" {
			windowsSites++
		}
	}
	if windowsSites != 1 {
		t.Errorf("found %d Windows-only sites, want 1", windowsSites)
	}
	// The portable fixture sites appear in both package loads but must be
	// reported only once.
	if len(sites) != 12 {
		t.Errorf("scan found %d merged sites, want 12", len(sites))
	}
}

func TestScan_FixtureExcludes(t *testing.T) {
	dir := filepath.Join("testdata", "fixture")
	sites, err := scan(dir, scanOptions{logPackagePath: fixtureLogPath, exclude: []string{"/app/"}})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(sites) != 0 {
		t.Fatalf("scan with /app/ excluded found %d sites, want 0", len(sites))
	}
}

func TestScan_ExcludesAreRepoRelative(t *testing.T) {
	// The fixture module itself sits under an ancestor directory literally
	// named "testdata" (scripts/errtrackaudit/testdata/fixture). Matching an
	// exclusion pattern against the absolute checkout path, instead of the
	// repo-relative path, would make "/testdata/" match every file here and
	// silently return an empty inventory.
	dir := filepath.Join("testdata", "fixture")
	sites, err := scan(dir, scanOptions{
		logPackagePath: fixtureLogPath,
		exclude:        []string{"/testdata/"},
		platforms:      []buildPlatform{{goos: "linux", goarch: "amd64"}},
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(sites) == 0 {
		t.Fatal("scan found 0 sites; the exclusion pattern matched the absolute checkout path instead of the repo-relative path")
	}
}

func TestScan_BrokenPackageFails(t *testing.T) {
	// A package that does not type-check must fail the scan loudly: a
	// partial inventory is worse than no inventory.
	dir := filepath.Join("testdata", "broken")
	_, err := scan(dir, scanOptions{logPackagePath: fixtureLogPath})
	if err == nil {
		t.Fatal("scan of a broken package succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "did not load cleanly") || !strings.Contains(err.Error(), "undefinedIdent") {
		t.Errorf("scan error = %v, want a load error mentioning undefinedIdent", err)
	}
}

func TestScan_BrokenDependencyFails(t *testing.T) {
	// A type error inside an imported dependency, not the root package
	// itself, must still fail the scan loudly: go/packages marks the
	// importing package IllTyped with no Errors of its own, so packageErrors
	// must walk the whole dependency graph, not just the root packages.
	dir := filepath.Join("testdata", "brokenimport")
	_, err := scan(dir, scanOptions{logPackagePath: fixtureLogPath})
	if err == nil {
		t.Fatal("scan of a package with a broken dependency succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "did not load cleanly") || !strings.Contains(err.Error(), "undefinedIdent") {
		t.Errorf("scan error = %v, want a load error mentioning undefinedIdent", err)
	}
}

func TestScan_SameLineDistinctCalls(t *testing.T) {
	// gofmt can place two audited calls on one source line (e.g. a short
	// "log.Error(a); log.Warn(b)" body). The dedup key that merges the same
	// call across platform loads must not collapse these into one site.
	dir := filepath.Join("testdata", "sameline")
	sites, err := scan(dir, scanOptions{
		logPackagePath: "example.com/sameline/internal/log",
		platforms:      []buildPlatform{{goos: "linux", goarch: "amd64"}},
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(sites) != 4 {
		t.Fatalf("scan found %d sites, want 4 (two per same-line function)", len(sites))
	}
}

func TestScan_SameLineOrderIsDeterministic(t *testing.T) {
	// F's two calls on testdata/sameline/app/app.go's line come out of an
	// unordered map, so a sort comparator that treats same-line sites as
	// equal lets their relative order vary from run to run. log.Error is at
	// the smaller column, so it must sort first on every run.
	dir := filepath.Join("testdata", "sameline")
	opts := scanOptions{
		logPackagePath: "example.com/sameline/internal/log",
		platforms:      []buildPlatform{{goos: "linux", goarch: "amd64"}},
	}
	for i := 0; i < 20; i++ {
		sites, err := scan(dir, opts)
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		if len(sites) != 4 {
			t.Fatalf("scan found %d sites, want 4 (two per same-line function)", len(sites))
		}
		if sites[0].Level != levelError || sites[1].Level != levelWarn {
			t.Fatalf("run %d: sites = [%s, %s], want [%s, %s] every run", i, sites[0].Level, sites[1].Level, levelError, levelWarn)
		}
	}
}

func TestScan_SameLineIgnoreBindsToNearestCall(t *testing.T) {
	// G's trailing //errtrack:ignore must bind to the nearest preceding
	// call (Warn, "reviewed") only. Keying suppression by line alone, as
	// before, would also suppress Error ("unreviewed") since it shares the
	// line, silently dropping it from the actionable inventory.
	dir := filepath.Join("testdata", "sameline")
	sites, err := scan(dir, scanOptions{
		logPackagePath: "example.com/sameline/internal/log",
		platforms:      []buildPlatform{{goos: "linux", goarch: "amd64"}},
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	var unreviewed, reviewed *Site
	for i := range sites {
		switch sites[i].Message {
		case "unreviewed":
			unreviewed = &sites[i]
		case "reviewed":
			reviewed = &sites[i]
		}
	}
	if unreviewed == nil || reviewed == nil {
		t.Fatalf("scan did not find both G sites: %+v", sites)
	}
	if unreviewed.Ignored {
		t.Error("unreviewed call was ignored; the directive must bind only to the nearest preceding call")
	}
	if !reviewed.Ignored {
		t.Error("reviewed call was not ignored; it is the nearest preceding call to the trailing directive")
	}
}

func TestHasIgnoreDirective(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"bare", "//errtrack:ignore", true},
		{"with reason", "//errtrack:ignore — adopted in #5251", true},
		{"spaced", "// errtrack:ignore", true},
		{"nolint form is not recognized", "//nolint:errtrack", false},
		{"unrelated directive", "//nolint:errcheck", false},
		{"plain comment", "// just a comment", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasIgnoreDirective(tc.text); got != tc.want {
				t.Errorf("hasIgnoreDirective(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestReportingDependencies(t *testing.T) {
	leaf := &packages.Package{PkgPath: "example.com/leaf"}
	middle := &packages.Package{PkgPath: "example.com/middle", Imports: map[string]*packages.Package{"example.com/leaf": leaf}}
	reporting := &packages.Package{PkgPath: "example.com/reporting", Imports: map[string]*packages.Package{"example.com/middle": middle}}
	unrelated := &packages.Package{PkgPath: "example.com/unrelated"}

	excluded, err := reportingDependencies([]*packages.Package{unrelated, reporting}, reporting.PkgPath)
	if err != nil {
		t.Fatalf("reportingDependencies: %v", err)
	}
	for _, path := range []string{reporting.PkgPath, middle.PkgPath, leaf.PkgPath} {
		if !excluded[path] {
			t.Errorf("dependency %q was not excluded", path)
		}
	}
	if excluded[unrelated.PkgPath] {
		t.Errorf("unrelated package %q was excluded", unrelated.PkgPath)
	}
	if _, err := reportingDependencies([]*packages.Package{unrelated}, reporting.PkgPath); err == nil {
		t.Error("missing reporting package did not return an error")
	}
}

func TestScan_RealRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("scanning the real repository is slow; skipped in -short mode")
	}
	root := filepath.Join("..", "..")
	sites, err := scan(root, defaultScanOptions())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// Loading the real repository verifies that the production package graph
	// and platform configurations remain valid. Fixed backlog counts do not
	// belong here because successful migrations intentionally reduce them.
	//
	// contrib/ is deliberately absent from this list: most of contrib/* is
	// out of scope by module boundary, not by an exclusion pattern, but
	// contrib/contrib.go, contrib/os, pubsubtrace, and kafkatrace have no
	// go.mod of their own and are legitimately scanned root-module code
	// (see errtrack-audit.yml's path filters). A blanket "contrib/" check
	// would fail this test the moment any of those gets an audited call.
	for _, s := range sites {
		for _, frag := range []string{"internal/log/", "internal/telemetry/", "_test.go", "testdata/"} {
			if strings.Contains(s.File, frag) {
				t.Errorf("site %s:%d falls in excluded scope %q", s.File, s.Line, frag)
			}
		}
	}
	// ddtrace/tracer keeps many plain log.Error/Warn sites: the reporting API
	// was only adopted on a handful of them so far.
	var tracerSites int
	for _, s := range sites {
		if strings.HasPrefix(s.File, "ddtrace/tracer/") {
			tracerSites++
		}
	}
	if tracerSites == 0 {
		t.Error("expected sites in ddtrace/tracer/, got none")
	}
}
