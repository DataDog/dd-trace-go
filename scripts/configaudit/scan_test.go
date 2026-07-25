// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestScan_DirectOS(t *testing.T) {
	findings, err := scanSyntax(filepath.Join("testdata", "fixture_a"), nil)
	if err != nil {
		t.Fatalf("scanSyntax: %v", err)
	}
	if !hasFinding(findings, "DD_DIRECT_OS", false, false) {
		t.Fatalf("expected direct os.Getenv finding, got %#v", findings)
	}
}

func TestScan_PackageErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/broken\n\ngo 1.25.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.go"), []byte("package broken\nfunc Broken() { missing }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := scan(dir, defaultRecognizers(), nil); err == nil {
		t.Fatal("scan should reject package errors")
	}
}

func TestScanCoverage_ReportsVariantPackageErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/coverage\n\ngo 1.25.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken_windows.go"), []byte("//go:build windows\n\npackage coverage\nfunc Broken() { missing }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	errs := scanCoverage(dir)
	if len(errs) == 0 {
		t.Fatal("expected windows variant package errors")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "windows-amd64") {
		t.Fatalf("coverage errors = %v, want windows-amd64 error", errs)
	}
}

func TestScan_Alias(t *testing.T) {
	findings, err := scanSyntax(filepath.Join("testdata", "fixture_a"), nil)
	if err != nil {
		t.Fatalf("scanSyntax: %v", err)
	}
	if !hasFinding(findings, "DD_ALIASED_OS", false, false) {
		t.Fatalf("expected os.Getenv alias finding, got %#v", findings)
	}
}

func TestScan_PackageScopeAliasAcrossFiles(t *testing.T) {
	findings, err := scanSyntax(filepath.Join("testdata", "fixture_package_alias"), nil)
	if err != nil {
		t.Fatalf("scanSyntax: %v", err)
	}
	if !hasFinding(findings, "DD_PACKAGE_ALIASED_OS", false, false) {
		t.Fatalf("expected package-scope os.Getenv alias finding, got %#v", findings)
	}
}

func TestScan_LocalWrapper(t *testing.T) {
	findings, err := scanSyntax(filepath.Join("testdata", "fixture_a"), nil)
	if err != nil {
		t.Fatalf("scanSyntax: %v", err)
	}
	if !hasFinding(findings, "DD_WRAPPED", false, false) {
		t.Fatalf("expected local wrapper finding, got %#v", findings)
	}
}

func TestScan_UnallowlistedWrapperWithoutCaller(t *testing.T) {
	dir := writeSyntaxFixture(t, map[string]string{
		"wrapper.go": `package fixture

import "os"

func genericRead(key string) string {
	return os.Getenv(key)
}
`,
	})
	findings, err := scanSyntax(dir, nil)
	if err != nil {
		t.Fatalf("scanSyntax: %v", err)
	}
	if !hasFinding(findings, "", true, false) {
		t.Fatalf("expected unallowlisted wrapper raw read to remain unresolved, got %#v", findings)
	}
}

func TestScan_UnallowlistedWrapperAcrossFiles(t *testing.T) {
	dir := writeSyntaxFixture(t, map[string]string{
		"wrapper.go": `package fixture

import "os"

func genericRead(key string) string {
	return os.Getenv(key)
}
`,
		"caller.go": `package fixture

func readConfig() {
	_ = genericRead("DD_CROSS_FILE_WRAPPER")
}
`,
	})
	findings, err := scanSyntax(dir, nil)
	if err != nil {
		t.Fatalf("scanSyntax: %v", err)
	}
	if !hasFinding(findings, "", true, false) {
		t.Fatalf("expected unallowlisted wrapper raw read to remain unresolved, got %#v", findings)
	}
	if !hasFinding(findings, "DD_CROSS_FILE_WRAPPER", false, false) {
		t.Fatalf("expected cross-file wrapper caller finding, got %#v", findings)
	}
}

