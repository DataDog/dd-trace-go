// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build scripts

package main

import (
	"cmp"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

const (
	rootModule  = "github.com/DataDog/dd-trace-go/v2"
	otelcAll    = "github.com/DataDog/dd-trace-go/otelc/all/v2"
	externalApp = "example.com/otelcexternalapp"

	slogOtelc    = "github.com/DataDog/dd-trace-go/contrib/log/slog/otelc/v2"
	logrusOtelc  = "github.com/DataDog/dd-trace-go/contrib/sirupsen/logrus/otelc/v2"
	remoteTarget = "github.com/remote/lib"
)

// localModules returns a module set that treats every dd-trace-go module path
// used by the tests as local, like the real fixmodules run does.
func localModules(paths ...string) map[string]GoMod {
	mods := make(map[string]GoMod, len(paths))
	for _, p := range paths {
		mods[p] = GoMod{Module: ModPath{Path: p}}
	}
	return mods
}

func rep(oldPath, newPath string) Replace {
	return Replace{
		Old: Module{Path: oldPath},
		New: Module{Path: newPath},
	}
}

// parseReplaces parses go.mod content and returns its replace directives in
// file order, together with the number of replace statements (standalone lines
// plus blocks).
func parseReplaces(t *testing.T, content string) (replaces []*modfile.Replace, statements int) {
	t.Helper()
	f, err := modfile.Parse("go.mod", []byte(content), nil)
	if err != nil {
		t.Fatalf("modfile.Parse: %v", err)
	}
	for _, stmt := range f.Syntax.Stmt {
		if isReplaceStmt(stmt) {
			statements++
		}
	}
	return f.Replace, statements
}

func TestSyncReplacesSortedBlock(t *testing.T) {
	mods := localModules(
		rootModule,
		"github.com/DataDog/dd-trace-go/contrib/a/otelc/v2",
		"github.com/DataDog/dd-trace-go/contrib/b/otelc/v2",
		"github.com/DataDog/dd-trace-go/contrib/c/otelc/v2",
		"github.com/DataDog/dd-trace-go/contrib/stale/otelc/v2",
	)

	// The file holds a replace block in reverse order, a standalone local
	// replace for the root module, a stale local replace that must be dropped,
	// and a non-local replace that must be preserved untouched.
	source := `module ` + otelcAll + `

go 1.26.0

replace ` + rootModule + ` => ../..

replace (
	github.com/DataDog/dd-trace-go/contrib/c/otelc/v2 => ../../contrib/c/otelc // c is sorted after b
	github.com/DataDog/dd-trace-go/contrib/a/otelc/v2 => ../../contrib/a/otelc
)

require github.com/DataDog/dd-trace-go/contrib/stale/otelc/v2 v2.14.0-dev

replace github.com/DataDog/dd-trace-go/contrib/stale/otelc/v2 => ../../contrib/stale/otelc

replace ` + remoteTarget + ` => ../remote
`
	want := []Replace{
		rep(rootModule, "../.."),
		rep("github.com/DataDog/dd-trace-go/contrib/a/otelc/v2", "../../contrib/a/otelc"),
		rep("github.com/DataDog/dd-trace-go/contrib/b/otelc/v2", "../../contrib/b/otelc"),
		rep("github.com/DataDog/dd-trace-go/contrib/c/otelc/v2", "../../contrib/c/otelc"),
	}

	got, err := applyReplaceSync("go.mod", []byte(source), mods, want)
	if err != nil {
		t.Fatalf("applyReplaceSync: %v", err)
	}

	replaces, statements := parseReplaces(t, string(got))
	if statements != 1 {
		t.Fatalf("expected exactly one replace statement, got %d in:\n%s", statements, got)
	}
	gotPaths := make([]string, 0, len(replaces))
	for _, r := range replaces {
		if !r.Syntax.InBlock {
			t.Errorf("replace %s is not in a block:\n%s", r.Old.Path, got)
		}
		gotPaths = append(gotPaths, r.Old.Path)
	}
	if !slices.IsSorted(gotPaths) {
		t.Errorf("replace block is not sorted: %v", gotPaths)
	}
	wantPaths := []string{
		"github.com/DataDog/dd-trace-go/contrib/a/otelc/v2",
		"github.com/DataDog/dd-trace-go/contrib/b/otelc/v2",
		"github.com/DataDog/dd-trace-go/contrib/c/otelc/v2",
		rootModule,
		remoteTarget,
	}
	if !slices.Equal(gotPaths, wantPaths) {
		t.Errorf("replace paths = %v, want %v", gotPaths, wantPaths)
	}

	remoteKept := false
	cCommentKept := false
	for _, r := range replaces {
		switch r.Old.Path {
		case "github.com/DataDog/dd-trace-go/contrib/stale/otelc/v2":
			t.Errorf("stale local replace was not dropped")
		case remoteTarget:
			remoteKept = r.New.Path == "../remote"
		case "github.com/DataDog/dd-trace-go/contrib/c/otelc/v2":
			for _, c := range r.Syntax.Suffix {
				if strings.Contains(c.Token, "c is sorted after b") {
					cCommentKept = true
				}
			}
		}
	}
	if !remoteKept {
		t.Errorf("non-local replace was not preserved:\n%s", got)
	}
	if !cCommentKept {
		t.Errorf("comment on the c replace was not preserved:\n%s", got)
	}

	// A second run with the same wanted set must produce identical content.
	again, err := applyReplaceSync("go.mod", got, mods, want)
	if err != nil {
		t.Fatalf("applyReplaceSync (second run): %v", err)
	}
	if string(again) != string(got) {
		t.Errorf("applyReplaceSync is not idempotent; second run changed the file:\n%s", again)
	}
}

func TestSyncReplacesStandaloneUntouched(t *testing.T) {
	mods := localModules(
		otelcAll,
		"github.com/DataDog/dd-trace-go/contrib/a/otelc/v2",
	)

	// A module whose replace set does not change and that holds no replace
	// block keeps a zero diff, even with several standalone replaces.
	source := `module ` + externalApp + `

go 1.26.0

replace ` + otelcAll + ` => ../otelc/all

replace github.com/DataDog/dd-trace-go/contrib/a/otelc/v2 => ../contrib/a/otelc
`
	want := []Replace{
		rep(otelcAll, "../otelc/all"),
		rep("github.com/DataDog/dd-trace-go/contrib/a/otelc/v2", "../contrib/a/otelc"),
	}

	got, err := applyReplaceSync("go.mod", []byte(source), mods, want)
	if err != nil {
		t.Fatalf("applyReplaceSync: %v", err)
	}
	if string(got) != source {
		t.Errorf("untouched module was rewritten:\n%s", got)
	}
}

func TestApplyReplaceSync(t *testing.T) {
	mods := localModules(rootModule, otelcAll)
	const header = "module " + externalApp + "\n\ngo 1.26.0\n"
	const single = header + "\nreplace " + rootModule + " => ../..\n"
	const block = header + "\nreplace (\n\t" + rootModule + " => ../..\n\t" + otelcAll + " => ../otelc/all\n)\n"

	for _, tt := range []struct {
		name   string
		source string
		want   []Replace
		output string
	}{
		{
			name:   "one remaining replace is standalone",
			source: block,
			want:   []Replace{rep(rootModule, "../..")},
			output: single,
		},
		{
			name:   "no remaining replaces removes the block",
			source: block,
			output: header,
		},
		{
			name:   "stale target is updated",
			source: header + "\nreplace " + rootModule + " => ../old-root\n",
			want:   []Replace{rep(rootModule, "../..")},
			output: single,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := applyReplaceSync("go.mod", []byte(tt.source), mods, tt.want)
			if err != nil {
				t.Fatalf("applyReplaceSync: %v", err)
			}
			if string(got) != tt.output {
				t.Errorf("applyReplaceSync = %q, want %q", got, tt.output)
			}
		})
	}
}

