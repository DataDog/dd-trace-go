# Subtest Test Management & Retry Enablement

## Goals

- Honour Datadog Test Management directives (disable, quarantine, attempt-to-fix) for Go
  subtests created with `t.Run`.
- Allow subtests to orchestrate their own retry loops while keeping parent behaviour intact.
- Preserve existing semantics for flaky retries, coverage, logging, telemetry, and testify support.
- Provide deterministic integration tests that exercise the parent/subtest matrix.

This document walks through the implementation added since commit
`a722e23890a1f7306af5ac6bf9060e4846dbb895`, highlighting how the new pieces work together and the
nuances reviewers should keep in mind.

## Architecture Overview

The feature builds on three pillars:

1. **Identity plumbing** – every test/subtest now has a `testIdentity` with module, suite, base name,
   full name, and hierarchical segments (see `newTestIdentity`). This identity is threaded through
   `applyAdditionalFeaturesToTestFunc`, the orchestrion wrapper, and tests so configuration lookups
   can fall back to parent segments when a subtest lacks explicit settings.

2. **Enhanced instrumentation** – `applyAdditionalFeaturesToTestFunc` and
   `instrumentTestingTFunc` gained subtest-aware logic. Parents still own global wrappers, but
   subtests may now wrap themselves when the feature flag (`DD_CIVISIBILITY_SUBTEST_FEATURES_ENABLED`) is
   on and an exact directive exists. Attempt-to-fix orchestration honours a single “owner” to avoid
   double retries.

3. **Scenario harness** – the new `integrations/gotesting/subtests` package spins up a mock backend
   and walks through the parent/subtest matrix. Each scenario asserts span counts and tags, including
   retry telemetry (`test.is_retry`, `test.retry_reason`), so regressions surface immediately.

## Feature Gating & Settings Bootstrap

- `integrations/civisibility_features.go` honours `DD_CIVISIBILITY_SUBTEST_FEATURES_ENABLED` so
  environments can opt into subtest directives explicitly.
- `ciSettings.SubtestFeaturesEnabled` guards *every* subtest-specific branch. When the flag is off,
  behaviour is identical to pre-feature builds.

## Identity & Management Lookup

- `integrations/gotesting/testing.go`
  - Introduces `testIdentity` and `newTestIdentity`. Segments allow lookups like
    `TestParent/Sub1/Sub2`, falling back to `TestParent/Sub1`, then `TestParent`.
  - `commonInfo` now carries a pointer to the identity so wrappers can reuse it.
  - Internal test/benchmark instrumentation stores the identity and threads it into
    `applyAdditionalFeaturesToTestFunc`.
- `getTestManagementData` returns both the matched properties and `matchKind`
  (`Exact`, `Ancestor`, or `None`) so subtests can tell whether they should run the additional
  wrapper or inherit parent behaviour.
- The subtest matrix exercises identity matching directly; no extra test-only helpers are required.

## Instrumentation Enhancements

### `applyAdditionalFeaturesToTestFunc`

- Accepts an optional `parentExecMeta` so subtests know if their parent already owns attempt-to-fix
  retries.
- Builds a metadata struct with explicit flags (`hasExplicitAttemptToFix`, etc.) and the match kind.
- For subtests:
  - Requires the feature flag plus an **exact** match before wrapping.
  - Disables EFD/flaky auto retries to avoid double wrapping.
  - Only orchestrates attempt-to-fix locally if the subtest explicitly asked for it and the parent
    isn’t already handling retries.
- During execution:
  - Propagates explicit directives before OR-ing inherited values, ensuring targeted overrides win.
  - Emits debug logs to help trace scenario execution when verbose logging is enabled.
  - Only the orchestrator in charge emits attempt-to-fix retry logs and success tags.

### `instrumentation_orchestrion.go`

- On entry, computes the subtest identity and inspects parent metadata when available.
- Short-circuits in two cases:
  - Feature flag disabled or no directives present (`RUN_SUBTEST_CONTROLLER=1` harness case).
  - Parent already handles everything (legacy behaviour).