func TestScan_ExactAllowlistedWrapperSuppressesUnderlyingRead(t *testing.T) {
	dir := writeSyntaxFixture(t, map[string]string{
		"wrapper.go": `package fixture

import env "github.com/DataDog/dd-trace-go/v2/internal/env"

func allowedRead(key string) string {
	return env.Get(key)
}
`,
	})
	findings, err := scanSyntax(dir, rawReadAllowlist{
		{File: "wrapper.go", Func: "allowedRead"}: {},
	})
	if err != nil {
		t.Fatalf("scanSyntax: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("exact allowlisted wrapper should suppress its underlying read, got %#v", findings)
	}
}

func TestDefaultRawReadAllowlistIncludesOnlyExactBootstrapBoundaries(t *testing.T) {
	allow := defaultRawReadAllowlist()
	locations := map[rawReadLocation]struct{}{
		{
			File: "internal/config/bootstrap/telemetry.go",
			Func: "TelemetryEnabled",
		}: {},
		{
			File: "internal/config/bootstrap/appsec.go",
			Func: "resolveAppSecStackTrace",
		}: {},
		{
			File: "internal/config/bootstrap/testoptimization.go",
			Func: "resolveTestOptimization",
		}: {},
	}
	for location := range locations {
		if _, ok := allow[location]; !ok {
			t.Fatalf("bootstrap boundary is not allowlisted: %#v", location)
		}
	}
	for candidate := range allow {
		if _, ok := locations[candidate]; strings.HasPrefix(candidate.File, "internal/config/bootstrap/") && !ok {
			t.Fatalf("unexpected broader bootstrap allowlist entry: %#v", candidate)
		}
	}
}

func TestDefaultRawReadAllowlistIncludesOnlyExactCIProviderBoundary(t *testing.T) {
	allow := defaultRawReadAllowlist()
	location := rawReadLocation{
		File: "internal/civisibility/utils/ci_environment.go",
		Func: "lookupCIProviderEnvironment",
	}
	if _, ok := allow[location]; !ok {
		t.Fatalf("CI provider environment boundary is not allowlisted: %#v", location)
	}
	for candidate := range allow {
		if strings.HasPrefix(candidate.File, "internal/civisibility/") && candidate != location {
			t.Fatalf("unexpected broader CI Visibility allowlist entry: %#v", candidate)
		}
	}
}

func TestScan_StringLiteralsAreUnquoted(t *testing.T) {
	dir := writeSyntaxFixture(t, map[string]string{
		"literal.go": "package fixture\n\nimport \"os\"\n\nfunc readConfig() {\n\t_ = os.Getenv(`DD_RAW_LITERAL`)\n\t_ = os.Getenv(\"DD_\\x45SCAPED_LITERAL\")\n}\n",
	})
	findings, err := scanSyntax(dir, nil)
	if err != nil {
		t.Fatalf("scanSyntax: %v", err)
	}
	for _, key := range []string{"DD_RAW_LITERAL", "DD_ESCAPED_LITERAL"} {
		if !hasFinding(findings, key, false, false) {
			t.Errorf("expected decoded %s finding, got %#v", key, findings)
		}
	}
}

func TestScan_AssignmentAliasUsesLHSPositions(t *testing.T) {
	dir := writeSyntaxFixture(t, map[string]string{
		"assignment.go": `package fixture

import "os"

var value func(string) string

func readConfig() {
	var readEnv func(string) string
	var obj struct{ field func(string) string }
	obj.field, readEnv = value, os.Getenv
	_ = readEnv("DD_POSITIONAL_ALIAS")
}
`,
	})
	findings, err := scanSyntax(dir, nil)
	if err != nil {
		t.Fatalf("scanSyntax: %v", err)
	}
	if !hasFinding(findings, "DD_POSITIONAL_ALIAS", false, false) {
		t.Fatalf("expected positionally assigned alias finding, got %#v", findings)
	}
}

func TestScan_AssignmentClearsAliasOnReassignment(t *testing.T) {
	dir := writeSyntaxFixture(t, map[string]string{
		"assignment.go": `package fixture

import "os"

func notAReader(string) string { return "" }

func readConfig() {
	readEnv := os.Getenv
	readEnv = notAReader
	_ = readEnv("DD_REASSIGNED_ALIAS")
}
`,
	})
	findings, err := scanSyntax(dir, nil)
	if err != nil {
		t.Fatalf("scanSyntax: %v", err)
	}
	if hasFinding(findings, "DD_REASSIGNED_ALIAS", false, false) {
		t.Fatalf("reassigned function must no longer be treated as a reader, got %#v", findings)
	}
}

func TestScan_BlockShadowingDoesNotOverwriteOuterAlias(t *testing.T) {
	dir := writeSyntaxFixture(t, map[string]string{
		"assignment.go": `package fixture

import "os"

var readEnv = os.Getenv

func notAReader(string) string { return "" }

func readConfig() {
	{
		readEnv := notAReader
		_ = readEnv("DD_SHADOWED_ALIAS")
	}
	_ = readEnv("DD_OUTER_ALIAS")
}
`,
	})
	findings, err := scanSyntax(dir, nil)
	if err != nil {
		t.Fatalf("scanSyntax: %v", err)
	}
	if hasFinding(findings, "DD_SHADOWED_ALIAS", false, false) {
		t.Fatalf("shadowed function must not be treated as a reader, got %#v", findings)
	}
	if !hasFinding(findings, "DD_OUTER_ALIAS", false, false) {
		t.Fatalf("outer alias should remain a reader after the block, got %#v", findings)
	}
}

func TestScan_ConditionalAliasRemainsVisibleFailClosed(t *testing.T) {
	dir := writeSyntaxFixture(t, map[string]string{
		"assignment.go": `package fixture

import "os"

func notAReader(string) string { return "" }

func readConfig(enabled bool) {
	var readEnv func(string) string
	if enabled {
		readEnv = os.Getenv
	} else {
		readEnv = notAReader
	}
	_ = readEnv("DD_CONDITIONAL_ALIAS")
}
`,
	})
	findings, err := scanSyntax(dir, nil)
	if err != nil {
		t.Fatalf("scanSyntax: %v", err)
	}
	if !hasFinding(findings, "DD_CONDITIONAL_ALIAS", false, false) {
		t.Fatalf("conditional raw-reader alias must remain visible, got %#v", findings)
	}
}

func TestScan_SwitchFallthroughCarriesAliasState(t *testing.T) {
	dir := writeSyntaxFixture(t, map[string]string{
		"assignment.go": `package fixture

import "os"

func readConfig(selection int) {
	var readEnv func(string) string
	switch selection {
	case 0:
		readEnv = os.Getenv
		fallthrough
	case 1:
		_ = readEnv("DD_FALLTHROUGH_ALIAS")
	}
}
`,
	})
	findings, err := scanSyntax(dir, nil)
	if err != nil {
		t.Fatalf("scanSyntax: %v", err)
	}
	if !hasFinding(findings, "DD_FALLTHROUGH_ALIAS", false, false) {
		t.Fatalf("fallthrough raw-reader alias must remain visible in the next case, got %#v", findings)
	}
}

func TestScan_BranchExitsPreserveMayReaderState(t *testing.T) {
	dir := writeSyntaxFixture(t, map[string]string{
		"branches.go": `package fixture

import "os"

func notAReader(string) string { return "" }

func breakRead() {
	var readEnv func(string) string
	for {
		readEnv = os.Getenv
		break
		readEnv = notAReader
	}
	_ = readEnv("DD_BREAK_ALIAS")
}

func continueRead() {
	var readEnv func(string) string
	for i := 0; i < 1; i++ {
		readEnv = os.Getenv
		continue
		readEnv = notAReader
	}
	_ = readEnv("DD_CONTINUE_ALIAS")
}

func labeledBreakRead() {
	var readEnv func(string) string
outer:
	for {
		readEnv = os.Getenv
		break outer
		readEnv = notAReader
	}
	_ = readEnv("DD_LABELED_BREAK_ALIAS")
}

func labeledContinueRead() {
	var readEnv func(string) string
outer:
	for i := 0; i < 1; i++ {
		for {
			readEnv = os.Getenv
			continue outer
			readEnv = notAReader
		}
	}
	_ = readEnv("DD_LABELED_CONTINUE_ALIAS")
}

func forwardGotoRead() {
	var readEnv func(string) string
	readEnv = os.Getenv
	goto call
	readEnv = notAReader
call:
	_ = readEnv("DD_FORWARD_GOTO_ALIAS")
}

func backwardGotoRead() {
	var readEnv func(string) string
	goto assign
call:
	_ = readEnv("DD_BACKWARD_GOTO_ALIAS")
	return
assign:
	readEnv = os.Getenv
	goto call
}
`,
	})
	findings, err := scanSyntax(dir, nil)
	if err != nil {
		t.Fatalf("scanSyntax: %v", err)
	}
	for _, key := range []string{
		"DD_BREAK_ALIAS",
		"DD_CONTINUE_ALIAS",
		"DD_LABELED_BREAK_ALIAS",
		"DD_LABELED_CONTINUE_ALIAS",
		"DD_FORWARD_GOTO_ALIAS",
		"DD_BACKWARD_GOTO_ALIAS",
	} {
		if !hasFinding(findings, key, false, false) {
			t.Errorf("branch exit must preserve possible reader for %s, got %#v", key, findings)
		}
	}
}

func TestScan_ClosureCapturesPreserveMayReaderState(t *testing.T) {
	dir := writeSyntaxFixture(t, map[string]string{
		"closures.go": `package fixture

import "os"

func notAReader(string) string { return "" }

func closureVariableRead() {
	readEnv := notAReader
	readLater := func() {
		_ = readEnv("DD_CLOSURE_VARIABLE_ALIAS")
	}
	readEnv = os.Getenv
	readLater()
}

func immediateClosureRead() {
	readEnv := os.Getenv
	func() {
		_ = readEnv("DD_IMMEDIATE_CLOSURE_ALIAS")
	}()
}

func deferredClosureRead() {
	readEnv := notAReader
	defer func() {
		_ = readEnv("DD_DEFERRED_CLOSURE_ALIAS")
	}()
	readEnv = os.Getenv
}

func goroutineClosureRead() {
	readEnv := notAReader
	go func() {
		_ = readEnv("DD_GOROUTINE_CLOSURE_ALIAS")
	}()
	readEnv = os.Getenv
}

func uncalledClosureDoesNotEraseReader() {
	readEnv := os.Getenv
	_ = func() {
		readEnv = notAReader
	}
	_ = readEnv("DD_UNCALLED_CLOSURE_ALIAS")
}
`,
	})
	findings, err := scanSyntax(dir, nil)
	if err != nil {
		t.Fatalf("scanSyntax: %v", err)
	}
	for _, key := range []string{
		"DD_CLOSURE_VARIABLE_ALIAS",
		"DD_IMMEDIATE_CLOSURE_ALIAS",
		"DD_DEFERRED_CLOSURE_ALIAS",
		"DD_GOROUTINE_CLOSURE_ALIAS",
		"DD_UNCALLED_CLOSURE_ALIAS",
	} {
		if !hasFinding(findings, key, false, false) {
			t.Errorf("closure capture must preserve possible reader for %s, got %#v", key, findings)
		}
	}
}

func TestScan_ConflictingBuildTagIdentitiesTerminate(t *testing.T) {
	type scanResult struct {
		findings []Finding
		err      error
	}
	result := make(chan scanResult, 1)
	go func() {
		findings, err := scanSyntax(filepath.Join("testdata", "fixture_conflicting_identities"), nil)
		result <- scanResult{findings: findings, err: err}
	}()

	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("scanSyntax: %v", got.err)
		}
		for _, key := range []string{"DD_CONFLICTING_ALIAS", "DD_CONFLICTING_WRAPPER"} {
			if !hasFinding(got.findings, key, true, false) {
				t.Errorf("expected unresolved conflicting identity for %s, got %#v", key, got.findings)
			}
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("conflicting build-tag identities did not converge")
	}
}

