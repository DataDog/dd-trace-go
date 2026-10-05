# Datadog Tracer for Go

This package provides the Datadog APM tracer for Go.

## API Stability

The public API of this package is tracked by CI using [`golang.org/x/exp/apidiff`](https://pkg.go.dev/golang.org/x/exp/apidiff). Changes are classified as **compatible** (additions, safe) or **incompatible** (removals/signature changes, breaking).

CI fails on incompatible changes. To acknowledge an intentional breaking change, add the `breaking-api-acknowledged` label to the PR — the workflow re-runs automatically with a warning instead of a failure.

### What constitutes an API change?

- Adding or removing exported functions, types, methods, or fields
- Changing function signatures
- Changing type definitions
- Changing interface definitions

### Checking API changes locally

```bash
# Full diff (compatible + incompatible)
make apidiff

# Breaking changes only — exits 1 if any are found
make apidiff/incompatible
```