func TestTopoSortModules(t *testing.T) {
	contribOtelc := "github.com/DataDog/dd-trace-go/contrib/foo/otelc/v2"

	t.Run("dependencies before dependents", func(t *testing.T) {
		edges := map[string][]string{
			// example.com sorts before every github.com path, but the external
			// app depends on otelc/all, which depends on the contrib module.
			externalApp:  {otelcAll},
			otelcAll:     {contribOtelc},
			contribOtelc: {},
		}
		paths := []string{externalApp, otelcAll, contribOtelc}
		got := topoSortModules(paths, func(p string) []string { return edges[p] })
		want := []string{contribOtelc, otelcAll, externalApp}
		if !slices.Equal(got, want) {
			t.Errorf("topoSortModules = %v, want %v", got, want)
		}
	})

	t.Run("diamond convergence", func(t *testing.T) {
		edges := map[string][]string{
			"top":   {"left", "right"},
			"left":  {"base"},
			"right": {"base"},
		}
		got := topoSortModules([]string{"top", "right", "left", "base"}, func(p string) []string {
			return edges[p]
		})
		want := []string{"base", "left", "right", "top"}
		if !slices.Equal(got, want) {
			t.Errorf("topoSortModules = %v, want %v", got, want)
		}
	})

	t.Run("ties break alphabetically", func(t *testing.T) {
		paths := []string{"example.com/z", "example.com/a", "example.com/m"}
		got := topoSortModules(paths, func(string) []string { return nil })
		want := []string{"example.com/a", "example.com/m", "example.com/z"}
		if !slices.Equal(got, want) {
			t.Errorf("topoSortModules = %v, want %v", got, want)
		}
	})

	t.Run("modules outside the list impose no order", func(t *testing.T) {
		edges := map[string][]string{
			"example.com/a": {"example.com/unlisted"},
			"example.com/b": nil,
		}
		got := topoSortModules([]string{"example.com/b", "example.com/a"}, func(p string) []string {
			return edges[p]
		})
		want := []string{"example.com/a", "example.com/b"}
		if !slices.Equal(got, want) {
			t.Errorf("topoSortModules = %v, want %v", got, want)
		}
	})

	t.Run("cycles come before dependents that sort earlier", func(t *testing.T) {
		edges := map[string][]string{
			"example.com/a": {"example.com/b"},
			"example.com/b": {"example.com/a"},
			"example.com/0": {"example.com/a"},
		}
		got := topoSortModules([]string{"example.com/0", "example.com/b", "example.com/a"}, func(p string) []string {
			return edges[p]
		})
		want := []string{"example.com/a", "example.com/b", "example.com/0"}
		if !slices.Equal(got, want) {
			t.Errorf("topoSortModules = %v, want %v", got, want)
		}
	})
}

