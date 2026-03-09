# contrib/ — AI Assistant Context

This directory contains instrumentation integrations for third-party libraries. Each
subdirectory is a self-contained package (and usually its own Go module) that wraps a
third-party library to emit Datadog traces.

## Testing

Run tests for the specific package you're working on:

```shell
cd contrib/<vendor>/<library> && go test ./...
```

Integration tests in this directory require Docker. Use `make test/contrib` from the
repo root to run all contrib tests via the test harness.

## New package checklist

Every new contrib package must have:

- A `go.mod` with module path ending in `/v2` in the declaration — e.g.,
  `module github.com/DataDog/dd-trace-go/contrib/rs/zerolog/v2`. The directory on disk
  does **not** get a `/v2` suffix (the directory is `contrib/rs/zerolog/`, not
  `contrib/rs/zerolog/v2/`)
- `option.go` — configuration struct and the standard option pattern (see below)
- `example_test.go` — runnable usage examples consistent with other contrib packages
- `orchestrion.yml` — automatic instrumentation support
- The standard copyright header on every `.go` file (CI enforces this)

After creating the `go.mod`, add the new module to `go.work` in the repo root and run
`make fix-modules`.

## Library initialization

`New*` and `Wrap*` functions must **never block**. Any I/O or network calls during
initialization (e.g., fetching cluster metadata) must be asynchronous:

```go
// Bad: blocks the caller's goroutine
func NewConsumer(conf *kafka.ConfigMap, opts ...Option) (*Consumer, error) {
    clusterID := fetchClusterID(c) // synchronous network call
    ...
}

// Good: returns immediately; enrichment happens in the background
func NewConsumer(conf *kafka.ConfigMap, opts ...Option) (*Consumer, error) {
    wrapped := WrapConsumer(c, opts...)
    wrapped.tracer.FetchClusterIDAsync(func() string { return fetchClusterID(c) })
    return wrapped, nil
}
```

If background work is running when the resource is released -- such as db.Close() -- cancel it via context or stop
channel so `Close()` does not block.

## Option pattern

`WithX` option functions are **user-facing public API only**. Do not create `WithX`
options to carry internal-only state between library layers — use unexported setters or
struct fields instead.

```go
// Bad: WithClusterID used only internally, not meant for users
opts = append(opts, WithClusterID(clusterID))

// Good: use an unexported setter
wrapped.tracer.setClusterID(clusterID)
```

The standard implementation in every `option.go`:

```go
type Option interface{ apply(*config) }
type OptionFn func(*config)
func (fn OptionFn) apply(cfg *config) { fn(cfg) }
```

## API design

- Order parameters from broadest → narrowest scope. For Kafka: cluster → group → topic
  → partition → offset. When adding a broader-scope parameter to an existing function,
  place it before the narrower ones — do not append it at the end.
- Public functions must encapsulate their preconditions. If a function needs normalized
  input, normalize inside the function — do not encode that requirement in the parameter
  name (e.g., `bootstrapServers`, not `normalizedBootstrapServers`).
- Use specific names for feature-specific APIs (`TrackDataStreamTransaction`, not the
  overly generic `TrackTransaction`).

## Environment variables

Use `env.Get`/`env.Lookup` from `instrumentation/env` — never `os.Getenv` directly.
New keys must be registered via `configinverter` before use (see root AGENTS.md).
