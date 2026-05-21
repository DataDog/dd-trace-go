# Config Migration Audit & Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a runnable audit tool that reports which `DD_*` configs have been migrated to `internal/config` and which have not, wire it into CI, then migrate the remaining configs using the established pattern.

**Architecture:** A new Go program at `scripts/configaudit/` uses `golang.org/x/tools/go/packages` to load the module, resolve constants through `go/types`, and classify every `DD_*` env var into three buckets: `migrated` (read via `provider` in `internal/config.loadConfig`), `unmigrated` (still read outside `internal/config`), and `untracked` (read in code but missing from `internal/env/supported_configurations.json`). Output is JSON + a human-readable table; a `make config-audit` target wraps it, and a non-blocking GitHub Actions job posts the report. Then individual configs are migrated by following the project's documented procedure in [internal/config/README.md](../../../internal/config/README.md), using `serviceName` (PR #4559) and `logStartup` (PR #4214) as canonical references.

**Tech Stack:** Go (1.25+, matches root `go.mod`), `golang.org/x/tools/go/packages`, `go/ast`, `go/types`, `go/constant`, GitHub Actions, existing `make` infrastructure.

> **Build-environment note:** `scripts/configaudit/` lives outside the root `go.work` (same as `scripts/configinverter/`). All `go build` / `go test` / `go run` commands targeting `scripts/configaudit/...` must be run with `GOWORK=off` set, or executed from inside the `scripts/configaudit/` directory with `GOWORK=off`. Every command in this plan that targets the configaudit module shows the `GOWORK=off` prefix.

---

## Repository orientation (read before starting any task)

Before touching code, the executing engineer **must** read these files:

- [AGENTS.md](../../../AGENTS.md) — repo entry point for AI/agent contributors
- [CONTRIBUTING.md](../../../CONTRIBUTING.md) — Go style, commit format, test conventions
- [README.md](../../../README.md) — high-level project orientation and `make` targets
- [internal/AGENTS.md](../../../internal/AGENTS.md) — rules for `internal/` changes (Phase 4 migrations touch this)
- [ddtrace/tracer/AGENTS.md](../../../ddtrace/tracer/AGENTS.md) — rules for tracer changes (Phase 4 migrations touch this)
- [internal/config/README.md](../../../internal/config/README.md) — the migration playbook, hot-path rules, and cross-product gate semantics

Conventional Commits are required. Migration commits in this repo are formatted `refactor(config): migrate <fieldName>` (see git log: `e8a9aad08`, `4e7653059`, `f8c44c758`, `0480a77e3`).

---

## File structure

### New files created

| Path | Responsibility |
|---|---|
| `scripts/configaudit/main.go` | CLI entrypoint, flag parsing, JSON/table output |
| `scripts/configaudit/known.go` | Parse `internal/env/supported_configurations.json` → set of known DD_* vars |
| `scripts/configaudit/migrated.go` | AST walk of `internal/config/config.go::loadConfig` → set of migrated DD_* vars |
| `scripts/configaudit/scan.go` | `packages.Load` walk of the rest of the module → map[envVar]→[]callSite, with constant resolution via `go/types` |
| `scripts/configaudit/classify.go` | Combine the three sets into `AuditResult{Migrated, Unmigrated, Untracked}` |
| `scripts/configaudit/known_test.go` | Unit tests for the JSON parser (table-driven) |
| `scripts/configaudit/migrated_test.go` | Unit tests for the `loadConfig` AST walker (synthetic source) |
| `scripts/configaudit/scan_test.go` | Unit tests for the codebase scanner (table-driven on small package fixtures under `testdata/`) |
| `scripts/configaudit/classify_test.go` | Unit tests for the set-arithmetic classifier |
| `scripts/configaudit/README.md` | How to run, output format, CI behavior |
| `scripts/configaudit/testdata/<...>` | Tiny package fixtures the scanner is run against in tests |
| `docs/superpowers/inventory/2026-05-21-config-migration-inventory.md` | Snapshot of the audit output at the moment Phase 3 runs (committed as the migration backlog) |
| `.github/workflows/config-audit.yml` | Non-blocking workflow that runs the audit on PRs and uploads the report as an artifact |

### Modified files

| Path | What changes |
|---|---|
| `Makefile` | Add a `config-audit` target |
| `internal/config/config.go` | Phase 4: add fields, getters, setters, `loadConfig` lines per migrated config |
| `internal/config/config_test.go` | Phase 4: tests for new getters/setters |
| Various call-site files outside `internal/config` | Phase 4: replace `env.Get("DD_*")` / `internal.BoolEnv("DD_*", …)` / etc. with `internal/config.Get().XxxGetter()` |

### Files **not** touched

- `internal/env/*` — the env package is the verified gateway; we read its data but do not alter how reads happen.
- `scripts/configinverter/*` — that tool already keeps `supported_configurations.gen.go` in sync; we coexist with it, not replace it.

---

## Phase 1: Build the audit tool (Tasks 1–6)

### Task 1: Bootstrap the `scripts/configaudit` package and verify it builds

**Files:**
- Create: `scripts/configaudit/main.go`
- Create: `scripts/configaudit/go.mod` (only if needed — confirm in Step 1 below)

- [ ] **Step 1: Verify whether `scripts/` uses its own module**

Run: `cat scripts/configinverter/go.mod 2>/dev/null || echo "no separate go.mod"`

Expected: prints the contents of `scripts/configinverter/go.mod` (it exists today). The new `configaudit` directory will need its own `go.mod` mirroring that pattern.

- [ ] **Step 2: Create `scripts/configaudit/go.mod`**

```
module github.com/DataDog/dd-trace-go/v2/scripts/configaudit

go 1.25.0

require (
	github.com/DataDog/dd-trace-go/v2 v2.0.0
	golang.org/x/tools v0.27.0
)

replace github.com/DataDog/dd-trace-go/v2 => ../..
```

The `require` block is provisional — `go mod tidy` will prune `github.com/DataDog/dd-trace-go/v2` and `golang.org/x/tools` until the code actually imports them (Task 3 brings them in). Commit whatever `go mod tidy` produces; do not hand-edit `go.sum`.

- [ ] **Step 3: Create a minimal `main.go` that compiles**

File: `scripts/configaudit/main.go`

```go
// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Command configaudit reports which DD_* environment-variable configurations
// have been migrated to internal/config and which have not.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	var (
		root   = flag.String("root", ".", "repository root")
		format = flag.String("format", "table", "output format: table or json")
	)
	flag.Parse()

	if err := run(*root, *format, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "configaudit:", err)
		os.Exit(1)
	}
}

func run(root, format string, out *os.File) error {
	_ = root
	_ = format
	_ = out
	return nil
}
```

- [ ] **Step 4: Confirm it builds**

