# Deferred Process Retry Baseline Comparison

Date: 2026-08-02

## Executive summary

This report compares the existing inline process-retry scheduler with the
experimental deferred process-retry scheduler.

- **Control:** `tony/feat/process-isolated-test-retries` at
  `304023edaacd5c61c47675633894daa3aa8a95dd`.
- **Experiment implementation:** `tony/experiment/deferred-process-retries` at
  `077d1590f3466e1913e9d5c9b7e961c8aa4e5ed5`, measured with the benchmark
  harness included alongside this report.
- **Primary result:** in every retrying multi-package scenario, the experiment
  started zero retry subprocesses before that package completed its native
  first pass. The control started every eligible retry inline.
- **Scheduler result:** with package parallelism available, first-pass
  completion typically improved by 5% to 50%.
- **End-to-end result:** total runtime was usually within a few percent of the
  control. Sequential EFD improved materially; several already-parallel paths
  were neutral or slightly slower.
- **Important limit:** with `go test -p=1`, deferring work cannot overlap it
  with another package's first pass. In the measured startup workload, first
  pass was 1.8% slower and total runtime was 6.7% slower.
- **No-retry result:** paired multi-package measurements were neutral: 0.38%
  faster first pass and 0.33% faster total runtime. FTR enabled with an initial
  pass was 0.12%/0.24% slower, also effectively neutral.
- **Correctness:** exact attempt counts, PIDs, retry reasons, feature API
  requests, coverage behavior, failure paths, and Orchestrion ownership passed
  the focused validation described below.

The experiment therefore improves the intended scheduler property, but it is
not a universal wall-clock optimization. Its strongest case is a test command
with multiple packages or other native work that can proceed while retries are
deferred.

## What is being compared

The control runs a retry as part of the instrumented top-level test before that
test returns to the native `testing` scheduler. The experiment records the
first-attempt result, returns control to Go, and drains admitted process retries
from the `testing.M` owner after the native first-pass phase.

Both sides use the same process retry implementation, test binary, retry count,
fake CI Visibility responses, generated package sources, workload profile, and
subprocess containment. The changed variable is when the parent schedules the
retry group.

## Environment

| Item | Value |
| --- | --- |
| Host | Apple M2 Max, 12 logical CPUs |
| OS | macOS 26.5.1, Darwin arm64 |
| Go | `go1.26.5` |
| Generated packages | 1, 2, 4, or 8 |
| Default package concurrency | 8 |
| Default process concurrency | runtime-derived, unless explicitly set |
| Samples | 3 paired samples per reported performance cell |
| Resource samples | 1 diagnostic sample, 50 ms polling |

## Methodology

The benchmark creates a temporary module with up to eight independent Go test
packages. Each package has:

- one top-level test controlled by FTR, EFD, A2F, Test Management, or ITR;
- one first-pass sentinel that must never execute in a retry child;
- a custom `TestMain` using `gotesting.RunM`;
- event reporting for parent start/finish, first-pass completion, child
  start/finish, PID, and private retry reason; and
- a fake agentless intake serving settings, known tests, Test Management, and
  skippable-tests responses.

Each performance sample runs the experiment and control in alternating order.
The reported values are medians of three samples:

- **First pass:** time until every selected package reports its native
  first-pass sentinel.
- **Drain:** time between the last first-pass sentinel and `go test` exit.
- **Total:** complete `go test` wall time.
- **Early retries:** retry children started before their own package's
  first-pass sentinel.

Positive percentages mean the experiment is faster. Negative percentages mean
it is slower.

Correctness does not depend on elapsed-time assertions. Tests validate exact
event counts and ordering, distinct process identities, retry reasons, feature
requests, bounded concurrency, expected exit disposition, and child completion.
Sleeps in benchmark profiles model work only; they are not pass/fail clocks.

## Retry family comparison

All rows use eight packages and `-p=8`. Startup-oriented retry rows add 250 ms
of child startup work. `A2F setting=3` means three total executions: one initial
execution and two retries. EFD and FTR settings count additional retries.