func TestScan_Dynamic(t *testing.T) {
	findings, err := scanSyntax(filepath.Join("testdata", "fixture_a"), nil)
	if err != nil {
		t.Fatalf("scanSyntax: %v", err)
	}
	if !hasFinding(findings, "", true, false) {
		t.Fatalf("expected unresolved dynamic finding, got %#v", findings)
	}
}

func TestScan_Suppression(t *testing.T) {
	findings, err := scanSyntax(filepath.Join("testdata", "fixture_a"), nil)
	if err != nil {
		t.Fatalf("scanSyntax: %v", err)
	}
	if !hasFinding(findings, "DD_SUPPRESSED", false, true) {
		t.Fatalf("expected suppression finding, got %#v", findings)
	}
}

func TestDiscoverModules(t *testing.T) {
	root, nested, err := discoverModules(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("discoverModules: %v", err)
	}
	if root.Path != "github.com/DataDog/dd-trace-go/v2" {
		t.Fatalf("root module = %#v", root)
	}
	if len(nested) == 0 {
		t.Fatal("expected nested modules")
	}
}

func TestCollectAuditReportsNonallowlistedSyntaxReadInExcludedDirectory(t *testing.T) {
	dir := writeSyntaxFixture(t, map[string]string{
		"internal/config/config.go": `package config

type SourcePolicy uint8
const SourceStable SourcePolicy = 1
type TelemetryPolicy uint8
const TelemetryReport TelemetryPolicy = 0
type SamplingBoundary uint8
const SampleTracerConstruction SamplingBoundary = 1
type RawDefinition struct {
	Key string
	Sources SourcePolicy
	Telemetry TelemetryPolicy
}
type ConsumerBinding struct {
	ID string
	Consumer string
	Keys []string
	Sampling SamplingBoundary
}

func registerRaw(RawDefinition) {}
func registerBinding(ConsumerBinding) {}

func init() {
	registerRaw(RawDefinition{Key: "DD_REGISTERED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{ID: "test.registered", Consumer: "test", Keys: []string{"DD_REGISTERED"}, Sampling: SampleTracerConstruction})
}
`,
		"internal/env/rogue.go": `package env

import "os"

func Rogue() string {
	return os.Getenv("DD_EXCLUDED_DIRECTORY")
}
`,
		"internal/env/supported_configurations.json": `{
  "version": "1",
  "supportedConfigurations": {
    "DD_EXCLUDED_DIRECTORY": []
  }
}
`,
	})
	result, err := collectAudit(dir, "")
	if err != nil {
		t.Fatalf("collectAudit: %v", err)
	}
	if !hasConfigEntry(result.Unmigrated, "DD_EXCLUDED_DIRECTORY") {
		t.Fatalf("expected nonallowlisted syntax read under internal/env to be reported, got %#v", result)
	}
}

