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
// internal/telemetry/log are genuinely internal packages
// (github.com/DataDog/dd-trace-go/v2/internal/...), so Go's compiler-enforced
// internal-package visibility rule makes it structurally impossible for any
// separate module to import them at all — none of the ~75 contrib/*
// integration modules, or any other module outside the
// github.com/DataDog/dd-trace-go/v2/... import path, can ever match that
// part of the analyzer. The only way a *separate* module can ever trigger a
// diagnostic is via the standard-library-log check, which is itself already
// scoped to specific path patterns (see formatverbs.go's stdLogAllowedFile:
// /scripts/, /tools/, /internal/orchestrion/). This tool filters go.work's
// module list to that same set of path patterns, rather than sweeping every
// workspace module — confirmed empirically that a full sweep both takes
// several minutes (each contrib module pulls in and type-checks its own
// real third-party SDK dependencies) and hits at least one unrelated,
// pre-existing build failure outside this scope
// (.github/workflows/apps has two files each declaring func main, which
// plain `go vet ./...`/`go build ./...` there already fails on independent
// of this analyzer) - full coverage isn't lost by filtering, since those
// modules could never match in the first place.
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

func isRelevantWorkspaceModule(relPath string) bool {
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
		if !isRelevantWorkspaceModule(use.Path) {
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