func TestSyncReplacesPreservesVersionPinsAndParenComments(t *testing.T) {
	mods := localModules(
		rootModule,
		"github.com/DataDog/dd-trace-go/contrib/a/otelc/v2",
		"github.com/DataDog/dd-trace-go/contrib/b/otelc/v2",
	)

	source := `module ` + otelcAll + `

go 1.26.0

replace ( // lparen note
	github.com/DataDog/dd-trace-go/contrib/a/otelc/v2 => ../../contrib/a/otelc
	github.com/DataDog/dd-trace-go/contrib/b/otelc/v2 v2.1.0 => ../../contrib/b/otelc
	// rparen before note
) // rparen suffix note
`
	want := []Replace{
		rep(rootModule, "../.."),
		rep("github.com/DataDog/dd-trace-go/contrib/a/otelc/v2", "../../contrib/a/otelc"),
	}

	got, err := applyReplaceSync("go.mod", []byte(source), mods, want)
	if err != nil {
		t.Fatalf("applyReplaceSync: %v", err)
	}
	gotStr := string(got)

	if !strings.Contains(gotStr, "github.com/DataDog/dd-trace-go/contrib/b/otelc/v2 v2.1.0 => ../../contrib/b/otelc") {
		t.Errorf("version-pinned local replace was dropped:\n%s", gotStr)
	}
	for _, wantComment := range []string{"// lparen note", "// rparen before note", "// rparen suffix note"} {
		if !strings.Contains(gotStr, wantComment) {
			t.Errorf("missing comment %q in:\n%s", wantComment, gotStr)
		}
	}
}