Run: `GOWORK=off go build ./scripts/configaudit/...` (from repo root) or `cd scripts/configaudit && GOWORK=off go build ./...`
Expected: exits 0, no output.

- [ ] **Step 5: Commit**

```bash
git add scripts/configaudit/main.go scripts/configaudit/go.mod scripts/configaudit/go.sum
git commit -m "chore(scripts): scaffold configaudit tool"
```

---

### Task 2: Parse `supported_configurations.json` into the "known" set

**Files:**
- Create: `scripts/configaudit/known.go`
- Test: `scripts/configaudit/known_test.go`
- Reference: `internal/env/supported_configurations.json` (236 DD_* keys at time of writing)

- [ ] **Step 1: Write the failing test**

File: `scripts/configaudit/known_test.go`

```go
package main

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestLoadKnown(t *testing.T) {
	// Use the real supported_configurations.json so the test stays honest.
	path := filepath.Join("..", "..", "internal", "env", "supported_configurations.json")
	got, err := loadKnown(path)
	if err != nil {
		t.Fatalf("loadKnown: %v", err)
	}
	if len(got) < 100 {
		t.Fatalf("expected at least 100 known DD_* configs, got %d", len(got))
	}
	// Spot-check a few known entries.
	for _, key := range []string{"DD_SERVICE", "DD_AGENT_HOST", "DD_TRACE_STARTUP_LOGS"} {
		if _, ok := got[key]; !ok {
			t.Errorf("expected %s in known set", key)
		}
	}
}

func TestLoadKnown_AliasIncluded(t *testing.T) {
	// DD_API_KEY has alias DD-API-KEY in the JSON; aliases should appear too.
	path := filepath.Join("..", "..", "internal", "env", "supported_configurations.json")
	got, err := loadKnown(path)
	if err != nil {
		t.Fatalf("loadKnown: %v", err)
	}
	if _, ok := got["DD-API-KEY"]; !ok {
		// list a few keys to aid debugging if this fails
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("expected DD-API-KEY alias in known set; sample: %v", keys[:5])
	}
}

func TestLoadKnown_MissingFile(t *testing.T) {
	_, err := loadKnown("does-not-exist.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadKnown_ShapeRoundTrip(t *testing.T) {
	// Verify the parser doesn't silently drop the impl-letter shape.
	want := []string{"DD_FOO", "DD-FOO-ALIAS"}
	tmp := t.TempDir()
	p := filepath.Join(tmp, "sc.json")
	if err := writeFile(p, []byte(`{"version":"2","supportedConfigurations":{"DD_FOO":[{"implementation":"A","type":"string","default":null,"aliases":["DD-FOO-ALIAS"]}]}}`)); err != nil {
		t.Fatal(err)
	}
	got, err := loadKnown(p)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sort.Strings(want)
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("got %v, want %v", keys, want)
	}
}
```

Note: `writeFile` is a tiny test helper. Add it inline at the bottom of `known_test.go`:

```go
import "os"

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
```

(Merge the imports — do not duplicate the `import` block.)

- [ ] **Step 2: Run the tests to confirm they fail**

Run: `cd scripts/configaudit && GOWORK=off go test ./...`
Expected: FAIL with `undefined: loadKnown`.

- [ ] **Step 3: Implement `loadKnown`**

File: `scripts/configaudit/known.go`

```go
// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type supportedConfigEntry struct {
	Implementation string   `json:"implementation"`
	Type           string   `json:"type"`
	Default        *string  `json:"default"`
	Aliases        []string `json:"aliases,omitempty"`
}

type supportedConfigsFile struct {
	Version                 string                            `json:"version"`
	SupportedConfigurations map[string][]supportedConfigEntry `json:"supportedConfigurations"`
}

// loadKnown returns the set of every DD_* env var (and its aliases) declared
// in internal/env/supported_configurations.json. The returned map is a set
// (value is struct{}).
func loadKnown(path string) (map[string]struct{}, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var f supportedConfigsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	out := make(map[string]struct{}, len(f.SupportedConfigurations))
	for key, entries := range f.SupportedConfigurations {
		out[key] = struct{}{}
		for _, e := range entries {
			for _, alias := range e.Aliases {
				out[alias] = struct{}{}
			}
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd scripts/configaudit && GOWORK=off go test ./...`
Expected: PASS, all four tests green.

- [ ] **Step 5: Commit**

```bash
git add scripts/configaudit/known.go scripts/configaudit/known_test.go
git commit -m "chore(scripts): parse supported_configurations.json in configaudit"
```

---

### Task 3: Discover the "migrated" set from `internal/config/config.go::loadConfig`

**Files:**
- Create: `scripts/configaudit/migrated.go`
- Test: `scripts/configaudit/migrated_test.go`
- Reference: `internal/config/config.go` `loadConfig()` function (currently lines 185–293 at `42975b42c`)

The strategy is **AST-only** for this file (no full type loading). `loadConfig` calls `p.GetString("DD_*", …)`, `p.GetBool(...)`, `p.GetInt(...)`, etc., where `p` is the local `*provider.Provider` returned by `provider.New()`. We walk every `*ast.CallExpr` inside the body of the func declaration `loadConfig`, match selector patterns `p.Get*`, and extract the first argument when it is a `*ast.BasicLit` of kind STRING, **or** a `*ast.SelectorExpr` resolved via the file's import set when it is a package constant. To stay simple, the AST walker resolves only constants defined in the same package (via that package's other `*ast.File`s).

- [ ] **Step 1: Write the failing test**

File: `scripts/configaudit/migrated_test.go`

```go
package main

import (
	"path/filepath"
	"testing"
)

func TestLoadMigrated_RealRepo(t *testing.T) {
	pkgDir := filepath.Join("..", "..", "internal", "config")
	got, err := loadMigrated(pkgDir)
	if err != nil {
		t.Fatalf("loadMigrated: %v", err)
	}
	// These are migrated as of the plan date.
	for _, key := range []string{
		"DD_SERVICE",
		"DD_TRACE_STARTUP_LOGS",
		"DD_TRACE_AGENT_URL",
		"DD_AGENT_HOST",
		"DD_RUNTIME_METRICS_ENABLED",
		"DD_TRACE_RATE_LIMIT",
		"DD_API_KEY",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("expected %s in migrated set", key)
		}
	}
	// DD_SITE has *not* been migrated yet.
	if _, ok := got["DD_SITE"]; ok {
		t.Errorf("did not expect DD_SITE to be in migrated set yet")
	}
}

func TestLoadMigrated_ResolvesPackageConstants(t *testing.T) {
	pkgDir := filepath.Join("..", "..", "internal", "config")
	got, err := loadMigrated(pkgDir)
	if err != nil {
		t.Fatal(err)
	}
	// CIVisibilityEnabledEnvironmentVariable is a constant from internal/civisibility/constants.
	// We require the walker to resolve at least one such cross-package constant.
	if _, ok := got["DD_CIVISIBILITY_ENABLED"]; !ok {
		t.Errorf("expected DD_CIVISIBILITY_ENABLED (resolved from constant) in migrated set")
	}
}
```