| Scenario | Retries | First pass control / experiment (ms) | First-pass change | Total control / experiment (ms) | Total change | Early retries control / experiment |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| FTR, initial pass | 0 | 4218 / 4224 | -0.12% | 4264 / 4275 | -0.24% | 0 / 0 |
| FTR, fail then pass | 8 | 4516 / 4252 | +5.84% | 4570 / 4610 | -0.88% | 8 / 0 |
| FTR, persistent failure | 40 | 5970 / 4489 | +24.81% | 6232 / 6155 | +1.23% | 40 / 0 |
| FTR, budget limited to 2 per package process | 16 | 5021 / 4454 | +11.29% | 5120 / 5146 | -0.51% | 16 / 0 |
| EFD, sequential, all pass | 80 | 8546 / 4266 | +50.08% | 8592 / 7589 | +11.67% | 80 / 0 |
| EFD, parallel, all pass | 80 | 5208 / 4302 | +17.40% | 5259 / 5300 | -0.79% | 80 / 0 |
| EFD, parallel, persistent failure | 80 | 5141 / 4237 | +17.57% | 5189 / 5235 | -0.88% | 80 / 0 |
| A2F setting=3, all pass | 16 | 4876 / 4295 | +11.93% | 4927 / 4968 | -0.83% | 16 / 0 |
| A2F setting=3, persistent failure | 16 | 5131 / 4713 | +8.14% | 5212 / 5397 | -3.54% | 16 / 0 |
| A2F + EFD + FTR precedence | 16 | 5117 / 4527 | +11.52% | 5162 / 5199 | -0.71% | 16 / 0 |
| A2F + disabled | 16 | 5108 / 4301 | +15.81% | 5155 / 4977 | +3.46% | 16 / 0 |
| A2F + quarantined | 16 | 6025 / 5013 | +16.79% | 6225 / 5728 | +7.99% | 16 / 0 |
| Test Management disabled | 0 | 4308 / 4435 | -2.94% | 4358 / 4501 | -3.29% | 0 / 0 |
| Test Management quarantined | 0 | 4415 / 4281 | +3.03% | 4464 / 4336 | +2.88% | 0 / 0 |
| ITR forced-run + FTR fail then pass | 8 | 4737 / 4473 | +5.58% | 4814 / 4833 | -0.39% | 8 / 0 |
| Coverage + parallel EFD | 16 | 4836 / 4537 | +6.19% | 4883 / 4899 | -0.33% | 16 / 0 |

The overlap row also validates that every child receives
`retry_reason=attempt_to_fix`; matching attempt counts alone are not accepted as
proof of precedence.

The FTR budget is session-local. Because each generated package is a separate
`go test` binary and CI Visibility session, a budget of two permits two retries
per package process, not two retries across the entire multi-package command.

## EFD retry-count scaling

These rows use eight packages, startup-oriented retries, parallel EFD, and the
default package concurrency.

| EFD retries per package | First pass control / experiment (ms) | First-pass change | Total control / experiment (ms) | Total change | Early retries control / experiment |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 0 | 4324 / 4308 | +0.38% | 4378 / 4364 | +0.33% | 0 / 0 |
| 2 | 4576 / 4339 | +5.19% | 4622 / 4698 | -1.64% | 16 / 0 |
| 5 | 4852 / 4247 | +12.48% | 4906 / 4973 | -1.38% | 40 / 0 |
| 10 | 5173 / 4305 | +16.78% | 5222 / 5306 | -1.62% | 80 / 0 |

An independent three-sample repeat of the 10-retry row measured +13.28% first
pass and -3.47% total. The conclusion is stable for first-pass scheduling, but
the exact end-to-end percentage has several points of host noise.

## Package-count scaling

These rows use 10 parallel EFD retries and package concurrency equal to the
number of packages.

| Packages | First pass control / experiment (ms) | First-pass change | Total control / experiment (ms) | Total change |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 2966 / 2028 | +31.64% | 3032 / 3117 | -2.81% |
| 2 | 3144 / 2166 | +31.09% | 3331 / 3192 | +4.16% |
| 4 | 3914 / 2920 | +25.39% | 3965 / 3910 | +1.38% |
| 8 | 5674 / 4921 | +13.28% | 5727 / 5926 | -3.47% |

One package still shows a large first-pass metric improvement because the
sentinel runs before the deferred drain, but there is no other package work to
overlap. The extra drain makes total time slightly worse. This is expected from
the architecture and is why first-pass and total must both be reported.

## Native package scheduler sensitivity

These rows use eight packages and 10 parallel EFD retries.

| `go test -p` | First pass control / experiment (ms) | First-pass change | Total control / experiment (ms) | Total change |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 16572 / 16865 | -1.77% | 16753 / 17869 | -6.66% |
| 2 | 10000 / 9286 | +7.14% | 10047 / 10289 | -2.41% |
| 4 | 5732 / 4684 | +18.27% | 5787 / 5718 | +1.19% |
| 8 | 5674 / 4921 | +13.28% | 5727 / 5926 | -3.47% |

The experiment should not be described as a throughput improvement for
strictly serial package execution. Its purpose there is ordering and isolation,
and that comes with measurable drain overhead.

## Process-concurrency sensitivity

The process limit is per test binary. With eight package binaries, a limit of
one still permits up to eight concurrent children globally.

| Per-package process concurrency | First pass control / experiment (ms) | First-pass change | Total control / experiment (ms) | Total change | Max children control / experiment |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 7799 / 4490 | +42.42% | 7848 / 7724 | +1.57% | 8 / 8 |
| 2 | 5813 / 4261 | +26.70% | 5866 / 5905 | -0.67% | 10 / 11 |
| runtime-derived | 5674 / 4921 | +13.28% | 5727 / 5926 | -3.47% | 12 / 10 |

