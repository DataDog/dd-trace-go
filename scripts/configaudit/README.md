# Config Audit

`configaudit` reports which `DD_*` environment-variable configurations have been
migrated to `internal/config` and which have not. It is the inventory tool
backing the migration tracked in [internal/config/README.md](../../internal/config/README.md).

## Output categories

| Status | Meaning |
|---|---|
| `UNMIGRATED` | The variable is read outside `internal/config` and is **not** yet handled by `loadConfig`. These are the migration backlog. |
| `STILL_READ` | The variable is migrated, but at least one caller outside `internal/config` is still reading it directly. Migration is incomplete; legacy reads should be replaced with calls to the singleton. |
| `UNTRACKED` | The variable is read in code but missing from `internal/env/supported_configurations.json`. Likely a bug — add it to the JSON or remove the read. |

Migrations proceed package-by-package, so output is grouped by the package
containing each call site. A variable that is migrated repo-wide can still
appear as `STILL_READ` in packages whose call sites haven't switched over
yet — that's expected, and the audit surfaces exactly which packages remain.

## Run

```sh
# Table output to stdout, grouped by package
make config-audit

# Focus on one package (prefix match against the path relative to the module root)
(cd scripts/configaudit && GOWORK=off go run . -root ../.. -package ddtrace/tracer)

# JSON for further processing (CallSite.Package is populated for grouping)
(cd scripts/configaudit && GOWORK=off go run . -root ../.. -format json) > /tmp/audit.json
```

## Suppressing intentional reads

If a direct env-var read is intentional and not a migration candidate, annotate
the line with `//nolint:configaudit`:

```go
} else if v := env.Get("DD_ENV"); v != "" { //nolint:configaudit — intentional: ...
```

The scanner skips any call site whose source line carries this annotation.

## CI

The `.github/workflows/config-audit.yml` workflow runs the audit on every PR and
uploads `audit.json` as an artifact. It does **not** fail the build — its job is
to give reviewers visibility into migration progress.
