# Go Version Rollout

This document describes how `go-versions.yml` serves as the single source of truth
for Go versions in the dd-trace-go ecosystem, what is automated, and what still
requires manual action.

For the full context, see the [Confluence rollout checklist](https://datadoghq.atlassian.net/wiki/spaces/DL/pages/3884220944).

---

## How It Works

`go-versions.yml` at the repo root declares four fields:

```yaml
stable: "1.26"           # Current stable minor version
oldstable: "1.25"        # Previous stable minor version (still supported)
stable_patch: "1.26.0"   # Patch version for stable
oldstable_patch: "1.25.7" # Patch version for oldstable
```

When this file is updated on `main`, the `go-versions-changed` GitHub Actions workflow
triggers rebuilds and updates across all downstream repositories automatically.

### Automation Chain

```
go-versions.yml updated on main
  ├── go-versions-changed.yml (GitHub Actions)
  │   ├── trigger benchmarking-platform GitLab pipeline (with GO_VERSION)
  │   │   ├── compute-image-tag (content-hash based)
  │   │   ├── build-ci-images (builds new Docker image)
  │   │   └── notify-dd-trace-go → dispatch base-image-updated
  │   │       └── update-base-image.yml → auto-PR to update BASE_CI_IMAGE
  │   ├── trigger apm-sdks-benchmarks GitLab pipeline
  │   │   └── parse-go-versions → dotenv → all Go benchmark jobs
  │   └── dispatch go-version-updated to datadog-reliability-env
  │       └── go-version-updated.yml → update-go-versions.sh → auto-PR
  ├── parse-go-versions (.gitlab-ci.yml, local read)
  │   └── dotenv → macrobenchmarks (stable/oldstable)
  └── system-tests fetches go-versions.yml on each CI run
      └── GO_VERSION → build-arg → all Go Dockerfiles
```

---

## Rollout Procedure

When Go releases a new version (e.g., Go 1.27):

### Step 1: Edit `go-versions.yml`

```yaml
# Before (Go 1.27 released, 1.26 becomes oldstable):
stable: "1.26"
oldstable: "1.25"
stable_patch: "1.26.0"
oldstable_patch: "1.25.7"

# After:
stable: "1.27"
oldstable: "1.26"
stable_patch: "1.27.0"
oldstable_patch: "1.26.5"
```

### Step 2: Update `go.mod` and `go.work`

```bash
./scripts/rollout-go-version.sh
```

This script reads `go-versions.yml` and updates `go.work` and all ~87 `go.mod` files
to declare `oldstable_patch`. It then runs `go mod tidy` on every module.

> **Why `oldstable_patch`?** The `go.work` comment explains: the version must match
> the *lowest* supported Go version, not the highest. This ensures dd-trace-go compiles
> with both supported Go releases.

### Step 3: Verify Consistency

```bash
./scripts/check-go-versions.sh
```

This audits the repo and reports any drift between `go-versions.yml` and the
module files. Run with `--strict` to treat warnings (e.g., golangci-lint tooling)
as failures.

### Step 4: Commit and Merge

```bash
git add go-versions.yml go.work go.work.sum
git add $(git diff --name-only | grep go.mod)
git commit -m "feat: bump Go to stable=1.27.0 oldstable=1.26.5"
```

Open a PR and merge. Once merged to `main`, the automation chain fires automatically.

---

## What Is Automated

| Checklist Item | Trigger | Notes |
|---|---|---|
| GitHub Workflows (`setup-go` matrices) | `go-versions` action | Reads `go-versions.yml` directly; matrix updates on PR |
| `go.mod` / `go.work` versions | Manual (`rollout-go-version.sh`) | Developer runs script after editing `go-versions.yml` |
| benchmarking-platform container rebuild | `go-versions-changed.yml` | Triggers GitLab pipeline with `GO_VERSION` |
| `BASE_CI_IMAGE` auto-PR in dd-trace-go | `update-base-image.yml` | benchmarking-platform dispatches back after build |
| apm-sdks-benchmarks pipeline | `go-versions-changed.yml` | Triggers GitLab pipeline |
| macrobenchmarks Go version matrix | `.gitlab-ci.yml` `parse-go-versions` | Reads `go-versions.yml` locally via dotenv |
| system-tests Dockerfiles | system-tests CI | Fetches `go-versions.yml` from main on every run |
| datadog-reliability-env (`deployment.cue`, dashboards) | `go-versions-changed.yml` | Dispatches to reliability-env; script does key rotation + PR |

---

## Remaining Manual Steps

These items still require human judgment:

| Item | Why Manual | What To Do |
|---|---|---|
| golangci-lint version | New Go releases may require a newer linter | `check-go-versions.sh` will warn; see its output for exact commands |
| `go-libddwaf` release | Requires ASM team coordination | Contact the ASM team before merging |
| `DataDog/images/mirror.yaml` | Separate repo/process | Add new Go image mirror entry manually |
| `go:build` build tags | Requires source code review | Search for `//go:build goX.Y` tags and update as needed |

---

## Scripts Reference

### `scripts/rollout-go-version.sh`

Updates `go.work` and all `go.mod` files to match `oldstable_patch` in `go-versions.yml`.

```
Usage: ./scripts/rollout-go-version.sh [--dry-run]

  --dry-run   Print what would change without modifying any files.
```

### `scripts/check-go-versions.sh`

Audits the repo for consistency with `go-versions.yml`.

```
Usage: ./scripts/check-go-versions.sh [--strict]

  --strict    Exit 1 on warnings (e.g., golangci-lint tooling out of sync),
              not just on hard failures. Suitable for CI gating.
```

---

## Architecture Notes

- **`go-versions.yml` is append-only** in terms of keys: never remove a key without
  updating all consumers (GitHub Actions, GitLab CI, system-tests).

- **Minor vs. patch versions**: `stable`/`oldstable` are minor versions used in
  `setup-go` actions (which accept `"1.26"` to pick the latest patch). `stable_patch`/
  `oldstable_patch` are exact patch versions used for container image tags and
  `go.mod`/`go.work` directives.

- **Content-hash image tags**: benchmarking-platform computes a `sha256` of
  `go-versions.yml` to produce a stable, content-addressed Docker image tag. This
  ensures the same version bump always maps to the same image tag, enabling safe
  re-runs without rebuilding.