No measured row exceeded its configured per-package bound or the derived
global bound.

## Workload profile

All rows use eight packages and 10 parallel EFD retries.

| Profile | First pass control / experiment (ms) | First-pass change | Total control / experiment (ms) | Total change |
| --- | ---: | ---: | ---: | ---: |
| Child startup, 250 ms | 5674 / 4921 | +13.28% | 5727 / 5926 | -3.47% |
| Test body, 250 ms | 5470 / 4550 | +16.83% | 5517 / 5534 | -0.32% |
| CPU, 250M integer iterations | 6699 / 4947 | +26.15% | 6746 / 6578 | +2.49% |

CPU work benefits when the host has capacity for the deferred burst. These
figures are not a promise for constrained CI workers; the `-p=1` result shows
the opposite boundary.

## In-process and no-retry microbenchmarks

These existing benchmarks execute the two worktrees in separate commands, so
they are more sensitive to command order, CPU state, and build cache than the
paired multi-package benchmark. The table reports medians of three independent
benchmark invocations, each with five operations.

| Case | Control | Experiment | Difference |
| --- | ---: | ---: | ---: |
| One `in_process` retry | 701.6 ms | 653.7 ms | +6.83% |
| One inline `process` retry | 691.6 ms | 659.5 ms | +4.64% |
| 100 passing tests, CI Visibility only | 7.056 ms/test | 6.592 ms/test | +6.57% |
| 100 passing tests, `in_process` retry-capable | 7.081 ms/test | 6.647 ms/test | +6.13% |
| 100 passing tests, `process` retry-capable | 6.854 ms/test | 6.611 ms/test | +3.55% |

The broad 3% to 7% shift across even the unchanged CI-Visibility-only case
shows command-level bias. These values demonstrate no regression, but they are
not used to claim a causal speedup. The paired no-retry result (+0.33% total) is
the stronger estimate of scheduler overhead.

## In-process EFD comparison

The existing EFD benchmark was also run for the `in_process` backend in both
trees. Each row has one initial execution and the configured number of retries.
The `parallel` switch does not create subprocesses or parallelize fresh
in-process attempts; both trees reported zero child processes.

The body-dominated profile is the useful comparison because its 250 ms body
work outweighs controller process startup:

| Body workload | Control / experiment (ms) | Change |
| --- | ---: | ---: |
| 2 retries, sequential | 1137 / 1125 | +1.06% |
| 2 retries, parallel requested | 1140 / 1146 | -0.53% |
| 5 retries, sequential | 1915 / 1911 | +0.21% |
| 5 retries, parallel requested | 1877 / 1895 | -0.96% |
| 10 retries, sequential | 3128 / 3144 | -0.51% |
| 10 retries, parallel requested | 3117 / 3173 | -1.80% |

All body-dominated rows are within 2%. The startup-oriented in-process rows use
only a 10 ms body because the configured 250 ms delay belongs to process-child
startup and is therefore intentionally absent. Their separate-command results
ranged from +5.4% to -8.0% around a 0.6-second controller floor and are treated
as noise, not an in-process regression or improvement.

## Resource diagnostics

Resource sampling is intentionally separate from timing because invoking `ps`
every 50 ms perturbs the workload. One sample was collected for two high-child
scenarios.

| Scenario | Peak RSS control / experiment | Peak CPU control / experiment | Total change |
| --- | ---: | ---: | ---: |
| EFD, 8 packages x 10 retries | 2577 / 2678 MiB | 488% / 417% | -1.88% |
| FTR persistent failure, 8 x 5 | 2921 / 2854 MiB | 423% / 421% | -4.42% |

The single samples do not show a consistent RSS or CPU regression. They are
diagnostic only, not statistically strong enough for an acceptance threshold.

## Correctness-only comparison

Some paths are not meaningful throughput benchmarks. They were compared by
running the same focused fixtures on control and experiment and asserting the
same observable contract.

| Area | Control | Experiment | Notes |
| --- | --- | --- | --- |
| Process exit | Pass | Pass | Failed parent-owned attempt |
| Malformed result | Pass | Pass | Strict result rejection |
| Process timeout | Pass | Pass | Bounded terminate/kill/reap |
| Output-drain timeout | Pass | Pass | Containment-loss handling |
| Descendant cleanup | Pass | Pass | Process tree is reaped |
| Private transport isolation | Pass | Pass | Child markers scrubbed |
| Startup rerun/conflict | Pass | Pass | Package init/TestMain behavior |
| A2F ownership | Pass | Pass | Parent-owned retry spans |
| Parallel EFD | Pass | Pass | Exact process attempt count |
| A2F failfast | Not present in control | Pass | Aggregate deferred failfast |
| Coverage first attempt | Pass | Pass | `go test -cover`, 100% fixture coverage |
| Orchestrion retryprocess suite | Pass | Pass | Ownership and process-child paths |

