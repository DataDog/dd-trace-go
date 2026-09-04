// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build ignore

// This tool runs the SDK logging safety analyzers
// (internal/telemetry/log/analyzer/cmd) against every workspace module that
// can plausibly trigger a diagnostic — a bare `go run .../cmd ./...` (as
// used by `make lint/errlog` for the root module) cannot cross Go workspace
// module boundaries, so packages that live in a separate module were never
// checked at all. Confirmed live before this fix:
// internal/orchestrion/_integration/internal/generator/main.go's
// log.Fatalf(..., "%v", err) produced no diagnostic from `make lint/errlog`
// despite being exactly the pattern logformatverbs exists to catch.
//
// Scope is deliberately not "every module in go.work": internal/log and
// internal/telemetry/log are internal packages, and Go's compiler-enforced
// internal-visibility rule — which is import-path-based, not module-based —
// limits who can import them to packages beneath the root module's path
// (github.com/DataDog/dd-trace-go/v2/...). None of the ~75 contrib/*
// integration modules qualify: their module paths are
// github.com/DataDog/dd-trace-go/contrib/.../v2, a different prefix entirely.
// But modules nested *beneath the root module path* do qualify — they are
// separate go.work modules, yet their declared module path still sits under
// github.com/DataDog/dd-trace-go/v2/, so they can (and
// internal/traceprof/traceproftest does) import internal/log despite being
// outside every /scripts/, /tools/, /orchestrion/ directory. Such modules
// are therefore always in scope. Everything else can only ever trigger a
// diagnostic via the standard-library-log check, which is itself already
// scoped to specific path patterns (see formatverbs.go's stdLogAllowedFile:
// /scripts/, /tools/, /internal/orchestrion/); go.work's remaining modules
// are filtered to that same set. A full sweep is still avoided deliberately:
// it takes several minutes (each contrib module pulls in and type-checks
// its own real third-party SDK dependencies) and hits at least one
// unrelated, pre-existing build failure outside this scope
// (.github/workflows/apps has two files each declaring func main, which
// plain `go vet ./...`/`go build ./...` there already fails on, independent
// of this analyzer).
//
// Usage: go run ./scripts/lint_errlog_workspaces.go
package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

// stdLogAllowedPathSegments mirrors the directory-level scope of
// formatverbs.go's stdLogAllowedFile (internal/log/log.go and
// instrumentation/testutils/sql/sql.go are single files inside modules
// already covered elsewhere, so they don't need their own workspace entry
// here).
var stdLogAllowedPathSegments = []string{"/scripts/", "/tools/", "/orchestrion/"}

// isRelevantWorkspaceModule reports whether the workspace module at relPath
// (e.g. "./internal/traceprof/traceproftest"), whose declared module path is
// modPath, can plausibly trigger a diagnostic.
func isRelevantWorkspaceModule(relPath, modPath, rootModPath string) bool {
	// Modules beneath the root module path can import the root's internal
	// packages — internal visibility is keyed on import path, not module
	// boundaries — so they are always in scope, regardless of directory name.
	if modPath != "" && (modPath == rootModPath || strings.HasPrefix(modPath, rootModPath+"/")) {
		return true
	}
	// Anything else can only match via the standard-library-log check's
	// directory-level scoping.
	p := "/" + filepath.ToSlash(strings.TrimPrefix(relPath, "./")) + "/"
	for _, segment := range stdLogAllowedPathSegments {
		if strings.Contains(p, segment) {
			return true
		}
	}
	return false
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	workData, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		log.Fatal(err)
	}
	workFile, err := modfile.ParseWork("go.work", workData, nil)
	if err != nil {
		log.Fatalf("parsing go.work: %v", err)
	}

	rootModData, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		log.Fatal(err)
	}
	rootModFile, err := modfile.Parse("go.mod", rootModData, nil)
	if err != nil {
		log.Fatalf("parsing root go.mod: %v", err)
	}
	rootModPath := rootModFile.Module.Mod.Path

	// Build the analyzer once and reuse the binary via `go vet -vettool=`,
	// rather than `go run`-ing the analyzer's own source once per module —
	// this is also the tool's own documented usage (see
	// internal/telemetry/log/analyzer/cmd/main.go).
	binary := filepath.Join(os.TempDir(), "dd-trace-go-errlog-vet")
	build := exec.Command("go", "build", "-o", binary, "./internal/telemetry/log/analyzer/cmd")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		log.Fatalf("building the analyzer: %v", err)
	}

	var failed bool
	for _, use := range workFile.Use {
		if use.Path == "." {
			continue // the root module is checked by lint/errlog's own ./... pass
		}

		// A missing or unparsable go.mod for a workspace module is a real
		// problem, not something to silently narrow the sweep around.
		modData, err := os.ReadFile(filepath.Join(root, use.Path, "go.mod"))
		if err != nil {
			log.Fatalf("reading %s/go.mod: %v", use.Path, err)
		}
		mod, err := modfile.Parse("go.mod", modData, nil)
		if err != nil {
			log.Fatalf("parsing %s/go.mod: %v", use.Path, err)
		}
		modPath := ""
		if mod.Module != nil {
			modPath = mod.Module.Mod.Path
		}

		if !isRelevantWorkspaceModule(use.Path, modPath, rootModPath) {
			continue
		}
		dir := filepath.Join(root, use.Path)

		// `go vet ./...` exits non-zero when a module's root has no
		// packages of its own (e.g. its code all lives in a nested,
		// separately-mod'd subdirectory) — that's not a lint failure, just
		// nothing to check here, so skip vet entirely rather than let that
		// exit code masquerade as a real diagnostic below. But a `go list`
		// failure (a missing dependency, a malformed package) is a real
		// problem with the module, not an empty one — treating it the same
		// as "nothing to check" would let a broken module in this tool's own
		// scope pass silently instead of failing the sweep.
		list := exec.Command("go", "list", "./...")
		list.Dir = dir
		list.Stderr = os.Stderr
		out, err := list.Output()
		if err != nil {
			fmt.Printf("== %s: go list failed ==\n", use.Path)
			failed = true
			continue
		}
		if len(strings.TrimSpace(string(out))) == 0 {
			continue
		}

		fmt.Printf("== %s ==\n", use.Path)
		vet := exec.Command("go", "vet", "-vettool="+binary, "./...")
		vet.Dir = dir
		vet.Stdout = os.Stdout
		vet.Stderr = os.Stderr
		if err := vet.Run(); err != nil {
			failed = true
		}
	}

	if failed {
		os.Exit(1)
	}
}
