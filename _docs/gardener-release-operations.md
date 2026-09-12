# Gardener release operation reconciliation

This document describes the operator contract for the disabled Gardener release automation. It does not authorize a release. Gardener dispatch remains disabled until the B13 and B14 gates below are attested.

## Immutable request identity

A request is identified by the repository ID and original issue-comment ID. Retries use the original four workflow inputs: `contract_version`, `command`, `version`, and `context`. The context retains the original comment ID, acknowledgement comment ID, body snapshot, and policy revision. An operator must not choose a replacement comment ID, version, branch, SHA, workflow ref, or policy.

Use the read-only wrapper command to render retry evidence:

```sh
GOWORK=off go run ./scripts/gardener-release/cmd/gardener-release inspect-reconciliation \
  --request /path/to/original-dispatch.json \
  --operation /path/to/verified-operation.json
```

Omit `--operation` only when complete comment evidence proves that Gardener acknowledged the request but no state record was ever reserved. The output category is then `acknowledged_no_record`; it is pending operator action, not permission to create a new operation automatically.

## Trusted workflow graph

The workflow runs only from `refs/heads/main` with repository-wide serialization. It validates the request and reads the signed state before it selects a phase. Each resumed phase accepts the current run's validated selector only when the recorded phase makes that job the first unfinished job. Later jobs require the immediate digest-linked artifact from the same run.

The generation and observation jobs have read-only credentials. The exact-SHA test job also has read-only credentials and does not write state. The protected tag job revalidates and persists test evidence before it creates the first tag. Signing, state writes, branch or tag publication, and pull-request creation use the `gardener-release` environment. Feedback uses a separate issues-only token outside that environment. A request that fails validation produces no selector and no workflow-side feedback mutation; Gardener owns pre-dispatch rejection feedback.

## Resume decisions

The signed state record is authoritative:

- No record: a completed inspector remains inert and reports `acknowledged_no_record`; investigate the acknowledgement and pending/replaced attempts. An operator-approved dispatch of the exact original four inputs may attempt deterministic reservation. The workflow treats absence as proven only after a complete signed state-branch read; a missing branch, read/signature/schema failure, or any orphaned request path is a conflict, never absence.
- `reserved`: resume generation from the recorded source and resolved version.
- `signed`: restore the exact recovery bundle. Never regenerate or re-sign.
- `branches_published`: wait for exact-SHA test evidence.
- `tests_passed`: revalidate current attempts and publish only missing recorded tags.
- `tags_published`: perform only applicable PR, image observation, outcome, and feedback work.
- `complete`: verify and report the stored outcome. Perform no publication.

A feedback-only retry re-fetches and binds the original and canonical acknowledgement comments, then edits only the canonical acknowledgement. It cannot enter generation, signing, state publication, branch publication, tag publication, PR creation, or image promotion.

Concurrent exact no-record dispatches use the signed state branch's expected-parent compare-and-swap. One reservation commit wins; another attempt may reconcile that exact reservation or stop on conflict. After any reservation exists, every retry resumes its recorded version and source and cannot resolve either value again.

## Stops requiring investigation

Stop unattended reconciliation when any of these is observed:

- edited or deleted original request;
- changed, duplicated, forged, or wrong-author acknowledgement marker;
- request hash, policy revision, actor, issue, repository, or canonical acknowledgement mismatch;
- invalid state or Git signature;
- missing recovery prerequisites;
- branch movement or conflicting tag;
- incomplete API pagination or ambiguous test/image evidence;
- missing protected-environment approval or signing/publication credential.

Never use a force push, delete a ref, select a new version, replace a tag, adopt the current branch tip, re-sign a recorded output, or treat a cancelled/pending-replaced run as authorization.

## Public feedback states

The fixed states are `rejected`, `pending`, `testing`, `partial_publication`, `tags_published`, `images_pending`, `images_failed`, `complete`, and `feedback_failed`. Public bodies contain fixed prose plus validated public version, commit, pull-request, and workflow-run links. Raw API responses, subprocess errors, dispatch text, tokens, and secrets are never rendered.

Publication outcome is persisted before feedback. Feedback failure does not roll back a phase and does not republish refs.

## B13 administrator gates

Production enablement remains blocked until administrators attest all of the following:

1. Protected environment `gardener-release`, literal-main deployment restriction, required reviewers, no self-approval, and no administrator bypass.
2. Environment secret `GARDENER_RELEASE_SSH_SIGNING_KEY`; matching reviewed principal, SSH public key, and SHA-256 fingerprint; GitHub verification status; and rotation/revocation owner.
3. Exact dd-octo-sts App identity and supported OIDC claim transformation. The policies must bind repository, `workflow_dispatch`, main execution ref, environment subject, and main workflow ref.
4. Separate least-privilege feedback token with issues write and no contents write.
5. Protected `gardener-release-state` branch, signed fast-forward-only commits, create permissions, and force-push/deletion denial.
6. Release/module tag creation bypass plus independent update/deletion denial.
7. Exact repository ID, Gardener identity, issue mapping, workflow IDs/digests, required jobs, and policy revision. This includes the repository variable `GARDENER_RELEASE_DOCKER_BUILD_WORKFLOW_SHA256`, set to the reviewed lowercase SHA-256 of `.github/workflows/docker-build-and-push.yml`, and the reviewed parent/child workflow pair backported to every configured release branch.
8. Disposable protected-environment rehearsal proving signing, recovery, allowed creates, denied updates/deletions, wrong-ref token denial, and no key access outside protected jobs.
9. Maintainer decisions for pre-merge development tags, broad-role authorization with environment approval, initial older-line image policy, and release-note timing.

The private signing key must never be placed in repository files, policy, artifacts, caches, logs, command arguments, or outputs. Protected jobs materialize it only in a runner-temporary 0600 file and delete the temporary directory on every exit path.
