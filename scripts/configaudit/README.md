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
(cd scripts/configaudit && GOWORK=off go run . -root ../.. -format json) > /tmp/audit.json
```

## CI

The `.github/workflows/config-audit.yml` workflow runs the audit on every PR and
uploads `audit.json` as an artifact. It does **not** fail the build — its job is
to give reviewers visibility into migration progress.