- For subtests needing instrumentation:
  - Calls `applyAdditionalFeaturesToTestFunc` with parent metadata so ownership stays consistent.
  - Handles panic, fail, skip, and pass paths, tagging attempt-to-fix success/failure and retry
    exhaustion where appropriate.
  - Writes verbose debug messages when debug logging is enabled to aid troubleshooting.

## Attempt-to-Fix Ownership Rules

1. **Parent only** – Parent orchestrates retries, subtests inherit attempt-to-fix tagging but never
   claim success. Child spans have `test.is_retry=false`.
2. **Subtest only** – Parent remains neutral, child wraps itself and emits retry spans.
3. **Parent & subtest** – Parent wins; child spans show attempt-to-fix tagging with zero retry tags,
   ensuring telemetry is counted once.
4. **Parent quarantine + attempt-to-fix** – When the parent carries both directives it remains the
   retry owner; children inherit the quarantine tag but do not emit retry spans. A quarantined parent
   without attempt-to-fix leaves subtests free to orchestrate their own retries if requested.

These rules are codified in `parentAttemptFixScenario`,
`subAttemptFixOnlyScenario`, `parentAndSubAttemptFixScenario`, and
`parentQuarantinedAttemptFixScenario`.

## Scenario Harness (`integrations/gotesting/subtests`)

- `TestMain` iterates scenarios by spawning subprocesses to keep environment state isolated.
- `startSubtestServer` mimics Datadog APIs (settings, test-management, logs, git endpoints). Every
  branch is commented and safe under the sandbox.
- Each scenario uses `scenarioContext` helpers to build a mock payload, including parent and subtest
  directives.
- `subtestcontroller_test.go` validates:
  - Span counts per resource.
  - Passage/failure status tags.
  - Attempt-to-fix telemetry (`test.is_retry`, `test.retry_reason`, `test.test_management.attempt_to_fix_passed`).
  - Quarantine/disable tags for parent/child combinations.
  - Parallel subtests (`SubAttemptFixParallel`) behave identically to sequential ones.
- Debug logging provides detailed trace output, including identity matches,
  retry decisions, and span metadata.

### Scenarios Covered

- Baseline (no directives)
- `sub_disabled`
- `sub_quarantined`
- `parent_quarantined`
- `parent_quarantined_attempt_to_fix`
- `parent_attempt_to_fix`
- `sub_attempt_to_fix_only`
- `sub_attempt_to_fix_custom_retries`
- `sub_attempt_to_fix_parallel`
- `parent_and_sub_attempt_to_fix`

Each scenario is thoroughly documented inline so future contributors can extend the matrix.

## Testing Metadata & Utilities

- Subtest scenarios interact with the existing instrumentation helpers (`propagateTestExecutionMetadataFlags`,
  `setTestTagsFromExecutionMetadata`, etc.), demonstrating metadata propagation without introducing additional
  white-box-only harnesses.
- Ancestor fallback is exercised through the `subtests` matrix scenarios, removing the need for exported helper wrappers and keeping white-box tests out of the hot path.

## Feature Flags & Environment Variables

- `DD_CIVISIBILITY_SUBTEST_FEATURES_ENABLED` – enable subtest-specific behaviour.
- `CIVisibilityTestManagementAttemptToFixRetries` – global attempt-to-fix retry budget (already
  supported but exercised by new scenarios).
- `RUN_SUBTEST_CONTROLLER` – harness env var that forces the orchestrion wrapper to defer to the
  scenario driver when no directives exist.

## Summary

- Subtests now honour Datadog Test Management directives without breaking parent behaviour, testify
  support, or legacy flows.
- Attempt-to-fix orchestration chooses a single owner (parent by default) to prevent double retries
  and conflicting telemetry.
- The integration harness thoroughly exercises the directive matrix, including parallel subtests,
  custom retry budgets, and quarantine inheritance.
- Extensive inline documentation across instrumentation and tests explains each branch and decision,
  easing PR review and future maintenance.
