# Config Audit

`configaudit` is a fail-closed proof that root-module `DD_*` and `OTEL_*`
configuration reads follow the migration contract. It syntax-scans all
production Go files without applying build constraints, recursively enumerates
every nested `go.mod`, and excludes nested-module trees from the root scan. It
also type-loads the host, Linux amd64, Windows amd64, and Linux amd64 AppSec
variants. JSON output contains the root module and excluded-module scope.

## Output categories

| Status | Meaning |
|---|---|
| `UNMIGRATED` | A key tracked in `supported_configurations.json` is read in the root module but has no valid raw definition and consumer binding in `internal/config`. |
| `STILL_READ` | A migrated key still has a direct root-module read outside its approved low-level owner. |
| `UNTRACKED` | A `DD_*` or `OTEL_*` key is read in code but is absent from `internal/env/supported_configurations.json`. |
| `UNRESOLVED` | The syntax pass cannot prove a raw-read key or reader identity. |
| `SUPPRESSION` | A `//nolint:configaudit` suppression was found. |
| `COVERAGE_ERROR` | A required package build variant did not load. |

Table output groups call sites by package. `make config-audit` is the
release/CI proof: it exits 0 with an empty table. JSON keeps scope metadata
when the audit is clean.

## Run

```sh
# Table output to stdout, grouped by package
make config-audit

# Focus on one package (prefix match against the path relative to the module root)
(cd scripts/configaudit && GOWORK=off go run . -root ../.. -package ddtrace/tracer)

# JSON for further processing (CallSite.Package is populated for grouping)
(cd scripts/configaudit && GOWORK=off go run . -root ../.. -format json) > /tmp/audit.json
```

## Suppressions

`configaudit` rejects every `//nolint:configaudit` annotation. Each annotation
is reported in the `suppressions` result bucket and makes the audit fail.
Remove the direct read; configaudit suppressions are not supported.

## Raw-read allowlist

The allowlist uses exact file, receiver, and function identities for approved
low-level raw reads. Each entry is checked against live root-module declarations.
Missing, ambiguous, stale, and nested-module entries fail the audit. Aliases and
dynamic keys outside approved low-level owners are findings.

## CI

The `.github/workflows/config-audit.yml` workflow runs the audit on every PR
and uploads `audit.json` as an artifact.