func TestSyncReplacesMovesBlockAfterRequire(t *testing.T) {
	mods := localModules(
		rootModule,
		"github.com/DataDog/dd-trace-go/contrib/a/v2",
	)
	// Simulates a brand-new module after Pass 1 added replace (...) and Pass 2
	// go mod tidy appended require (...) after it.
	source := `module github.com/DataDog/dd-trace-go/contrib/a/otelc/v2

go 1.26.0

replace (
	github.com/DataDog/dd-trace-go/contrib/a/v2 => ..
	github.com/DataDog/dd-trace-go/v2 => ../../../..
)

require (
	github.com/DataDog/dd-trace-go/contrib/a/v2 v2.0.0-00010101000000-000000000000
	github.com/DataDog/dd-trace-go/v2 v2.0.0-00010101000000-000000000000
)
`
	want := []Replace{
		rep("github.com/DataDog/dd-trace-go/contrib/a/v2", ".."),
		rep(rootModule, "../../../.."),
	}
	got, err := applyReplaceSync("go.mod", []byte(source), mods, want)
	if err != nil {
		t.Fatalf("applyReplaceSync: %v", err)
	}
	gotStr := string(got)
	if strings.Index(gotStr, "require (") > strings.Index(gotStr, "replace (") {
		t.Fatalf("expected replace block after require block:\n%s", gotStr)
	}
	again, err := applyReplaceSync("go.mod", got, mods, want)
	if err != nil {
		t.Fatalf("applyReplaceSync (second run): %v", err)
	}
	if string(again) != gotStr {
		t.Errorf("not idempotent after moving replace block:\n%s", again)
	}
}

func TestSyncReplacesDeduplicatesIdenticalLocalReplaces(t *testing.T) {
	mods := localModules(rootModule, slogOtelc)
	source := `module ` + otelcAll + `

go 1.26.0

replace (
	github.com/DataDog/dd-trace-go/contrib/log/slog/otelc/v2 => ../../contrib/log/slog/otelc
	github.com/DataDog/dd-trace-go/contrib/log/slog/otelc/v2 => ../../contrib/log/slog/otelc
	github.com/DataDog/dd-trace-go/v2 => ../..
)
`
	want := []Replace{
		rep(slogOtelc, "../../contrib/log/slog/otelc"),
		rep(rootModule, "../.."),
	}
	got, err := applyReplaceSync("go.mod", []byte(source), mods, want)
	if err != nil {
		t.Fatalf("applyReplaceSync: %v", err)
	}
	replaces, statements := parseReplaces(t, string(got))
	if statements != 1 {
		t.Fatalf("expected 1 replace block, got %d:\n%s", statements, got)
	}
	gotPaths := make([]string, 0, len(replaces))
	for _, r := range replaces {
		gotPaths = append(gotPaths, r.Old.Path)
	}
	wantPaths := []string{slogOtelc, rootModule}
	if !slices.Equal(gotPaths, wantPaths) {
		t.Errorf("replace paths = %v, want %v\n%s", gotPaths, wantPaths, got)
	}
}

func TestResolveLocalModule(t *testing.T) {
	mods := localModules(
		rootModule,
		"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration",
		"github.com/DataDog/dd-trace-go/tools/v2fix",
		"example.com/chidep",
	)
	tests := []struct {
		imp  string
		want string
		ok   bool
	}{
		{imp: "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer", want: rootModule, ok: true},
		{imp: "github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/gin", want: "github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration", ok: true},
		{imp: "github.com/DataDog/dd-trace-go/tools/v2fix/internal/pkg", want: "github.com/DataDog/dd-trace-go/tools/v2fix", ok: true},
		{imp: "example.com/chidep/v5router", want: "example.com/chidep", ok: true},
		{imp: "example.com/chidepextra/pkg", want: "", ok: false},
	}
	for _, tc := range tests {
		got, ok := resolveLocalModule(tc.imp, mods)
		if got != tc.want || ok != tc.ok {
			t.Errorf("resolveLocalModule(%q) = (%q, %v), want (%q, %v)", tc.imp, got, ok, tc.want, tc.ok)
		}
	}
}

