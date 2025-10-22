# `internal/civisibility` Walkthrough

## High-Level Purpose
- Implements Datadog Test Optimization for Go: bootstraps tracing/log streaming, exposes manual test lifecycle APIs, and auto-instruments `testing`.
- Coordinates feature toggles such as Intelligent Test Runner (ITR), early flake detection, flaky retries, impacted tests, test management, subtest-level directives, code coverage, and CI logs.
- Normalizes CI/git metadata, fetches repository deltas, and communicates with backend settings/coverage/logs endpoints through a configurable client layer.
- Houses telemetry hooks to measure git command usage, HTTP behavior, and test instrumentation statistics.

## Top-Level Layout
- `civisibility.go` – atomic state/test-mode switches shared across integrations.
- `constants/` – tag keys, span types, environment variable names, capability flags, span metadata helpers.
- `integrations/` – tracer bootstrapping, feature negotiation, manual test lifecycle API, Go `testing` instrumentation (including subtest orchestration), log streaming.
- `integrations/gotesting/subtests/` – mock backend + scenario harness exercising parent/subtest directive matrices and retry ownership rules.
- `utils/` – CI provider discovery, git utilities, code owners lookup, network clients, telemetry plumbing, name canonicalization, impacted test logic, fixtures.

## Core Components

### Root State Management
`civisibility.go` stores a process-wide `status` (`StateUninitialized` through `StateExited`) and a `isTestMode` flag using `atomic` types. Integrations set these to coordinate tracer startup/teardown, and tests can toggle “test mode” for mock tracer usage.

### Constants Package
- `ci.go`, `env.go`, `git.go`, `os.go`, `runtime.go` declare string keys for CI metadata, git fields, OS/runtime descriptors, and env var names driving feature toggles (`DD_CIVISIBILITY_*`, `CIVisibility*`).
- `tags.go`, `test_tags.go`, `span_types.go` centralize span tag names, capability markers, and status/type enumerations used by integrations and utils.
- `test_tags.go` captures nuanced flags (`test.itr.forced_run`, quarantine/disable toggles, retry reasons, coverage toggles) ensuring consistent tagging between retries, EFD, ITR, and test management flows.

### Integrations Package
- `civisibility.go` handles one-time tracer initialization: sets `DD_CIVISIBILITY_ENABLED`, forces tracing sample rate to 1, preloads CI metadata/code owners, optionally sets service name from repo URL, and registers signal handlers that call `ExitCiVisibility`. Close actions queue via `PushCiVisibilityCloseAction`, running in LIFO order during shutdown.
- `civisibility_features.go` orchestrates backend settings negotiation. It spawns asynchronous git pack uploads, retries settings fetch if backend needs git data, applies env-var kill switches for features (flaky retries, impacted tests, test management, subtest directives), and lazily loads supplementary data (known tests, skippables, impacted tests analyzer). Settings and HTTP client live in package-level vars protected by `sync.Once`.
- `manual_api*.go` files expose strongly-typed interfaces for user-driven test lifecycle: `TestSession`, `TestModule`, `TestSuite`, `Test`, along with option structs for command, framework, working directory, start/finish timestamps, and error reporting via `ErrorOption`. Variants (`manual_api_ddtest*.go`) adapt to `ddtest` helper semantics, and `manual_api_mocktracer_test.go` validates API behavior under mock tracer mode.
- `gotesting/` auto-instruments `testing.M`, `testing.T`, and `testing.B`.
  - `testing.go`, `testingT.go`, `testingB.go`, and related files manage session/module/suite creation, attach tags (including module/suite counters), handle chatty output, skip logic, coverage capture, log streaming, and telemetry emission. They integrate with `integrations.GetSettings()`, `net` clients, and `logs`.
  - Hierarchical identity plumbing: `testIdentity` (module, suite, base name, full name, path segments) plus `matchTestManagementData` allow subtests to resolve directives like `TestParent/SubChild`. `getTestManagementData` reports whether a directive was an exact match or inherited from an ancestor.
  - `instrumentation.go` wires wrappers around test functions, stores execution metadata (retries, new/modified flags, quarantined/disabled states, attempt-to-fix ownership), and coordinates with backend settings for retries/EFD/ITR. It leverages `unsafe` pointers and reflective lookup to map `testing` internals, guarded by `sync` primitives. Subtests consult the parent execution metadata to decide whether they should wrap themselves or defer to the parent-run retry loop.
  - Attempt-to-fix ownership rules:  
    1. Parent-only directives orchestrate retries and tag success/failure; children inherit attempt-to-fix tagging but emit no retry spans.  
    2. Child-only directives wrap the subtest locally (when feature flag enabled and exact match present) while leaving the parent neutral.  
    3. Parent and child requesting attempt-to-fix results in the parent winning; subtests receive tags but do not run retries.  
    4. Parent quarantine + attempt-to-fix keeps the parent as the retry owner while subtests inherit quarantine tags; a quarantined parent without attempt-to-fix leaves children free to execute their own retries if explicitly configured.
  - Feature gating: `DD_CIVISIBILITY_SUBTEST_FEATURES_ENABLED` enables subtest directives. `RUN_SUBTEST_CONTROLLER` forces wrappers to short-circuit for harness-driven scenarios; standard debug logs capture identity/ownership traces when enabled.
  - `instrumentation_orchestrion.go` and `orchestrion.yml` support bytecode rewriting via Orchestrion for transparent instrumentation in user code. The orchestrion path computes subtest identities, inspects parent metadata, and applies the same ownership logic as the manual wrappers.
  - `coverage/` builds code coverage payloads, writes them via `coverage_writer`, and includes an auto-generated `test_coverage_msgp.go` for MsgPack encoding.
  - `reflections.go` / `_test.go` ensure compatibility with `go test` internal structures across versions; helper routines detect struct offsets, function pointers, and maintain compatibility with new Go releases.
