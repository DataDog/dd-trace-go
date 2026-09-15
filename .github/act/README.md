# Running GitHub Actions locally with `act`

`act` lets you check a workflow-YAML edit before pushing, without waiting on a real CI run. The check covers the job graph, matrix expressions, composite-action wiring, and step logic.

**By default, act does not test your local Go code.** Every checkout step in this repo's workflows pins an explicit `ref:` input, which makes act's checkout fetch that ref over the real network instead of taking a local-copy shortcut. It tests the last commit you pushed, not your working tree, and not even your local commits if you have not pushed them. There is no reliable workaround for that checkout behavior.

Use act to check that you wrote the workflow YAML correctly, not to check that your code change passes CI. For a code change, use `make lint`, `make test`, or `make ci/run` instead (see "Reproducing CI locally" in [CONTRIBUTING.md](../../CONTRIBUTING.md)).

## Testing a real code change through act

Push a scratch branch, then point the checkout step at it:

```shell
git push origin HEAD:refs/heads/wip/ci-check-$USER
```

Edit `pull_request.head.ref` and `.sha` in `.github/act/events/pull_request.json`, or pass `--input ref=wip/ci-check-$USER` for a workflow that accepts a `ref` input. Delete the branch when you finish.

Pushing a scratch branch this way is a manual, occasional step.

## What works under act

`generate.yml`, `static-checks.yml` (except the `lint` job), `unit-integration-tests.yml`, `apidiff-check.yml`, and `config-audit.yml` run directly. Use the make targets below.

`static-checks.yml`'s `lint` job posts PR review comments through `reviewdog` and mints a GitHub App token to do it. act cannot execute that step. Run `make lint/go` and `make lint/shell` directly instead.

`system-tests.yml`, `parametric-tests.yml`, and `lambda-integration-tests.yml` check out a separate repository and hand off to its own harness. Clone that repository and run its harness directly instead of using act.

Anything requiring `macos-latest`, `windows-latest`, or GitHub/AWS OIDC-authenticated API calls cannot run under act at all.

## Usage

```shell
make act/list           # list every job act can see
make act/generate       # runs the generate.yml job; no services or credentials needed, start here
make act/static-checks  # excludes the lint job, see above
make act/core-tests
make act/contrib-tests CHUNK=3   # find the chunk with `make ci/contrib/chunks`
```

Each target wraps `act -W <file> -j <job>` (`scripts/act.sh`), using the platform mappings, network mode, and artifact server path pinned in [`.actrc`](../../.actrc). Run `scripts/act.sh` directly for anything not covered by a make target, for example to filter a matrix job:

```shell
./scripts/act.sh -W .github/workflows/static-checks.yml -j checklocks
./scripts/act.sh -W .github/workflows/generate.yml -j generate --matrix go-version:1.23
```

## Event fixtures

act has no real GitHub webhook to read, so `-e <file>` feeds it a fake `github.event` payload instead. `.github/act/events/` holds three:

- **`pull_request.json`** simulates a `synchronize` event (new commits pushed to an open PR). Every `make act/*` target uses this one, via `ACT_EVENT` in the `Makefile`. Its empty `labels` array feeds `apidiff-check.yml`'s check for a `breaking-api-acknowledged` label; add the label name to the array if you need that check to see it as acknowledged.
- **`push_main.json`** simulates a `push` to `main`, matching how `main-branch-tests.yml` triggers. No make target uses it. To use it, run `./scripts/act.sh -e .github/act/events/push_main.json ...`.
- **`workflow_dispatch.json`** simulates a manual "Run workflow" click. No make target uses it. To use `config-audit.yml`'s dispatch-specific logic. run `./scripts/act.sh -e .github/act/events/workflow_dispatch.json ...`.

## Known gotchas

- **Apple Silicon runs arm64, not CI's amd64.** `.actrc` does not force `--container-architecture linux/amd64`, because that breaks act's Node-based actions (`actions/checkout`, `actions/cache`) under emulation. Pass the flag yourself if you need to test an architecture-specific bug.
- **The runner image only approximates CI.** It is not a snapshot of the real GitHub-hosted runner, and it can resolve a different Go toolchain version than a local run. Treat a code-correctness finding from act as a lead to check locally, not as a verdict.
- **The `github` event context is incomplete.** Anything not in `.github/act/events/*.json` (PR labels, milestones, `workflow_run` triggers) does not match a real GitHub event. Extend the JSON fixtures if you need to exercise that path.
- **Host networking exposes your laptop, not an empty runner.** `--network host` means job containers share your machine's network namespace. A port your laptop already uses can make `docker compose up` fail to bind, or can silently point a test at your own local database instead of a throwaway one. Check `docker ps` for conflicts before you trust a green run.

## Troubleshooting

- **act prompts for a runner image size.** A `runs-on` label or group has no `-P` mapping in `.actrc`. Add one instead of answering the prompt.
- **`address already in use` starting compose services.** Something local already holds one of CI's ports (3306, 5432, 6379, 9126). Stop it.
- **Connection refused to a compose-published port.** Host networking is not active. Recheck the macOS prerequisites below.
- **A result under act does not match a local run.** Expected, per "Known gotchas" above. Re-run the underlying script or tool on the host before you trust an act-only result.
- **No DD_API_KEY found.** Expected, as `dd-octo-sts` will not work locally. The only impact is test result uploads, which is not necessary for local debugging.

## Prerequisites

- `act` >= 0.2.89 (`brew install act`)
- Docker running locally
- **macOS, for the test-core/test-contrib targets**: Docker Desktop >= 4.34, with host networking enabled (Settings -> Resources -> Network), a signed-in Docker account, and Enhanced Container Isolation disabled.