Orchestrion wall times were not compared. The experiment ran before the
control and paid a much larger compile/tool cache cost (63 s versus 24 s), while
the actual nested package test times were 4.45 s and 4.06 s. That topology is a
correctness gate, not a stable performance benchmark.

The deterministic family regression additionally covers:

- FTR initial pass, fail-to-pass, persistent failure, and budget limiting;
- sequential and parallel EFD, pass and persistent failure;
- A2F pass/failure and precedence over EFD/FTR;
- disabled/quarantined tests, with and without A2F;
- ITR forced-run plus FTR; and
- coverage plus parallel EFD.

## Cases intentionally not timed as throughput

- Panic, `runtime.Goexit`, cancellation, shutdown, malformed transport,
  containment loss, and unreaped children are terminal correctness paths.
- Unsupported layouts, fuzzing, selected subtest identities, `shuffle=on`, and
  unsupported process containment are eligibility/fallback contracts.
- Slow EFD's five-minute threshold is covered by retry-count policy tests. A
  real-time benchmark would require at least five minutes per first attempt and
  would not add scheduler information beyond the duration-selection test.
- Manual and Orchestrion ownership use different setup mechanisms but converge
  on the same retry scheduler. They are retained as integration proofs rather
  than duplicated performance cells.

## Conclusions

1. **The experiment achieves its primary architectural goal.** No retry child
   starts before its package completes the native first pass.
2. **The scheduler benefit scales with available independent work.** First-pass
   completion improves strongly with multiple packages, bounded sequential
   retries, or CPU work.
3. **Total runtime is not uniformly better.** Most parallel cases are within a
   few percent; EFD sequential improves, while `-p=1` regresses materially.
4. **The no-retry path is neutral in the strongest paired measurement.** There
   is no evidence that merely enabling retry features imposes a meaningful new
   cost when no retry is admitted.
5. **The deferred drain is the remaining optimization target.** Reducing
   process startup/finalization overhead or improving drain scheduling would
   help the slightly negative total-runtime rows without sacrificing native
   first-pass scheduling.
6. **The experiment should be accepted for scheduling transparency, not sold as
   a universal speedup.** The report supports a performance claim only when
   package/native parallelism is available.

## Reproduction commands

Run from the experimental worktree and set the frozen control root:

```sh
export PROCESS_RETRY_BURST_BASELINE_ROOT=/Users/tony.redondo/repos/github/Datadog/dd-trace-go

go test ./internal/civisibility/integrations/gotesting/retryprocess \
  -run '^TestProcessRetryBurstFamilyScenarios$' -count=1

go test ./internal/civisibility/integrations/gotesting/retryprocess \
  -run '^$' -bench '^BenchmarkProcessRetryMultiPackageBurst/families' \
  -benchtime=3x -count=1

go test ./internal/civisibility/integrations/gotesting/retryprocess \
  -run '^$' -bench '^BenchmarkProcessRetryMultiPackageBurst/scale' \
  -benchtime=3x -count=1

go test ./internal/civisibility/integrations/gotesting/retryprocess \
  -run '^$' -bench '^BenchmarkProcessRetryMultiPackageBurst/packages=8/go-test-p' \
  -benchtime=3x -count=1

go test ./internal/civisibility/integrations/gotesting/retryprocess \
  -run '^$' -bench '^BenchmarkProcessRetryMultiPackageBurst/packages=8/process-concurrency' \
  -benchtime=3x -count=1

go test ./internal/civisibility/integrations/gotesting/retryprocess \
  -run '^$' -bench '^BenchmarkProcessRetryMultiPackageBurst/packages=8/profile' \
  -benchtime=3x -count=1
```

Run the existing single-package microbenchmarks separately in each worktree:

```sh
go test ./internal/civisibility/integrations/gotesting/retryprocess \
  -run '^$' \
  -bench '^(BenchmarkProcessRetryExecutionMode|BenchmarkProcessRetryNoRetryHotPath)$' \
  -benchtime=5x -count=3
```

## Limitations

- Local results cover Darwin arm64 and Go 1.26.5 only. Linux, Windows, Go 1.25,
  and Go 1.26 CI remain required before production acceptance.
- Process startup, filesystem, and scheduler costs vary substantially by CI
  runner and security tooling.
- Three paired samples are sufficient to identify the scheduling effect but
  not sub-percent wall-time changes.
- The resource sampler is a single diagnostic observation and should not be
  used as an allocation benchmark.
- The generated benchmark uses one CI Visibility session per package, matching
  `go test` package binaries. It does not model a repository-wide shared FTR
  budget because the library does not own one across independent binaries.
