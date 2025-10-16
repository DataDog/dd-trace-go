# Plan: Subtest-Level Test Management & Flaky Retry Support

## Goals
- Extend CI Visibility to honor Test Management decisions (quarantine/disable/attempt-to-fix) and Flaky Test Retries for Go subtests (`t.Run`) without regressing current parent-test behaviour.
- Retain existing semantics for Early Flake Detection (EFD), Intelligent Test Runner (ITR), coverage, logging, and telemetry.
- Preserve backend compatibility: continue to accept module/suite/test maps while allowing keys that represent hierarchical subtest names (`TestFoo/Subcase`).

## Constraints & Guardrails
- Parent tests must continue to resolve configuration exactly as today when no subtest-specific configuration is present.
- Subtests must fall back to parent configuration when backend data omits their specific name.
- Retry orchestration must remain single-owner to avoid double wrapping (parent *and* child) and to ensure counters like `_dd.auto_test_retries` stay accurate.
- Avoid breaking orchestrion-based instrumentation and testify integration (shared code paths must continue to mark instrumentation metadata correctly).
- Continue respecting environment kill switches (`CIVisibility*` env vars), test metadata caching, and `Run` semantics in parallel mode.
- Any function modification or newly introduced function/struct/type must include documentation and targeted comments that explain intent and rationale for future maintainers.
- Early Flake Detection (EFD) must continue to operate exactly as today; no plan item should change EFD logic or configuration flow.

## New functionality matrix

| P. Disabled | S. Disabled | P. Quarantined | S. Quarantined | P. Attempt-to-Fix | S. Attempt-to-Fix | P. Retries | S. Retries | Outcome                                                                                                                                    |
|-------------|-------------|----------------|----------------|-------------------|-------------------|------------|------------|--------------------------------------------------------------------------------------------------------------------------------------------|
| No          | No          | No             | No             | No                | No                | No         | No         | Both run normally                                                                                                                          |
| No          | No          | No             | No             | No                | No                | No         | Yes        | Parent don't retry, but subtest do the retries                                                                                             |
| No          | No          | No             | No             | No                | No                | Yes        | No         | Parent retries both parent and subtest; subtest runs once per parent retry                                                                 |
| No          | No          | No             | No             | No                | No                | Yes        | Yes        | Parent retries both parent and subtest; don't want multiply retries, subtests runs once per parent retry                                   |
| No          | No          | No             | No             | No                | Yes               | No         | No         | Parent don't run the attempt to fix retries; subtest do the attempt to fix retries.                                                        |
| No          | No          | No             | No             | Yes               | No                | No         | No         | Parent do the attempt to fix retries; subtest don't do anything and also don't report any attempt to fix tags.                             |
| No          | No          | No             | No             | Yes               | Yes               | No         | No         | Parent do the attempt to fix retries; don't want multiply retries, subtests runs once per parent retry but report the attempt to fix tags. |
| No          | No          | Yes            | No             | No                | No                | No         | No         | Parent is quarantined, so subtest is also quarantined. Both run but reported skipped.                                                      |
| No          | No          | No             | Yes            | No                | No                | No         | No         | Subtest is quarantined (run but reported skipped). Parent runs normally.                                                                   |
| No          | No          | Yes            | Yes            | No                | No                | No         | No         | Both runs but reported skipped.                                                                                                            |
| Yes         | No          | No             | No             | No                | No                | No         | No         | Parent is disabled (not run) so subtest is disabled (not run) as well.                                                                     |
| No          | Yes         | No             | No             | No                | No                | No         | No         | Subtest is disabled (not run) but Parent runs normally.                                                                                    |
| Yes         | Yes         | No             | No             | No                | No                | No         | No         | Both Parent and Subtest are disabled (not run).                                                                                            |
| Yes         | No          | No             | Yes            | No                | No                | No         | No         | Parent is disabled (not run) so subtest will not run.                                                                                      |