func TestLocalDepsReplacesFollowsImportOnlyEdge(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// External app imports otelc/all; otelc/all blank-imports contrib/a/otelc;
	// neither has go.mod require lines yet.
	write("contrib/a/otelc/go.mod", "module github.com/DataDog/dd-trace-go/contrib/a/otelc/v2\n\ngo 1.26.0\n")
	write("contrib/a/otelc/otelc.go", "package otelc\n")
	write("otelc/all/go.mod", "module "+otelcAll+"\n\ngo 1.26.0\n")
	write("otelc/all/otel.instrumentation.go", "//go:build tools\npackage all\nimport _ \"github.com/DataDog/dd-trace-go/contrib/a/otelc/v2\"\n")
	write("internal/apps/otelc-external-app/go.mod", "module "+externalApp+"\n\ngo 1.26.0\n")
	write("internal/apps/otelc-external-app/otel.instrumentation.go", "//go:build tools\npackage tools\nimport _ \""+otelcAll+"\"\n")

	mods := map[string]GoMod{
		"github.com/DataDog/dd-trace-go/contrib/a/otelc/v2": {
			dir:    filepath.Join(dir, "contrib/a/otelc"),
			Module: ModPath{Path: "github.com/DataDog/dd-trace-go/contrib/a/otelc/v2"},
		},
		otelcAll: {
			dir:    filepath.Join(dir, "otelc/all"),
			Module: ModPath{Path: otelcAll},
		},
		externalApp: {
			dir:    filepath.Join(dir, "internal/apps/otelc-external-app"),
			Module: ModPath{Path: externalApp},
		},
	}

	deps := newLocalDeps(mods)
	got := deps.replaces(externalApp)
	want := []Replace{
		rep("github.com/DataDog/dd-trace-go/contrib/a/otelc/v2", "../../../contrib/a/otelc"),
		rep(otelcAll, "../../../otelc/all"),
	}
	if !slices.Equal(got, want) {
		t.Errorf("replaces(%s) = %v, want %v", externalApp, got, want)
	}
}

// TestGetModGoFilesNestedModules checks that the walk survives nested modules
// regardless of where they sort relative to the parent's files. scripts/errtrackaudit
// was the first module with several nested testdata modules; the previous walk
// returned fs.SkipAll at the first nested go.mod, aborting the whole walk, so
// parent files sorting after it were dropped and later nested modules were
// never recorded.
func TestGetModGoFilesNestedModules(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Parent module. "aaa" sorts before "go.mod" and "z.go", so the walk meets
	// the first nested module before the parent's own go.mod.
	write("go.mod", "module example.com/m\n\ngo 1.26\n")
	write("main.go", "package m\n")
	write("z.go", "package m\n")

	// First nested module, met before the parent's go.mod.
	write("aaa/go.mod", "module example.com/m/aaa\n\ngo 1.26\n")
	write("aaa/a.go", "package aaa\n")     // sorts before aaa/go.mod, filtered by prefix
	write("aaa/zz.go", "package aaa\n")    // sorts after aaa/go.mod, skipped by fs.SkipDir
	write("aaa/sub/s.go", "package sub\n") // in a skipped subtree

	// Second nested module, sorting after every parent file: the old fs.SkipAll
	// abort recorded only the first nested module, never this one.
	write("zzz/go.mod", "module example.com/m/zzz\n\ngo 1.26\n")
	write("zzz/z.go", "package zzz\n")

	// A sibling directory sharing the first nested module's name prefix but
	// without its own go.mod: its files belong to the parent module.
	write("aaaextra/x.go", "package main\n")

	files, err := getModGoFiles(GoMod{dir: dir})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		filepath.Join(dir, "aaaextra", "x.go"),
		filepath.Join(dir, "main.go"),
		filepath.Join(dir, "z.go"),
	}
	slices.Sort(files)
	if !slices.Equal(files, want) {
		t.Errorf("getModGoFiles() = %v\nwant %v", files, want)
	}
}