- [ ] **Step 2: Run the test, confirm it fails**

Run: `cd scripts/configaudit && GOWORK=off go test -run TestLoadMigrated ./...`
Expected: FAIL with `undefined: loadMigrated`.

- [ ] **Step 3: Implement `loadMigrated` using `go/packages` + `go/types`**

The cross-package constant requirement in Step 1's second test forces using `packages.Load` with type info — pure AST is not enough.

File: `scripts/configaudit/migrated.go`

```go
// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"

	"golang.org/x/tools/go/packages"
)

// providerGetterPrefixes are the method names on *provider.Provider that read
// a DD_* config value. Keep in sync with internal/config/provider/provider.go.
var providerGetterPrefixes = []string{
	"GetString", "GetStringWithValidator",
	"GetBool",
	"GetInt", "GetIntWithValidator",
	"GetFloat", "GetFloatWithValidator", "GetFloatWithValidatorOrigin",
	"GetDuration",
}

func isProviderGetter(name string) bool {
	for _, p := range providerGetterPrefixes {
		if name == p {
			return true
		}
	}
	return false
}

// loadMigrated walks the loadConfig function inside the package at pkgDir and
// returns the set of DD_* keys passed as the first argument to any provider
// getter call.
func loadMigrated(pkgDir string) (map[string]struct{}, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports |
			packages.NeedDeps,
		Dir: pkgDir,
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", pkgDir, err)
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no packages loaded from %s", pkgDir)
	}
	if errs := packageErrors(pkgs); len(errs) > 0 {
		return nil, fmt.Errorf("type errors in %s: %v", pkgDir, errs)
	}

	out := make(map[string]struct{})
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				fn, ok := n.(*ast.FuncDecl)
				if !ok || fn.Name.Name != "loadConfig" {
					return true
				}
				ast.Inspect(fn.Body, func(inner ast.Node) bool {
					call, ok := inner.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || !isProviderGetter(sel.Sel.Name) {
						return true
					}
					if len(call.Args) == 0 {
						return true
					}
					if key, ok := resolveStringArg(pkg.TypesInfo, call.Args[0]); ok {
						out[key] = struct{}{}
					}
					return true
				})
				return false
			})
		}
	}
	return out, nil
}

// resolveStringArg returns the string value of expr if it is a constant string
// (literal or named constant), and the second return is true on success.
func resolveStringArg(info *types.Info, expr ast.Expr) (string, bool) {
	tv, ok := info.Types[expr]
	if !ok || tv.Value == nil {
		return "", false
	}
	if tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

func packageErrors(pkgs []*packages.Package) []error {
	var errs []error
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			errs = append(errs, e)
		}
	}
	if len(errs) > 5 {
		errs = errs[:5]
	}
	return errs
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd scripts/configaudit && GOWORK=off go test -run TestLoadMigrated ./...`
Expected: PASS, both tests green.

- [ ] **Step 5: Commit**

```bash
git add scripts/configaudit/migrated.go scripts/configaudit/migrated_test.go scripts/configaudit/go.sum
git commit -m "chore(scripts): detect migrated configs from loadConfig AST"
```

---

### Task 4: Scan the rest of the codebase for un-migrated DD_* reads

**Files:**
- Create: `scripts/configaudit/scan.go`
- Test: `scripts/configaudit/scan_test.go`
- Create: `scripts/configaudit/testdata/fixture_a/fixture.go`
- Create: `scripts/configaudit/testdata/fixture_a/go.mod`

The scanner walks every Go package in the module **except** an exclude list, and reports each call to a known env-reading function plus its resolved DD_* key.

The exclude list is:
- `github.com/DataDog/dd-trace-go/v2/internal/config/...` (the destination)
- `github.com/DataDog/dd-trace-go/v2/internal/env/...` (the gateway)
- `github.com/DataDog/dd-trace-go/v2/scripts/...` (tooling)
- any `_test.go` file (counted separately, not in main output)

The env-reading functions we recognize (sourced from `internal/env/env.go`, `internal/internal.go` / `internal/env.go`, and `internal/stableconfig`):

| Package | Function | Key arg index |
|---|---|---|
| `internal/env` | `Get`, `Lookup` | 0 |
| `internal` | `BoolEnv`, `BoolEnvNoDefault`, `IntEnv`, `FloatEnv`, `DurationEnv`, `DurationEnvWithUnit` | 0 |
| `internal/stableconfig` | `Bool`, `String`, `Int`, `Float` | 0 |

- [ ] **Step 1: Create the test fixture**

File: `scripts/configaudit/testdata/fixture_a/go.mod`

```
module example.com/fixturea

go 1.24
```

File: `scripts/configaudit/testdata/fixture_a/fixture.go`

```go
package fixturea

// Simulate the env-reading helpers in the real repo without depending on it.

func envGet(key string) string                 { return "" }
func boolEnv(key string, def bool) bool        { return def }
func intEnv(key string, def int) int           { return def }

const ddSiteKey = "DD_SITE"

func ReadAll() {
	_ = envGet("DD_HOSTNAME")
	_ = envGet(ddSiteKey)
	_ = boolEnv("DD_PROFILING_ENABLED", false)
	_ = intEnv("DD_TRACE_AGENT_PORT", 8126)
}
```

The fixture covers a literal string, a named constant, and three different helper functions. The scanner under test must recognize all four call sites.

- [ ] **Step 2: Write the failing test**

File: `scripts/configaudit/scan_test.go`

```go
package main

import (
	"path/filepath"
	"sort"
	"testing"
)

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
}

func TestScan_RealRepoTracerHasUnmigratedReads(t *testing.T) {
	// Smoke test: a top-level run over the real tracer should find DD_SITE
	// (used inside ddtrace/tracer/option.go).
	root := filepath.Join("..", "..")
	got, err := scan(root, defaultRecognizers(), defaultExcludes(root))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	sites := got["DD_SITE"]
	if len(sites) == 0 {
		t.Fatalf("expected DD_SITE call sites in real repo, got none")
	}
}
```

- [ ] **Step 3: Run the test, confirm it fails**

Run: `cd scripts/configaudit && GOWORK=off go test -run TestScan ./...`
Expected: FAIL with `undefined: scan` (and friends).

- [ ] **Step 4: Implement the scanner**

File: `scripts/configaudit/scan.go`

