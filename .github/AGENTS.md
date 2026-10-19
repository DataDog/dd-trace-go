# CI workflows, actions and scripts

* Every `curl` run in CI must fail on HTTP errors and retry: `curl -fsSL --retry 5 --retry-delay 2 --retry-connrefused`. For downloads (safe to repeat), add `--retry-all-errors`.
* Use fewer retries only where a hung attempt is costly, with a comment saying why (e.g. `actions/dd-ci-upload`, retried once).
* Don't retry non-idempotent requests (e.g. metric submissions): a retry after a response timeout sends the payload twice.
* Make the failure stop the step: use `pipefail` for `curl | tar`/`curl | jq`, and download scripts to a file instead of `curl | sh`.