func hasConfigEntry(entries []ConfigEntry, key string) bool {
	for _, entry := range entries {
		if entry.Name == key {
			return true
		}
	}
	return false
}

func hasFinding(findings []Finding, key string, unresolved, suppressed bool) bool {
	for _, finding := range findings {
		if finding.Key == key && finding.Unresolved == unresolved && finding.Suppressed == suppressed {
			return true
		}
	}
	return false
}

func writeSyntaxFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/fixture\n\ngo 1.25.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, contents := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestScan_Fixture(t *testing.T) {
	dir := filepath.Join("testdata", "fixture_a")
	// Recognizer matches by *unqualified* function name for the fixture, since
	// the fixture defines its own helpers. In the real codebase we match by
	// fully-qualified path.
	recog := recognizers{
		ByName: map[string]bool{
			"envGet":  true,
			"boolEnv": true,
			"intEnv":  true,
		},
	}
	got, err := scan(dir, recog, nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	gotKeys := make([]string, 0, len(got))
	for k := range got {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(gotKeys)
	want := []string{"DD_HOSTNAME", "DD_PROFILING_ENABLED", "DD_SITE", "DD_TRACE_AGENT_PORT"}
	if len(gotKeys) != len(want) {
		t.Fatalf("got keys %v, want %v", gotKeys, want)
	}
	for i, k := range want {
		if gotKeys[i] != k {
			t.Errorf("got[%d]=%s, want %s", i, gotKeys[i], k)
		}
	}
	if len(got["DD_SITE"]) != 1 {
		t.Errorf("DD_SITE call-site count = %d, want 1", len(got["DD_SITE"]))
	}
	// DD_ENV is suppressed with //nolint:configaudit and must not appear.
	if len(got["DD_ENV"]) != 0 {
		t.Errorf("DD_ENV should be suppressed, got %d call sites", len(got["DD_ENV"]))
	}
}

func TestScan_RealRepoFindsUnmigratedReads(t *testing.T) {
	// Smoke test: DD_PROFILING_ENABLED is still read directly by the profiler,
	// so it should appear as an unmigrated call site.
	root := filepath.Join("..", "..")
	got, err := scan(root, defaultRecognizers(), defaultExcludes())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got["DD_PROFILING_ENABLED"]) == 0 {
		t.Fatal("expected DD_PROFILING_ENABLED call sites in real repo, got none")
	}
}