```go
// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

// CallSite records where a DD_* env var was read.
type CallSite struct {
	File string
	Line int
	Func string // fully-qualified or fixture-local function name
}

// recognizers describes how to identify env-reading function calls.
//
//   - ByPath: map[importPath]map[funcName]bool — used in the real codebase
//   - ByName: map[funcName]bool — used in unit-test fixtures where the helpers
//     have no stable import path
type recognizers struct {
	ByPath map[string]map[string]bool
	ByName map[string]bool
}

func defaultRecognizers() recognizers {
	return recognizers{
		ByPath: map[string]map[string]bool{
			"github.com/DataDog/dd-trace-go/v2/internal/env": {
				"Get":    true,
				"Lookup": true,
			},
			"github.com/DataDog/dd-trace-go/v2/internal": {
				"BoolEnv":             true,
				"BoolEnvNoDefault":    true,
				"IntEnv":              true,
				"FloatEnv":            true,
				"DurationEnv":         true,
				"DurationEnvWithUnit": true,
			},
			"github.com/DataDog/dd-trace-go/v2/internal/stableconfig": {
				"Bool":   true,
				"String": true,
				"Int":    true,
				"Float":  true,
			},
		},
	}
}

func defaultExcludes(root string) []string {
	// Patterns are matched as a substring of the file path returned by go/packages.
	return []string{
		"/internal/config/",
		"/internal/env/",
		"/scripts/",
		"_test.go",
	}
}

func excluded(path string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(path, p) {
			return true
		}
	}
	return false
}

// scan walks every package under root and returns the DD_* key -> call sites map.
func scan(root string, r recognizers, exclude []string) (map[string][]CallSite, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports |
			packages.NeedDeps | packages.NeedCompiledGoFiles,
		Dir:   root,
		Tests: false,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("packages.Load: %w", err)
	}
	out := make(map[string][]CallSite)
	for _, pkg := range pkgs {
		for i, file := range pkg.Syntax {
			filename := pkg.CompiledGoFiles[i]
			if excluded(filename, exclude) {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				funcID, recognized := callIdentity(pkg, call, r)
				if !recognized {
					return true
				}
				key, ok := resolveStringArg(pkg.TypesInfo, call.Args[0])
				if !ok {
					return true
				}
				if !strings.HasPrefix(key, "DD_") && !strings.HasPrefix(key, "DD-") {
					return true
				}
				pos := pkg.Fset.Position(call.Pos())
				out[key] = append(out[key], CallSite{
					File: pos.Filename,
					Line: pos.Line,
					Func: funcID,
				})
				return true
			})
		}
	}
	return out, nil
}

// callIdentity decides whether the call matches one of our recognizers, and
// returns a printable function identity ("pkg.Func" or just "Func" for fixtures).
func callIdentity(pkg *packages.Package, call *ast.CallExpr, r recognizers) (string, bool) {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		// Same-package call. Try ByName recognizer first.
		if r.ByName != nil && r.ByName[fn.Name] {
			return fn.Name, true
		}
		// Try ByPath using this package's path.
		if names, ok := r.ByPath[pkg.PkgPath]; ok && names[fn.Name] {
			return pkg.PkgPath + "." + fn.Name, true
		}
		return "", false
	case *ast.SelectorExpr:
		// pkgname.Func form. Resolve the imported package.
		obj, ok := pkg.TypesInfo.Uses[fn.Sel]
		if !ok {
			return "", false
		}
		// Only function calls (not method calls) are env helpers.
		fnObj, ok := obj.(*types.Func)
		if !ok {
			return "", false
		}
		impPkg := fnObj.Pkg()
		if impPkg == nil {
			return "", false
		}
		path := impPkg.Path()
		if names, ok := r.ByPath[path]; ok && names[fnObj.Name()] {
			return path + "." + fnObj.Name(), true
		}
		if r.ByName != nil && r.ByName[fnObj.Name()] {
			return fnObj.Name(), true
		}
		return "", false
	}
	return "", false
}

```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd scripts/configaudit && GOWORK=off go test -run TestScan ./... -timeout 5m`

Expected: PASS, both tests green. The real-repo test takes 30-90s the first time because `packages.Load("./...")` builds the whole module.

- [ ] **Step 6: Commit**

```bash
git add scripts/configaudit/scan.go scripts/configaudit/scan_test.go scripts/configaudit/testdata
git commit -m "chore(scripts): scan DD_* env reads outside internal/config"
```

---

### Task 5: Classify into migrated / unmigrated / untracked, and render output

**Files:**
- Create: `scripts/configaudit/classify.go`
- Test: `scripts/configaudit/classify_test.go`
- Modify: `scripts/configaudit/main.go`

- [ ] **Step 1: Write the failing test**

File: `scripts/configaudit/classify_test.go`

```go
package main

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	known := map[string]struct{}{
		"DD_AGENT_HOST": {},
		"DD_SERVICE":    {},
		"DD_SITE":       {},
	}
	migrated := map[string]struct{}{
		"DD_AGENT_HOST": {},
		"DD_SERVICE":    {},
	}
	reads := map[string][]CallSite{
		"DD_SITE":     {{File: "x.go", Line: 1, Func: "env.Get"}},
		"DD_AGENT_HOST": {{File: "y.go", Line: 2, Func: "env.Get"}}, // also still read outside (legacy)
		"DD_UNKNOWN":  {{File: "z.go", Line: 3, Func: "env.Get"}},
	}
	res := classify(known, migrated, reads)

	migratedKeys := keySet(res.Migrated)
	unmigratedKeys := keySet(res.Unmigrated)
	untrackedKeys := keySet(res.Untracked)
	stillReadKeys := keySet(res.MigratedButStillReadOutside)

	wantEq(t, "migrated", migratedKeys, []string{"DD_AGENT_HOST", "DD_SERVICE"})
	wantEq(t, "unmigrated", unmigratedKeys, []string{"DD_SITE"})
	wantEq(t, "untracked", untrackedKeys, []string{"DD_UNKNOWN"})
	wantEq(t, "stillReadOutside", stillReadKeys, []string{"DD_AGENT_HOST"})
}

func TestRenderTable(t *testing.T) {
	res := AuditResult{
		Unmigrated: []ConfigEntry{
			{Name: "DD_SITE", CallSites: []CallSite{{File: "a.go", Line: 1}}},
		},
	}
	var buf bytes.Buffer
	if err := renderTable(&buf, res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "DD_SITE") {
		t.Fatalf("expected DD_SITE in table output, got %q", buf.String())
	}
}

func TestRenderJSON(t *testing.T) {
	res := AuditResult{
		Unmigrated: []ConfigEntry{{Name: "DD_SITE"}},
	}
	var buf bytes.Buffer
	if err := renderJSON(&buf, res); err != nil {
		t.Fatal(err)
	}
	var got AuditResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Unmigrated) != 1 || got.Unmigrated[0].Name != "DD_SITE" {
		t.Fatalf("round-trip failed: %+v", got)
	}
}

