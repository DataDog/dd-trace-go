# ddtrace/ — AI Assistant Context

This directory contains the core tracing API (`ddtrace/`) and the tracer implementation
(`ddtrace/tracer/`). It is the most concurrency-sensitive part of the codebase.

## Public API surface

`ddtrace/tracer/api.txt` tracks the exported API. Every change to this file is reviewed
carefully — naming, parameter order, and scope are all scrutinized. When in doubt about
an API change, discuss before implementing.

## Lock discipline

The tracer implementation uses fine-grained mutexes analyzed by the `checklocks` static
analyzer from gvisor. Follow these rules precisely:

- **Name locked functions with the `Locked` suffix** — any function that requires a
  specific mutex to be held by the caller must end in `Locked` (e.g., `setTagLocked`,
  `setPropagatingTagLocked`, `hasMetaKeyLocked`)
- **Annotate with `// +checklocks:s.mu`** (or the relevant field name) above any
  function that requires a lock — this enables static verification
- **Do not use closures inside locked regions** — closures confuse the `checklocks`
  analyzer and produce false positives; extract them into named methods instead
- **Avoid holding a lock while calling a function that acquires the same lock** — this
  causes deadlocks and `checklocks` will flag it

```go
// Bad: closure inside locked region confuses checklocks
mu.Lock()
defer mu.Unlock()
process(func() { s.setTagLocked(k, v) }) // checklocks can't follow this

// Good: extract to a named method
mu.Lock()
defer mu.Unlock()
s.processLocked(k, v)
```

## Running tests

```shell
# From repo root
make test/unit

# From this directory
go test ./...

# With race detector
go test -race ./...
```

The `checklocks` analyzer runs as part of `make lint`. Run it directly on a target:

```shell
./scripts/checklocks.sh ./ddtrace/tracer
```