// TestSortedReplaceBlockMergesCleanly reproduces the conflict-reduction this
// change targets: two PRs that each add a different otelc module to the sorted
// replace block of a go.mod must merge without a conflict.
func TestSortedReplaceBlockMergesCleanly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	mods := localModules(
		rootModule,
		otelcAll,
		"github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/otelc/v2",
		"github.com/DataDog/dd-trace-go/contrib/k8s.io/client-go/otelc/v2",
		"github.com/DataDog/dd-trace-go/contrib/labstack/echo.v4/otelc/v2",
		"github.com/DataDog/dd-trace-go/contrib/net/http/otelc/v2",
		"github.com/DataDog/dd-trace-go/contrib/rs/zerolog/otelc/v2",
		"github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/otelc/v2",
		slogOtelc,
		logrusOtelc,
	)

	// Every module is replaced with the local directory that fixmodules would
	// compute for otelc/all.
	baseWant := []Replace{
		rep(rootModule, "../.."),
		rep("github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/otelc/v2", "../../contrib/gin-gonic/gin/otelc"),
		rep("github.com/DataDog/dd-trace-go/contrib/k8s.io/client-go/otelc/v2", "../../contrib/k8s.io/client-go/otelc"),
		rep("github.com/DataDog/dd-trace-go/contrib/labstack/echo.v4/otelc/v2", "../../contrib/labstack/echo.v4/otelc"),
		rep("github.com/DataDog/dd-trace-go/contrib/net/http/otelc/v2", "../../contrib/net/http/otelc"),
		rep("github.com/DataDog/dd-trace-go/contrib/rs/zerolog/otelc/v2", "../../contrib/rs/zerolog/otelc"),
		rep("github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/otelc/v2", "../../contrib/segmentio/kafka-go/otelc"),
	}
	// The base is the sorted replace block this change produces on main; each PR
	// then adds one line for its new module, in sorted position.
	sortedWant := slices.Clone(baseWant)
	slices.SortFunc(sortedWant, func(a, b Replace) int {
		return cmp.Compare(a.Old.Path, b.Old.Path)
	})
	var baseBuilder strings.Builder
	baseBuilder.WriteString("module " + otelcAll + "\n\ngo 1.26.0\n\nreplace (\n")
	for _, r := range sortedWant {
		baseBuilder.WriteString("\t" + r.Old.Path + " => " + r.New.Path + "\n")
	}
	baseBuilder.WriteString(")\n")
	base := []byte(baseBuilder.String())

	// One PR adds the slog rules module, another adds the logrus one.
	sync := func(extra Replace) []byte {
		t.Helper()
		want := append(slices.Clone(baseWant), extra)
		got, err := applyReplaceSync("go.mod", base, mods, want)
		if err != nil {
			t.Fatalf("applyReplaceSync: %v", err)
		}
		if _, statements := parseReplaces(t, string(got)); statements != 1 {
			t.Fatalf("expected one replace block, got %d statements:\n%s", statements, got)
		}
		return got
	}
	ours := sync(rep(slogOtelc, "../../contrib/log/slog/otelc"))
	theirs := sync(rep(logrusOtelc, "../../contrib/sirupsen/logrus/otelc"))

	dir := t.TempDir()
	oursPath := filepath.Join(dir, "ours")
	basePath := filepath.Join(dir, "base")
	theirsPath := filepath.Join(dir, "theirs")
	for path, content := range map[string][]byte{oursPath: ours, basePath: base, theirsPath: theirs} {
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command("git", "merge-file", "-p",
		"-L", "ours", "-L", "base", "-L", "theirs",
		oursPath, basePath, theirsPath)
	merged, err := cmd.Output()
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() > 0 {
			t.Fatalf("merge-file conflicted (exit %d):\n%s", exitErr.ExitCode(), merged)
		}
		t.Fatalf("git merge-file: %v", err)
	}
	if strings.Contains(string(merged), "<<<<<<<") {
		t.Errorf("merged file holds conflict markers:\n%s", merged)
	}

	replaces, _ := parseReplaces(t, string(merged))
	gotPaths := make([]string, 0, len(replaces))
	for _, r := range replaces {
		gotPaths = append(gotPaths, r.Old.Path)
	}
	if !slices.Contains(gotPaths, slogOtelc) || !slices.Contains(gotPaths, logrusOtelc) {
		t.Errorf("merged replace block misses one of the added modules: %v", gotPaths)
	}
	if !slices.IsSorted(gotPaths) {
		t.Errorf("merged replace block is not sorted: %v", gotPaths)
	}
}