func keySet(entries []ConfigEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	sort.Strings(out)
	return out
}

func wantEq(t *testing.T, label string, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s: got %v, want %v", label, got, want)
	}
}
```

- [ ] **Step 2: Run the test to confirm it fails**

Run: `cd scripts/configaudit && GOWORK=off go test -run "TestClassify|TestRender" ./...`
Expected: FAIL with `undefined: classify, AuditResult, ConfigEntry, renderTable, renderJSON`.

- [ ] **Step 3: Implement the classifier and renderers**

File: `scripts/configaudit/classify.go`

```go
// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
)

// ConfigEntry is a DD_* env var grouped with the places it is read.
type ConfigEntry struct {
	Name      string     `json:"name"`
	CallSites []CallSite `json:"call_sites,omitempty"`
}

// AuditResult is the structured output of one audit run.
type AuditResult struct {
	Migrated                    []ConfigEntry `json:"migrated"`
	Unmigrated                  []ConfigEntry `json:"unmigrated"`
	Untracked                   []ConfigEntry `json:"untracked"`
	MigratedButStillReadOutside []ConfigEntry `json:"migrated_but_still_read_outside"`
}

func classify(known, migrated map[string]struct{}, reads map[string][]CallSite) AuditResult {
	var res AuditResult
	// All known keys: emit either as migrated or as "known but not migrated and not read" (skipped).
	// We only emit migrated entries that actually have a corresponding migrated marker.
	for key := range migrated {
		res.Migrated = append(res.Migrated, ConfigEntry{Name: key})
		if sites, ok := reads[key]; ok {
			res.MigratedButStillReadOutside = append(res.MigratedButStillReadOutside, ConfigEntry{Name: key, CallSites: sites})
		}
	}
	for key, sites := range reads {
		if _, isMigrated := migrated[key]; isMigrated {
			continue
		}
		entry := ConfigEntry{Name: key, CallSites: sites}
		if _, isKnown := known[key]; isKnown {
			res.Unmigrated = append(res.Unmigrated, entry)
		} else {
			res.Untracked = append(res.Untracked, entry)
		}
	}
	sortEntries(res.Migrated)
	sortEntries(res.Unmigrated)
	sortEntries(res.Untracked)
	sortEntries(res.MigratedButStillReadOutside)
	return res
}

func sortEntries(es []ConfigEntry) {
	sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })
}

func renderJSON(w io.Writer, res AuditResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

func renderTable(w io.Writer, res AuditResult) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "STATUS\tCONFIG\tCALL_SITES\n")
	for _, e := range res.Unmigrated {
		fmt.Fprintf(tw, "UNMIGRATED\t%s\t%d\n", e.Name, len(e.CallSites))
	}
	for _, e := range res.MigratedButStillReadOutside {
		fmt.Fprintf(tw, "STILL_READ\t%s\t%d\n", e.Name, len(e.CallSites))
	}
	for _, e := range res.Untracked {
		fmt.Fprintf(tw, "UNTRACKED\t%s\t%d\n", e.Name, len(e.CallSites))
	}
	fmt.Fprintf(tw, "---\n")
	fmt.Fprintf(tw, "SUMMARY\tmigrated=%d\tunmigrated=%d\tuntracked=%d\tstill_read=%d\n",
		len(res.Migrated), len(res.Unmigrated), len(res.Untracked), len(res.MigratedButStillReadOutside))
	return tw.Flush()
}
```

- [ ] **Step 4: Wire `run()` in `main.go` to use everything**

File: `scripts/configaudit/main.go` (replace the stub `run` function)

```go
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	var (
		root   = flag.String("root", ".", "repository root")
		format = flag.String("format", "table", "output format: table or json")
	)
	flag.Parse()

	if err := run(*root, *format, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "configaudit:", err)
		os.Exit(1)
	}
}

func run(root, format string, out io.Writer) error {
	known, err := loadKnown(filepath.Join(root, "internal", "env", "supported_configurations.json"))
	if err != nil {
		return err
	}
	migrated, err := loadMigrated(filepath.Join(root, "internal", "config"))
	if err != nil {
		return err
	}
	reads, err := scan(root, defaultRecognizers(), defaultExcludes(root))
	if err != nil {
		return err
	}
	res := classify(known, migrated, reads)
	switch format {
	case "json":
		return renderJSON(out, res)
	case "table":
		return renderTable(out, res)
	default:
		return fmt.Errorf("unknown format %q", format)
	}
}
```

(Replace the existing `main.go` contents entirely. `os.Stdout` is `*os.File` which already satisfies `io.Writer`, so the call site at `main()` continues to compile.)

- [ ] **Step 5: Run the full test suite**

Run: `cd scripts/configaudit && GOWORK=off go test ./... -timeout 5m`
Expected: PASS, all tests green.

- [ ] **Step 6: Smoke-test the binary end-to-end**

Run: `cd scripts/configaudit && GOWORK=off go run . -root ../.. -format table | head -30`
Expected: shows `STATUS / CONFIG / CALL_SITES` table with `UNMIGRATED` rows like `DD_SITE`, `DD_APP_KEY`, etc., ending in a `SUMMARY` line.

- [ ] **Step 7: Commit**

```bash
git add scripts/configaudit/classify.go scripts/configaudit/classify_test.go scripts/configaudit/main.go
git commit -m "chore(scripts): classify and render configaudit results"
```

---

### Task 6: Document the tool and add a make target

**Files:**
- Create: `scripts/configaudit/README.md`
- Modify: `Makefile`

- [ ] **Step 1: Confirm the existing `Makefile` shape**

Run: `head -40 Makefile`
Expected: existing targets (`lint`, `test`, etc.) — use them as the formatting template.

- [ ] **Step 2: Write the README**

File: `scripts/configaudit/README.md`

```markdown
# Config Audit

`configaudit` reports which `DD_*` environment-variable configurations have been
migrated to `internal/config` and which have not. It is the inventory tool
backing the migration tracked in [internal/config/README.md](../../internal/config/README.md).

## Output categories

| Status | Meaning |
|---|---|
| `MIGRATED` | The variable is read inside `internal/config.loadConfig`. |
| `UNMIGRATED` | The variable is read outside `internal/config` and is **not** yet handled by `loadConfig`. These are the migration backlog. |
| `STILL_READ` | The variable is migrated, but at least one caller outside `internal/config` is still reading it directly. Migration is incomplete; legacy reads should be replaced with calls to the singleton. |
| `UNTRACKED` | The variable is read in code but missing from `internal/env/supported_configurations.json`. Likely a bug — add it to the JSON or remove the read. |