- `logs/` encapsulates CI log forwarding: gating via `DD_CIVISIBILITY_LOGS_ENABLED` stable config, packaging log entries with consistent tags, buffering/flush policies, payload formatting (`logs_payload.go`) and writer lifecycle (`logs_writer.go`).

### Subtest Scenario Harness (`integrations/gotesting/subtests/`)
- Provides an executable matrix covering parent/subtest directive permutations. `main_test.go` enables the subtest feature flag and spawns subprocesses per scenario using `SUBTEST_MATRIX_SCENARIO`.
- `subtestcontroller_test.go` spins up a mock backend (`startSubtestServer`) that surfaces settings, test-management payloads, and stubbed endpoints for logs/git to keep tests hermetic.
- Scenarios assert span counts, `test.status`, `test.is_quarantined`, `test.is_disabled`, retry metadata (`test.is_retry`, `test.retry_reason`), and ownership of attempt-to-fix success tags. Parallel subtests and custom retry budgets are part of the matrix.
- Utilities (`scenarioContext`, `setParentDirective`, `setSubDirective`) help craft hierarchical directives; helper assertions document expected tagging for collaborators extending the matrix.

### Utils Package
- `ci_providers.go` detects CI metadata across numerous providers (AppVeyor, Azure Pipelines, GitHub Actions, Jenkins, etc.), normalizes refs/URLs, removes secrets, supports user overrides through `DD_GIT_*` env vars, and logs detected provider. Fixtures under `testdata/fixtures/{providers}` supply provider-specific JSON.
- `environmentTags.go` maintains cached CI tags/metrics with thread-safe mutation (`AddCITags*`, `ResetCITags*`), expands `~`, computes relative paths, and augments CPU metrics (logical cores).
- `git.go` performs git command execution with telemetry instrumentation, synchronized access (`gitCommandMutex`), shallow clone detection/unshallow, pack-file generation (`MaxPackFileSizeInMb`, `CreatePackfiles`), base branch discovery, and sensitive info filtering. Interacts with `utils/telemetry` enums to classify commands/errors. Backed by tests covering command paths and error handling.
- `file_environmental_data.go` and `_test.go` collect file-level metadata (size, permissions, hash) referenced by impacted tests. `filebitmap/` stores efficient bitmap representation of file coverage.
- `impactedtests/` implements incremental test selection. `algorithm.md` documents the base branch detection heuristic (with 2025 updates) and ties closely to git utilities; `impacted_tests.go` consumes backend responses to track new/modified tests.
- `codeowners.go` parses CODEOWNERS files with caching and fallback to repo root; fixtures for GitHub/GitLab located under `testdata/fixtures/codeowners`.
- `names.go` normalizes module/suite names via runtime function lookup and heuristics, ensuring consistent tagging even with nested/subtests; tests validate complex name resolution.
- `home.go` and `file_environmental_data.go` handle home directory discovery with consideration for CI sandboxes and Windows drive letters.
- `net/` houses HTTP client logic:
  - `client.go` builds agent or agentless clients, selects base URL/subdomain, attaches tags/headers, and exposes methods for settings, known tests, pack files, coverage, logs, skippables, and test management APIs. Incorporates retry/backoff (`math/rand/v2` jitter), compression awareness, telemetry hooks, and optional EVP proxy over Unix sockets.
  - `http.go`, `coverage.go`, `logs_api.go`, `settings_api.go`, etc., serialize network payloads, set proper endpoints, compress payloads, and capture request/response telemetry (status codes, compression flags, payload sizes).
  - `skippable.go`, `known_tests_api.go`, `test_management_tests_api.go` parse backend responses into typed structs for downstream integrations.
