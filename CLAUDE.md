# dd-trace-go — AI Assistant Context

## Coding guidelines

All contributor coding guidelines live in [CONTRIBUTING.md](./CONTRIBUTING.md). Read it before
making changes, especially the **Code quality** section which covers:

- Never blocking in library initialization (`New*`, `Wrap*`, `Setup*`)
- `WithX` option functions are user-facing API only
- Public API parameter ordering (broadest → narrowest scope)
- Public functions must encapsulate their preconditions
- Inject dependencies instead of duplicating function bodies

## Before pushing

Run `make lint` and `make format` locally — CI enforces `gofmt` and golangci-lint and will
reject unformatted or lint-failing code.

## Repository layout

- `ddtrace/` — core tracing API and tracer implementation
- `contrib/` — instrumentation integrations (one directory per third-party library)
- `instrumentation/` — shared instrumentation helpers used by contrib packages
- `internal/` — internal packages not intended for external use
- `profiler/` — continuous profiling client
