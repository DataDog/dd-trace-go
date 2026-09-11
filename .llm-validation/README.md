# LLM Validation — `dd-apm-sdk-review`

This folder is how we test the review skill. It is **not** a `go test`.
The cases live here; the runner lives in [`ddoghq/llm-validation-platform`](https://github.com/ddoghq/llm-validation-platform).

Same gate as [`dd-trace-js#10137`](https://github.com/DataDog/dd-trace-js/pull/10137) and
[`dd-trace-java#12409`](https://github.com/DataDog/dd-trace-java/pull/12409).

It answers: *did an edit to a review rule make the agent better or worse?*

## Add a rule (this is the whole contribution)

Overrides are owned by this repo. The shared core is not — never edit `.agents/skills/dd-apm-sdk-review/`.

1. Create or extend a file under [`.agents/dd-apm-sdk-review-overrides/reviewers/`](../.agents/dd-apm-sdk-review-overrides/reviewers/).
   Copy the shape of `performance.md` or `security.md`: one pattern, why it matters, the fix.
2. Add a case in [`suites/dd-apm-sdk-review.yaml`](./suites/dd-apm-sdk-review.yaml). Copy the starter case.
   A good case is a 10-line snippet plus 2–3 `expected_criteria` that would fail if the rule disappeared.
3. List the new case id under `presets.gate.cases` in [`config.yaml`](./config.yaml) if you want CI to run it.
4. Open a PR. That is it.

The starter case in this folder is the example. Keep new ones that short.

## Layout

| Path | Role |
|---|---|
| [`config.yaml`](./config.yaml) | Monitored instruction files, model, `--level` presets |
| [`suites/dd-apm-sdk-review.yaml`](./suites/dd-apm-sdk-review.yaml) | Cases (one file only — the CLI errors if `suites/` has more than one YAML) |

## Run locally (Docker)

From the **`dd-trace-go` repo root**:

```bash
export LLMVAL_IMAGE=registry.ddbuild.io/ci/llm-validation-platform/llmval:latest
docker pull "$LLMVAL_IMAGE"

# Offline smoke — no gateway, no Claude (1 case)
docker run --rm -v "$PWD:/repo" "$LLMVAL_IMAGE" \
  --repo /repo --base-sha main --level minimum --fake

# Cheap real smoke — still 1 case
export LLMVAL_AUTH_HEADER="$(ddtool auth token rapid-ai-platform --datacenter us1.staging.dog --http-header)"
docker run --rm -e LLMVAL_AUTH_HEADER -v "$PWD:/repo" "$LLMVAL_IMAGE" \
  --repo /repo --base-sha main --level minimum --runs 1

# The gate preset (starter case; add more ids in config.yaml to grow it)
docker run --rm -e LLMVAL_AUTH_HEADER -v "$PWD:/repo" "$LLMVAL_IMAGE" \
  --repo /repo --base-sha main --level gate --runs 1

# One named case
docker run --rm -e LLMVAL_AUTH_HEADER -v "$PWD:/repo" "$LLMVAL_IMAGE" \
  --repo /repo --base-sha main --case go-perf-startspanconfig-first --runs 1
```

`--level` picks **which cases** run (`minimum` / `gate` = the starter case until you add more, `full` = every case).
`--runs` only repeats those cases. Needs `ddtool` on the host for a real (non-`--fake`) run.

CI includes the reusable `"llm validation"` job from the platform repo (see `.gitlab-ci.yml`).
This repo's default branch is `main`, so the job sets `LLMVAL_BASE_REF: "main"`.

## What a pass means

This is an A/B comparison, not an absolute score:

- **Candidate** = the working tree. Uncommitted edits count.
- **Baseline** = `git show <base-sha>:<file>`. A file that is not on `main` yet is treated as added.

The gate fails only on a **confident regression**. Noisy changes WARN and do not block.