## Run

```sh
# Table output to stdout
make config-audit

# JSON for further processing
GOWORK=off go run ./scripts/configaudit -root . -format json > /tmp/audit.json
```

## CI

The `.github/workflows/config-audit.yml` workflow runs the audit on every PR and
uploads `audit.json` as an artifact. It does **not** fail the build — its job is
to give reviewers visibility into migration progress.
```

- [ ] **Step 3: Add the `config-audit` target**

Modify `Makefile`. Append at the end of the file:

```makefile
.PHONY: config-audit
config-audit: ## Report which DD_* configs are migrated to internal/config
	@GOWORK=off go run ./scripts/configaudit -root . -format table
```

- [ ] **Step 4: Verify the make target works**

Run: `make config-audit | tail -5`
Expected: prints the `SUMMARY` line — `migrated=N unmigrated=M untracked=K still_read=L`.

- [ ] **Step 5: Commit**

```bash
git add scripts/configaudit/README.md Makefile
git commit -m "docs(scripts): document configaudit and add make target"
```

---

## Phase 2: CI integration (Task 7)

### Task 7: Add a non-blocking GitHub Actions workflow

**Files:**
- Create: `.github/workflows/config-audit.yml`

- [ ] **Step 1: Verify existing workflows for the right setup-go action version**

Run: `grep -h "actions/setup-go" .github/workflows/*.yml | sort -u`
Expected: one or more lines like `uses: actions/setup-go@vN` — adopt the most recent pinned version used by neighboring workflows so we don't diverge.

- [ ] **Step 2: Create the workflow**

File: `.github/workflows/config-audit.yml`

```yaml
name: config-audit

on:
  pull_request:
    paths:
      - 'internal/config/**'
      - 'internal/env/supported_configurations.json'
      - 'ddtrace/**'
      - 'profiler/**'
      - 'scripts/configaudit/**'
      - '.github/workflows/config-audit.yml'
  workflow_dispatch:

permissions:
  contents: read

jobs:
  audit:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true
      - name: Run config audit
        run: |
          mkdir -p out
          GOWORK=off go run ./scripts/configaudit -root . -format json > out/audit.json
          GOWORK=off go run ./scripts/configaudit -root . -format table | tee out/audit.txt
      - name: Upload audit artifact
        uses: actions/upload-artifact@v4
        with:
          name: config-audit
          path: out/
          retention-days: 14
```

> Note: Replace `actions/setup-go@v5` with whatever version Step 1 surfaced if it differs. The workflow is `continue-on-error` by design — it is informational; it does not gate merges.

- [ ] **Step 3: Validate the YAML locally**

Run: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/config-audit.yml'))"`
Expected: no output (parsing succeeded).

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/config-audit.yml
git commit -m "ci: add non-blocking config-audit workflow"
```

---

## Phase 3: Snapshot the migration backlog (Task 8)

### Task 8: Run the audit and commit the resulting inventory

The committed snapshot serves three purposes: (a) a clear "what's left" document for reviewers, (b) a stable list to delegate work against, (c) a diff target — re-running the audit later should show shrinkage.

**Files:**
- Create: `docs/superpowers/inventory/2026-05-21-config-migration-inventory.md`

- [ ] **Step 1: Capture audit output**

Run from repo root:

```bash
GOWORK=off go run ./scripts/configaudit -root . -format table > /tmp/audit-table.txt
GOWORK=off go run ./scripts/configaudit -root . -format json   > /tmp/audit.json
wc -l /tmp/audit-table.txt /tmp/audit.json
```

Expected: both files non-empty, table includes a `SUMMARY` line.

- [ ] **Step 2: Author the inventory document**

File: `docs/superpowers/inventory/2026-05-21-config-migration-inventory.md`

Template (the executing engineer fills the placeholder lists with the actual contents of `/tmp/audit-table.txt`):

```markdown
# Config Migration Inventory — 2026-05-21

Generated by `make config-audit` (see [scripts/configaudit/README.md](../../../scripts/configaudit/README.md)).

## Summary

<!-- Paste the SUMMARY line from /tmp/audit-table.txt here. Example:
SUMMARY  migrated=27  unmigrated=14  untracked=2  still_read=3
-->

## Unmigrated

The configs below are still read from outside `internal/config`. Each becomes
one migration task in Phase 4. Order follows alphabetical for stability — there
is no implied priority.

<!-- Paste every "UNMIGRATED" row from /tmp/audit-table.txt as a markdown list:
- `DD_SITE` (1 call site)
- `DD_APP_KEY` (1 call site)
... -->

## Migrated but still read outside

These are bugs to fix as part of the migration cleanup. Each remaining read site
needs to be replaced with a call to the `internal/config` singleton.

<!-- Paste every "STILL_READ" row. -->

## Untracked

Likely bugs. Each entry is an env var read in code but missing from
`internal/env/supported_configurations.json`. Either add it to the JSON
(running `go run ./scripts/configinverter/main.go generate` after editing) or
remove the read.

<!-- Paste every "UNTRACKED" row. -->
```

- [ ] **Step 3: Verify the document is well-formed**

Run: `head -40 docs/superpowers/inventory/2026-05-21-config-migration-inventory.md`
Expected: shows the summary line and a non-empty Unmigrated section.

- [ ] **Step 4: Commit**

```bash
git add docs/superpowers/inventory/2026-05-21-config-migration-inventory.md
git commit -m "docs: snapshot config migration inventory (2026-05-21)"
```

---

## Phase 4: Migrate the remaining configs (Tasks 9, then Task 10 repeated)

This phase is iterative. Task 9 walks through migrating one real unmigrated config end-to-end (`DD_SITE`) so the engineer sees every step concretely. Task 10 then states the **migration template** — every other entry from the Phase 3 inventory uses the same template.

### Task 9: Migrate `DD_SITE` — the worked example

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `ddtrace/tracer/option.go` (line ~464)
- Modify: `ddtrace/tracer/civisibility_transport.go` (line ~98)
- Modify: `internal/civisibility/utils/net/client.go` (line ~154)
- Modify: `profiler/options.go` (line ~225)

> Confirm the line numbers in your local checkout — they were captured at commit `42975b42c` and may have drifted.

- [ ] **Step 1: Confirm current state of `DD_SITE` reads**

Run: `grep -rn '"DD_SITE"' --include="*.go" . | grep -v _test.go | grep -v internal/env`
Expected: four call sites in `ddtrace/tracer/option.go`, `ddtrace/tracer/civisibility_transport.go`, `internal/civisibility/utils/net/client.go`, `profiler/options.go`.

- [ ] **Step 2: Add a failing test for the new getter/setter**

Add this test to `internal/config/config_test.go`. (First inspect the file to find the right location alongside e.g. `TestSetServiceName`:)

```go
func TestSiteRoundTrip(t *testing.T) {
	t.Setenv("DD_SITE", "datadoghq.eu")
	resetGlobalState()
	if got := Get().Site(); got != "datadoghq.eu" {
		t.Errorf("Site() from env = %q, want %q", got, "datadoghq.eu")
	}
	Get().SetSite("us3.datadoghq.com", OriginCode)
	if got := Get().Site(); got != "us3.datadoghq.com" {
		t.Errorf("Site() after SetSite = %q, want %q", got, "us3.datadoghq.com")
	}
}

func TestSiteDefaultEmpty(t *testing.T) {
	t.Setenv("DD_SITE", "")
	resetGlobalState()
	if got := Get().Site(); got != "" {
		t.Errorf("Site() default = %q, want \"\"", got)
	}
}
```

`resetGlobalState` already exists in the test file (used by other migrated-config tests like `TestSetServiceName` — find it with `grep -n resetGlobalState internal/config/config_test.go`).

- [ ] **Step 3: Run the tests to confirm they fail**

Run: `go test ./internal/config/... -run TestSite`
Expected: FAIL with `c.Site undefined (type *Config has no field or method Site)`.

- [ ] **Step 4: Add the field on `Config`**

In `internal/config/config.go`, add a `site string` field inside the `Config` struct alongside other `string` fields (e.g., next to `serviceName`):

```go
	// site is the Datadog site (e.g. "datadoghq.com", "datadoghq.eu").
	site string
```

- [ ] **Step 5: Initialize it in `loadConfig`**

Add inside `loadConfig` (next to `cfg.serviceName = p.GetString("DD_SERVICE", "")`):

```go
	cfg.site = p.GetString("DD_SITE", "")
```

- [ ] **Step 6: Add getter and setter**

Append to `internal/config/config.go` next to `ServiceName`/`SetServiceName`:

```go
func (c *Config) Site() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.site
}

func (c *Config) SetSite(site string, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("DD_SITE", origin, site, product...) {
		return
	}
	c.site = site
	configtelemetry.Report("DD_SITE", site, origin)
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/config/... -run TestSite`
Expected: PASS.

- [ ] **Step 8: Replace caller in `ddtrace/tracer/option.go`**

Modify the `Site:` line (around line 464):

```go
		Site:       internalconfig.Get().Site(),
```

(`internalconfig` is already the import alias for `github.com/DataDog/dd-trace-go/v2/internal/config` in this file — confirm with `grep -n "internalconfig" ddtrace/tracer/option.go | head -3`.)

- [ ] **Step 9: Replace caller in `ddtrace/tracer/civisibility_transport.go` line ~98**

The original idiom is `if v := env.Get("DD_SITE"); v != "" { ... }`. Replace with:

```go
		if v := internalconfig.Get().Site(); v != "" {
```

If the import `internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"` is not present in this file, add it to the import block. Remove the unused `env` import if no other `env.Get` / `env.Lookup` calls remain in the file (let `goimports` handle this).

- [ ] **Step 10: Replace caller in `internal/civisibility/utils/net/client.go` line ~154**

Same pattern: `env.Get("DD_SITE")` → `internalconfig.Get().Site()`.

- [ ] **Step 11: Replace caller in `profiler/options.go` line ~225**

Same pattern.

- [ ] **Step 12: Confirm there are no remaining direct `DD_SITE` reads**

Run: `grep -rn '"DD_SITE"' --include="*.go" . | grep -v _test.go | grep -v internal/env | grep -v contrib/aws/datadog-lambda-go`
Expected: empty output. (The `contrib/aws/datadog-lambda-go/ddlambda.go` reference is a constant string for a public env-var name and is allowed to remain — it documents the env var, it does not read it.)

- [ ] **Step 13: Build and run the full suite for the touched packages**

Run:

```bash
go build ./...
go test ./internal/config/... ./ddtrace/tracer/... ./profiler/... ./internal/civisibility/...
```

Expected: PASS. Watch for cyclic-import errors from `internal/civisibility/utils/net/client.go` importing `internal/config` — if one occurs, fall back to the alternative migration step in the next bullet.

- [ ] **Step 14 (conditional): If `internal/civisibility/utils/net/client.go` cannot import `internal/config`**

Some packages already import `internal/config` (search with `grep -rn "internal/config\"" internal/civisibility/`). If a cycle exists, leave that call site untouched in this task and record the cycle as a follow-up in the inventory file (Task 8 doc). Migrations do not require eliminating every legacy read in one go — the audit's `STILL_READ` bucket will keep tracking it.

- [ ] **Step 15: Re-run the audit**

Run: `make config-audit | grep -E "DD_SITE|SUMMARY"`
Expected: `DD_SITE` no longer appears under `UNMIGRATED`; appears under `MIGRATED` (and possibly under `STILL_READ` if Step 14 applied).

- [ ] **Step 16: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go \
        ddtrace/tracer/option.go ddtrace/tracer/civisibility_transport.go \
        internal/civisibility/utils/net/client.go profiler/options.go
git commit -m "refactor(config): migrate site"
```

---

### Task 10 (template, repeated per remaining unmigrated config from Task 8)

For each entry in the **Unmigrated** section of `docs/superpowers/inventory/2026-05-21-config-migration-inventory.md`, repeat the steps below. The example uses `<FIELD>` as a placeholder for the camelCase field name (e.g., `appKey` for `DD_APP_KEY`) and `<DD_VAR>` for the env var name (e.g., `DD_APP_KEY`).

> Naming guidance: use the existing tracer-side field name when one exists (e.g., `option.go`'s `APPKey` → `appKey` here, getter `APIKey()` — pick the casing already in use elsewhere in `internal/config` for consistency). For string fields, the type is `string`; for booleans, `bool`; for ints, `int`; for durations, `time.Duration`. Match the type that `internal.BoolEnv` / `internal.IntEnv` / etc. produces at the call site.

- [ ] **Step 1: Locate every read site**

Run: `grep -rn '"<DD_VAR>"' --include="*.go" . | grep -v _test.go | grep -v internal/env`

If the env var name is hidden behind a `const` (the audit tool's output will say so via the `Func` field), grep for the constant name instead. Record every file:line that needs to change.

- [ ] **Step 2: Write failing tests in `internal/config/config_test.go`**

```go
func Test<Field>RoundTrip(t *testing.T) {
	t.Setenv("<DD_VAR>", "value-from-env")
	resetGlobalState()
	if got := Get().<Field>(); got != "value-from-env" {
		t.Errorf("<Field>() from env = %q, want %q", got, "value-from-env")
	}
	Get().Set<Field>("value-from-code", OriginCode)
	if got := Get().<Field>(); got != "value-from-code" {
		t.Errorf("<Field>() after Set<Field> = %q, want %q", got, "value-from-code")
	}
}
```

(For bool/int/duration, adapt the test values and the `t.Setenv` string accordingly.)

- [ ] **Step 3: Run tests to confirm failure**

Run: `go test ./internal/config/... -run Test<Field>`
Expected: FAIL with "undefined" error.

- [ ] **Step 4: Add the field on `Config`** (in `internal/config/config.go`, struct definition near line 67):

```go
	<field> <Type>
```

- [ ] **Step 5: Initialize in `loadConfig`**

Pick the right provider getter for the type:

| Type | Call |
|---|---|
| `string` | `p.GetString("<DD_VAR>", "<default>")` |
| `bool` | `p.GetBool("<DD_VAR>", <default>)` |
| `int` | `p.GetInt("<DD_VAR>", <default>)` (or `GetIntWithValidator` if there's a bound) |
| `float64` | `p.GetFloat("<DD_VAR>", <default>)` |
| `time.Duration` | `p.GetDuration("<DD_VAR>", <default>)` |

- [ ] **Step 6: Add the getter and the setter**

```go
func (c *Config) <Field>() <Type> {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.<field>
}

func (c *Config) Set<Field>(v <Type>, origin telemetry.Origin, product ...Product) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkProductConflict("<DD_VAR>", origin, v, product...) {
		return
	}
	c.<field> = v
	configtelemetry.Report("<DD_VAR>", v, origin)
}
```

- [ ] **Step 7: Run config tests, confirm pass**

Run: `go test ./internal/config/... -run Test<Field>`
Expected: PASS.

- [ ] **Step 8: Update every call site found in Step 1**

Replace the `env.Get("<DD_VAR>")` / `internal.BoolEnv("<DD_VAR>", …)` / `stableconfig.Bool("<DD_VAR>", …)` call with `internalconfig.Get().<Field>()`. Add the `internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"` import if needed. If a programmatic `WithSite("…")`-style option exists for this field, route it through `Set<Field>(value, internalconfig.OriginCode, internalconfig.ProductTracer)` — search for `internalConfig.Set` in `ddtrace/tracer/option.go` for prior art.

- [ ] **Step 9: Remove stale field on the legacy `config` struct**

If the field was previously held on `ddtrace/tracer.config` (or another product's local config), and there are no remaining references, delete the field. Use `grep -rn "<legacyFieldName>" ddtrace/tracer/` to verify.

- [ ] **Step 10: Build + test broadly**

```bash
go build ./...
go test ./internal/config/... ./ddtrace/tracer/... ./profiler/...
```

Expected: PASS. If a package outside the above list also references the migrated field, extend the test command to include it.

- [ ] **Step 11: Re-run the audit and confirm shrinkage**

Run: `make config-audit | grep -E "<DD_VAR>|SUMMARY"`
Expected: `<DD_VAR>` is now in the `MIGRATED` bucket. `unmigrated` count in SUMMARY decreased by 1.

- [ ] **Step 12: Commit**

```bash
git add <changed paths>
git commit -m "refactor(config): migrate <field>"
```

- [ ] **Step 13: Update the inventory document**

Edit `docs/superpowers/inventory/2026-05-21-config-migration-inventory.md` to strike through (or delete) the entry for `<DD_VAR>`. Amend or follow with a separate commit:

```bash
git add docs/superpowers/inventory/2026-05-21-config-migration-inventory.md
git commit -m "docs: mark <DD_VAR> migrated in inventory"
```

> **Stop condition.** Phase 4 ends when `make config-audit` reports `unmigrated=0` AND `still_read=0`. The remaining `MIGRATED` and `UNTRACKED` buckets are out of scope for this plan: the former is the success state, the latter is tracked separately as data-integrity bugs in `internal/env/supported_configurations.json`.

---

## Cross-cutting concerns

### Hot-path performance
Per [internal/config/README.md](../../../internal/config/README.md), any newly migrated config read in a hot path (span start/finish, partial flush) must either:
- be read through an existing or new snapshot in `internal/config/snapshots.go`, or
- have its hot-path read benchmarked before/after to confirm no regression.

When in doubt, run `go test -bench=BenchmarkStartSpanConcurrent -benchmem ./ddtrace/tracer/...` before and after a migration.

### Cross-product gate
Every `Set<Field>` setter **must** call `c.checkProductConflict(...)` as its first action after taking the lock — Task 9 Step 6 and Task 10 Step 6 already encode this. Do not skip it even for fields only one product uses today; it is the project's invariant.

### `Get()` vs `CreateNew()` at the call site
The singleton's lifecycle matters when migrating callers:
- **`internalconfig.Get()`** returns the cached singleton. Use this from any code path that runs after `Tracer.Start(...)`.
- **`internalconfig.CreateNew()`** forces a fresh load from env vars and replaces the cached singleton. Use this in `defaultConfig()`-style constructors that run at product startup, especially when tests use `t.Setenv(...)` to seed env before constructing the product. The tracer's `option.go` uses `CreateNew()` in `newConfig()`; the profiler's `defaultConfig()` (in `profiler/options.go`) does the same. If you replace a call site that's inside a per-call constructor and the corresponding tests fail with stale env values, the fix is to switch from `Get()` to `CreateNew()` (matching the prior art in that file).

### Test isolation
The audit tool excludes `_test.go` files from the unmigrated-reads scan. That is intentional: tests intentionally call `t.Setenv("DD_…", …)` and the singleton's setters directly. Tests that mutate state should call `resetGlobalState()` (see `internal/config/config_test.go`) at the top.

### Backports / V1
This plan targets the V2 module (`github.com/DataDog/dd-trace-go/v2`). The `release-v1` branch has its own config layout and is **out of scope** here.

---

## Acceptance criteria for the whole plan

1. `make config-audit` exits 0 and prints a `SUMMARY` line.
2. `.github/workflows/config-audit.yml` runs on PRs and uploads `audit.json`.
3. `docs/superpowers/inventory/2026-05-21-config-migration-inventory.md` exists and reflects current state.
4. After all Phase 4 iterations: `make config-audit | grep SUMMARY` shows `unmigrated=0 still_read=0`.
5. `go test ./...` is green across the module.
6. `grep -rn 'env\.Get("DD_\|env\.Lookup("DD_\|internal\.BoolEnv("DD_\|internal\.IntEnv("DD_\|internal\.FloatEnv("DD_\|internal\.DurationEnv("DD_' --include='*.go' . | grep -v _test.go | grep -v internal/config | grep -v internal/env | grep -v scripts/ | grep -v contrib/aws/datadog-lambda-go` returns no lines (string-literal sanity check; constant-hidden reads are still caught by the audit tool).