P = Parent test; S = Subtest

As you can see from the table above, parent-level settings take precedence over subtest-level settings for disable/quarantine/attempt-to-fix. Subtests can independently request retries when parents do not, but when both request retries, only the parent drives the retry loop to avoid nested retries.
You have to consider all combinations of parent and subtest settings to ensure the correct behavior is implemented. And create a complete test suite for this matrix to validate the implementation. Similar to what we have for the current logic (see `integrations/gotesting/testcontroller_test.go`).
Because we don't want to keep the existing logic, don't modify the existing scenarios (current behavior depends on the number of tests defined in the `gotesting` package, maybe is better to create a new package (`subtests`) only for testing this new functionality (e.g., `subtests/subtestcontroller_test.go`).


## High-Level Approach
1. **Model identities consistently**: introduce a runtime-resolved descriptor that captures module, suite, and fully qualified test path (including nested subtest segments).
2. **Broaden backend lookups**: allow `getTestManagementData` and related consumers to match hierarchical names (exact and fallback).
3. **Refactor additional feature wrapper**: split parent-specific prework from reusable logic so both parent tests and subtests can opt into management/retry flows without double wrapping.
4. **Inject wrappers in subtests**: invoke the refactored additional-feature pipeline from `instrumentTestingTFunc` once the subtest identity is known, keeping parent wrappers intact.
5. **Propagate retry state fully**: ensure execution metadata for subtests carries last-retry flags and attempt status so tags/telemetry align with behaviour.
6. **Hardening & tests**: add coverage for new cases (disabled/quarantined subtests, attempt-to-fix subtests, flaky retries in subtests, mixed parent/subtest configs, parallel subtests) and verify no regressions.

---

## Detailed Steps

### 1. Canonical Test Identity
- **Introduce `testIdentity` struct** containing:
  - `ModuleName`, `SuiteName` (existing values)
  - `BaseName` (top-level test name)
  - `FullName` (Go subtest path from `t.Name()`)
  - `Segments` (`[]string` split by `/`) for easier parent traversal.
- **Populate identities**:
  - For parent tests inside `executeInternalTest`: BaseName = FullName = original test name.
  - For subtests inside `instrumentTestingTFunc`: compute `FullName` via `t.Name()` at call time; derive `BaseName` from first segment.
  - Store identity (or pointer) on the execution metadata so downstream code can reference it.
- Ensure existing code that formats module/suite names keeps using the same values. Support arbitrary subtest depth by treating `Segments` as unbounded.

### 2. Backend Data Matching Updates
- Extend `getTestManagementData` to:
  - Accept `testIdentity`.
  - Attempt exact match on `identity.FullName`.
  - Fallback to legacy `identity.BaseName` (preserves today’s behaviour).
  - Optionally support case-normalized or escaped variants if backend standardizes differently (document assumptions).
- Mirror the same lookup logic for known tests / impacted-tests if they later expose subtest granularity (prepare helpers even if not yet used).
- Update telemetry counters (`TestManagementTestsResponseTests`) if nested keys inflate totals (verify counting logic).
- Add unit tests using fixtures with hierarchical names (extend `utils/testdata/fixtures/...` as needed).

### 3. Feature Controller Refactor
- Extract the feature orchestration code from `applyAdditionalFeaturesToTestFunc` into a reusable component, e.g.:
  - `newFeatureController(identity *testIdentity) *featureController`.
  - Methods to load management data lazily when `*testing.T` is available (so subtests can resolve dynamic names).
  - Keep existing sequencing: management → EFD → flaky retries, but invoke the existing EFD path unmodified.
- Adjust `applyAdditionalFeaturesToTestFunc` (parent path) to:
  - Build controller with static identity.
  - Delegate retry loop orchestration to a shared helper to preserve behaviour.
- Ensure controller caches backend lookups per identity to avoid repeated map scans.
  - Provide `resolveEffectiveManagementConfig(parent, child)` helper: start from parent directives, allow child to override only additive fields (e.g., metadata notes) while keeping parent-disabled/quarantined/attempt-to-fix states authoritative; propagate the resolved config in both parent and subtest flows so tagging reflects the enforced precedence.

### 4. Subtest Wrapper Injection
- In `instrumentTestingTFunc`:
  - After creating `instrumentedFn`, build a thin shim that:
    1. Computes the subtest identity (`t.Name()`, parent pointer, module/suite).
    2. Invokes the shared controller (which will perform management checks, attach metadata, and decide whether to run retries around the subtest body).
  - Return the wrapped function only when at least one ancillary feature is enabled; otherwise keep fast path.
- Prevent double instrumentation:
  - Use `instrumentationMetadata` flags (`IsInternal`) to detect when the wrapper already encapsulates retries (e.g., orchestrion-generated wrappers).
  - Ensure subtest wrapper doesn’t attempt to re-wrap internal Datadog helper subtests (if any exist).
  - If extending `instrumentationMetadata`, only add backward-compatible, zero-value-safe fields (e.g., `IsSubtestWrapper`); document the semantics inline and update orchestrion/testify integrations to assert against the new shape.
- Handle parallel subtests carefully:
  - Respect existing logic that clones `testing.T` instances and manages barriers.
  - When running retries, reuse `runTestWithRetry`, making sure the mutex selection (`noopMutex` vs `sync.Mutex`) still honours `CIVisibilityInternalParallelEarlyFlakeDetectionEnabled`.

### 5. Execution Metadata Enhancements
- Extend `testExecutionMetadata` to include:
  - Pointer to `testIdentity`.
  - Last retry/attempt flags accessible to subtest finalizers (`instrumentTestingTFunc`).
  - Reference to parent execution metadata so subtests can consult inherited state (e.g., parent skip reasons, attempt-to-fix ownership) without recomputing lookups.
- Update `propagateTestExecutionMetadataFlags` to copy:
  - `isLastRetry`, `allAttemptsPassed`, `allRetriesFailed`.
  - Final attempt counts (`RemainingTotalRetryCount` adjustments should still be parent-scoped; ensure subtests don’t decrement the global counter twice).
  - Effective management configuration snapshot so logs/telemetry emitted outside the retry loop remain consistent even if child skips early.
- When a subtest completes:
  - Apply `TestAttemptToFixPassed` and `TestHasFailedAllRetries` tags based on new metadata.
  - Emit skip reasons for disabled/quarantined subtests (with same message as parent).

### 6. Retry Orchestration Adjustments
- Allow parent and subtest wrappers to coexist:
  - Parent retries should continue to wrap the entire `testInfo.originalFunc`.
  - If a subtest is marked for retries but the parent is not, only the subtest wrapper should drive retries.
  - If both parent and subtest are marked, parent-level configuration takes precedence. Subtests inherit the parent’s disabled/quarantined/attempt-to-fix/retry state even when they have their own backend entry; guard to prevent nested retry loops.
    - Record retry ownership on `testExecutionMetadata` (e.g., `RetryOwnerIdentity`) so a subtest wrapper can detect when the parent already drives the retry loop.
    - Only allow the subtest controller to enter `runTestWithRetry` when it is the owner; otherwise short-circuit to tagging/skip logic while delegating retry counting to the parent.
    - Add a regression test where both parent and child request retries to ensure only one retry loop executes and counters stay consistent.
- Update `RemainingTotalRetryCount` handling:
  - Ensure decrements happen once per retried execution regardless of hierarchy.
  - Consider storing per-identity counters to avoid global exhaustion by numerous subtests; at minimum, guard against negative totals.
- Preserve attempt-to-fix logging format, prefixing indentation to remain readable under nested retries.

### 7. Tagging & Telemetry Consistency
- Ensure new subtest tags (`test.test_management.is_quarantined`, `...attempt_to_fix_passed`, `test.has_failed_all_retries`) mirror parent semantics.
- Emit telemetry exactly as today; no additional parent-vs-subtest differentiation is required by the backend.
- Confirm logs (via `collectAndWriteLogs`) continue to attach to the correct span even when retries re-run subtests multiple times; document any new metadata fields we rely on so instrumentation partners can audit changes.

### 8. Testing Strategy
- **Unit tests**:
  - Create a new `integrations/gotesting/subtests/testcontroller_test.go` to cover:
    - All the parent/subtest configuration permutations from the functionality matrix above.
    - In Flaky retry for subtests (ensure final status reflects retries, global counters adjust).
    - Parallel subtests with retry wrappers.
    - Instrumentation metadata propagation (e.g., new `IsSubtestWrapper` flag) so wrappers remain transparent to downstream consumers.
  - Don't modify `integrations/gotesting/testcontroller_test.go` or any other existing test files in the `gotesting` package; preserve current behaviour tests as-is.
  - Don't execute a single test inside the `gotesting` package, the tests to pass require every other test in the same package to be executed as well (due to the global state managed by the `civisibility` package).
  - Add targeted tests for `getTestManagementData` with hierarchical keys.
- **Integration tests**:
  - Simulate contexts where parent marks subtests as modified/unskippable and verify interplay.
  - Validate coverage flows when subtests undergo retries (ensure no panic on barrier restoration).
    - Force retries on subtests while Go coverage is enabled to confirm `coverageRecorder` snapshots and post-run merges stay balanced across attempts.
- **Regression tests**:
  - Re-run existing suites to ensure parent behaviour unchanged (e.g., attempt-to-fix on parent still logs identical messages).
  - Confirm telemetry-based tests (if any) still pass with new counters.
  - Revalidate Early Flake Detection flows (unit/integration) to confirm no behavioural drift since plan does not alter EFD logic.

### 9. Documentation & Rollout
- Update `CIVISIBILITY_OVERVIEW.md` with notes on subtest-level support.
- Document expected backend payload shape (hierarchical test names) for Test Management.
- Gate release behind a feature flag (env or config) if desired for gradual rollout; default to disabled until backend fully supplies subtest entries.
  - Introduce `CIVisibilitySubtestFeaturesEnabled` (mirrored by `DD_CIVISIBILITY_SUBTEST_FEATURES_ENABLED`) gating both management lookups and retry controller injection; default false and log activation state for visibility.
  - Add smoke tests to CI that exercise both flag states so regressions surface before rollout.
  - Verify that disabling the flag bypasses newly added metadata fields to keep legacy instrumentation outputs bit-for-bit identical.
  - Prepare configuration docs and release notes describing how to enable the flag, including guidance to toggle only between CI runs.
- Communicate with backend team regarding name canonicalization to avoid misalignment (`TestFoo/Subcase` vs `TestFoo::Subcase`, etc.).

### 10. Follow-Up & Monitoring
- Monitor telemetry for unexpected spikes in retry counts or skip rates post-release.
- Gather metrics on subtest retry success to validate backend configurations.
- Plan future enhancements (e.g., subtest-level impacted test detection) once groundwork proved stable.

---

## Resolved Considerations
- Backend already surfaces nested subtests using slash-delimited names; identity handling and lookup logic above accommodate arbitrary depth, so no additional normalization is required.
- Parent-level management directives remain authoritative (disable/quarantine/attempt-to-fix/retry). The controller merge helper enforces this and ensures emitted tags reflect the inherited state.
- No new public API is required for subtest-only unskippable flags; current ergonomics remain until new product requirements emerge.
- Telemetry continues aggregating parent and subtest data together; no schema changes are planned, only additional tests to ensure counters stay accurate.
- Beware of deadlocks in parallel subtests with retries; existing mutex logic is preserved, and new tests validate no contention arises.