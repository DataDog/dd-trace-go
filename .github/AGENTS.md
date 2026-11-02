# CI workflows, actions and scripts

* Every `curl` run in CI must fail on HTTP errors and retry: `curl -fsSL --retry 5 --retry-delay 2 --retry-connrefused`. For downloads (safe to repeat), add `--retry-all-errors`.
* Use fewer retries only where a hung attempt is costly, with a comment saying why (e.g. `actions/dd-ci-upload`, retried once).
* Don't retry non-idempotent requests (e.g. metric submissions): a retry after a response timeout sends the payload twice.
* Make the failure stop the step: use `pipefail` for `curl | tar`/`curl | jq`, and download scripts to a file instead of `curl | sh`.
* The merge queue builds every queued commit on a `mq-working-branch-<target>-<hash>` branch before it lands on the target branch. A workflow with a `push` trigger must therefore list `mq-working-branch-**` under `on.push.branches`, or a queued commit can land without its checks. Only the queue can exclude a lane deliberately: it excludes the OTelc lane (`otelc.yml`). Keep `TestOrchestrionRunsOnTheMergeQueue` and `TestOTelcSkipsTheMergeQueue` in `scripts/ciselect/workflows_test.go` in sync with the workflow files.