- `telemetry/` defines dimensional labels for events (framework identifiers, CI provider tags, error types, git command categories) used throughout the package to emit consistent metrics.
- `names_test.go`, `git_test.go`, `codeowners_test.go`, `ci_providers_test.go`, `net/*_test.go`, etc., provide extensive coverage, often using fixtures to simulate CI environments and network responses.

## Testing, Fixtures, and Tooling
- Extensive `_test.go` coverage in integrations (`manual_api`, `gotesting`, `logs`) and utils ensures feature toggles, retries, coverage serialization, and network clients behave as expected.
- Subtest matrix harness (`integrations/gotesting/subtests`) runs under `go test` and exercises the parent/subtest permutations needed to guard subtest-specific instrumentation changes. Enable debug logging to surface per-scenario diagnostics.
- `integrations/gotesting/testcontroller_test.go` retains historical scenarios for flaky retries, EFD, ITR, and impacted tests; it coexists with the new subtest harness to avoid regression gaps.
- `utils/testdata/fixtures/providers/*.json` mimics CI payloads; `github-event.json` supports webhook parsing tests.
- Generated assets: `coverage/test_coverage_msgp.go` (MsgPack via `go:generate`), with tests to ensure deterministic encoding.
- `integrations/gotesting/reflections_test.go` safeguards reflection-based hooks against Go runtime changes.
- Mock tracer support via `mocktracer` allows unit tests to assert spans without real agent connectivity.

## Notable Nuances & Design Choices
- Heavy use of `sync.Once`, `atomic`, and mutexes to guard global state, ensuring idempotent initialization even under concurrent instrumentation hooks.
- Feature toggles honor both backend settings and local env overrides, often logging when overrides disable capabilities to aid troubleshooting. Subtest features default off unless an env var enables them, allowing gradual rollout.
- Subtest wrappers strictly require an exact directive match before wrapping to avoid unnecessary allocations when hierarchy lookups fall back to parent configuration.
- Git operations are serialized to avoid repository lock contention, and telemetry logs command timings plus categorized exit codes to monitor flaky git environments.
- Instrumentation leans on `unsafe.Pointer` and reflection to interpose on testing internals, a delicate strategy mitigated by fallback logic and version checks. Helper utilities (`reflections.go`) centralize offsets so new Go releases require updates in a single place.
- Coverage and impacted test features rely on asynchronous git uploads; close actions ensure goroutines finish before process exit.
- Network layer supports agentless uploads with API key validation and on-the-fly compression, while also accommodating Datadog agent EVP proxy over HTTP or Unix sockets.
- `orchestrion.yml` indicates support for compile-time rewriting, hinting at hybrid instrumentation strategies (manual wrappers plus bytecode injection).
- Logging pipeline mirrors test span IDs and includes service/host tags, but is guarded behind stable-config flag to avoid unexpected log emission.

## Getting Involved
- When touching `integrations/gotesting`, run both the legacy controller suite and the subtest matrix (`go test ./internal/civisibility/integrations/gotesting/...`). Many scenarios spawn subprocesses; enable debug logging for verbose traces.
- Any change to retry ownership or metadata propagation should be mirrored in the harness scenarios and in `docs/SUBTEST_FEATURE_IMPLEMENTATION.md` to keep documentation synchronized.
- Utility changes often require updating fixtures or provider expectations; leverage the existing test suites instead of ad-hoc scripts.
