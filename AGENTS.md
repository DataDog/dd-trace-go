# dd-trace-go — AI Assistant Context

dd-trace-go is Datadog's Go tracing and profiling library — APM instrumentation for
Go applications and instrumentation integrations for third-party libraries.

## Commands

```shell
make lint           # golangci-lint — required before every push (CI enforces)
make format         # gofmt + goimports
make fix-modules    # go mod tidy across all modules (required after any dependency change)
make test/unit      # core unit tests (no Docker required)
make test           # all tests — unit + integration + contrib (requires Docker)
make test/contrib   # contrib integration tests only (requires Docker)
make tools-install  # install all dev tools
```

**Per-package tests** — use these during contrib development for faster feedback:

```shell
cd contrib/<vendor>/<library> && go test ./...
```

**New modules** — after adding a new `go.mod`, add the module to `go.work` and run
`make fix-modules` to update `go.work.sum`.

**New environment variables** — register before use (see CONTRIBUTING.md for full workflow):

```shell
go run ./scripts/configinverter/main.go add DD_MY_NEW_KEY
go run ./scripts/configinverter/main.go generate
```

## Repository layout

- `ddtrace/` — core tracing API and tracer implementation; `ddtrace/tracer/api.txt`
  tracks the public API surface and is reviewed on every change
- `contrib/` — instrumentation integrations (one subdirectory per third-party library,
  each with its own `go.mod`)
- `instrumentation/` — shared helpers used across contrib packages
- `internal/` — internal packages; not part of the public API, can change freely
- `profiler/` — continuous profiling client

## Code conventions

### Library initialization

`New*`, `Wrap*`, and `Setup*` functions must never block. Any I/O or network calls during
initialization must be asynchronous (goroutines). See CONTRIBUTING.md → *Never block in
library initialization* for the rationale and examples.

### Option functions

`WithX` option functions are **user-facing public API only**. Never use them to pass
internal state between library layers — use unexported setters or struct fields instead.

### API design

- Order parameters from broadest → narrowest scope (e.g., cluster → group → topic →
  partition → offset for Kafka)
- Public functions must encapsulate their preconditions — callers must not need to
  pre-process arguments before calling
- Prefer specific names for feature-specific APIs (`TrackDataStreamTransaction`, not
  the overly generic `TrackTransaction`)

### Lock discipline (`checklocks`)

- Functions that must be called with a lock held are named with the `Locked` suffix
  (e.g., `setTagLocked`, `setPropagatingTagLocked`)
- Annotate such functions with `// +checklocks:s.mu` (or the relevant field name)
- Do not use closures inside locked regions — they confuse the `checklocks` static
  analyzer; extract them into named methods instead

### Naming

- Use `noop` (not `nop`) for no-op implementations
- Use full Go initialisms: `OTel`, `HTTP`, `URL`, `ID`
- Use `reflect.Pointer` (not the deprecated `reflect.Ptr`)
- Distinguish test doubles precisely: `fake` = working simplified implementation;
  `mock` = call-recorder that verifies expectations

### Constants

Any literal value with semantic meaning must be a named constant. Magic numbers are
rejected in review.

### Environment variables

Never call `os.Getenv` or `os.LookupEnv` directly. Use `env.Get`/`env.Lookup` from:

- `instrumentation/env` — for contrib packages
- `internal/env` — for core packages

New keys must be registered via `configinverter` (see commands above) and added to
Datadog's internal configuration registry by a maintainer before the PR can merge.

### Performance

In hot paths, avoid unnecessary allocations:

- Prefer `dst = append(dst, ...)` over allocating a new slice
- Use string builders or concatenation (`a + "b"`) over `fmt.Sprintf` in tight loops
- Use standard library helpers: `slices.Contains`, `binary.BigEndian.AppendUint64`

### Compile-time interface assertions

Types intended to satisfy an interface require a compile-time assertion near the type
definition:

```go
var _ InterfaceName = (*TypeName)(nil)
```

### Generated files

Do not hand-edit `.gen.go` files — and never in a way that breaks their sort order or
canonical format, since this breaks future regeneration. Run the relevant generator
instead.

### Imports

Three groups, separated by blank lines: standard library → third-party → internal
`github.com/DataDog/dd-trace-go/...`. `goimports` handles this, but reviewers verify
alignment for new files.

## Testing conventions

- New public API requires tests
- Test helper files must end in `_test.go` to avoid being compiled into production builds
- Tests must exercise real behavior — setups that make assertions vacuous are rejected
- Do not add test-only fields to production structs; structure tests to avoid this need
- When fixing a concurrency or race-condition bug, add a regression test (even as a
  follow-up PR if needed)
- In packages that use `testing/synctest` (Go 1.24+), use deterministic time
  advancement rather than `assert.Eventually` with wall-clock timeouts
- Benchmark changes must not introduce measurement artifacts; use `//nolint:modernize`
  when `b.Loop()` would skew results

## Contrib package conventions

New contrib packages must:

- Use a module path ending in `/v2` in the `go.mod` `module` declaration (e.g.,
  `github.com/DataDog/dd-trace-go/contrib/rs/zerolog/v2`); the directory on disk
  does **not** have a `/v2` suffix (e.g., `contrib/rs/zerolog/`)
- Include `option.go` with the standard `Option` interface / `OptionFn` pattern
- Include `example_test.go` with runnable usage examples
- Include an `orchestrion.yml` for automatic instrumentation support
- Have the standard copyright header on all files — copy it from any existing file
  (CI enforces this and will block the merge)

## Definition of done

A task is complete when **all** of the following pass locally before pushing:

1. `make lint` — zero errors
2. `make format` — no diff
3. `make fix-modules` — no diff (if dependencies changed)
4. Tests pass for the modified package(s)
5. All new `.go` files have the standard copyright header (copy from any existing file)
6. No unrelated changes are bundled in the PR

See [CONTRIBUTING.md](./CONTRIBUTING.md) for full contributor guidelines including PR
naming conventions (conventional commits format) and code quality rules with examples.
