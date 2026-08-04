# Retry Execution Mode Performance Comparison

Date: 2026-08-04

## Summary

This report compares the current `in_process` retry backend with the deferred
`process` backend. The runtime under measurement is commit
`d732aafbc03e66ac50b98b63620d59b55d68314b`; only the benchmark and test harness
were changed to run identical scenarios through both backends.

The result is workload-dependent:

- With no admitted retries, the modes are effectively equivalent. The focused
  hot-path benchmark measured about 6.6 ms per passing test for both modes.
- For short test bodies, process creation dominates. FTR fail-to-pass was about
  4% slower and persistent FTR with five retries was about 56% slower in the
  eight-package startup profile.
- Parallel EFD becomes faster when each retry does useful work. With a 250 ms
  body, five retries were about 43% faster and ten retries about 56% faster in a
  single package when process concurrency was high enough.
- CPU-bound retries need spare CPU capacity. Process-parallel EFD was 9% slower
  with `GOMAXPROCS=1`, 24% faster with `GOMAXPROCS=2`, and 11% faster with
  `GOMAXPROCS=4`.
- Native package concurrency matters. In the eight-package startup profile,
  process mode was about 42% slower with `go test -p=1` and 12% slower with
  `-p=8`.
- Deferred process retries preserve the scheduler goal: no process retry began
  before its package reported completion of its native first pass. In-process
  retries remained inline and therefore completed before that sentinel.

Process mode should therefore be selected for isolation and for workloads that
can exploit retry parallelism. It is not a universal latency optimization.

## Method

The multi-package benchmark creates eight independent Go test packages. Each
package uses `gotesting.RunM`, one controlled top-level test, a first-pass
sentinel, and a fake CI Visibility intake. Every scenario is run once with
`DD_CIVISIBILITY_RETRY_EXECUTION_MODE=in_process` and once with `process`.
Paired samples alternate execution order.

Correctness is independent of elapsed time. Before timings are accepted, the
harness validates exact retry counts, retry reasons, parent and child process
ownership, feature requests, expected exit status, coverage behavior, and
process concurrency. Timings are reported only as benchmark metrics.

Environment:

| Item | Value |
| --- | --- |
| Host | Apple M2 Max, 12 logical CPUs |
| OS | macOS 26.5.1, Darwin arm64 |
| Go | `go1.26.5` |
| Runtime commit | `d732aafbc03e66ac50b98b63620d59b55d68314b` |
| Multi-package samples | 3 paired samples per main cell |
| Hot-path samples | 5 or 7 paired samples; 3 x 10 operations for microbenchmarks |
| EFD and CPU samples | 3 operations per cell |
| Resource samples | 1 diagnostic operation per profile |

The synthetic profiles isolate different costs:

- `startup`: 250 ms of retry-child startup work and negligible test-body work;
- `body`: 250 ms in every test execution and negligible child startup work;
- `cpu`: 250 million integer iterations in every execution.

The startup delay intentionally applies only to process children. It models
package initialization, runtime setup, and `TestMain` work that does not exist
for an in-process retry.

## No-Retry Path

The focused 100-test microbenchmark produced these median `RunM` costs:

| Configuration | Time per passing test | Retry children |
| --- | ---: | ---: |
| CI Visibility, no retry feature | 6.57 ms | 0 |
| Retry-capable `in_process` | 6.64 ms | 0 |
| Retry-capable `process` | 6.52 ms | 0 |

The differences are below the command-level noise observed across repeated
runs. In the paired multi-package checks, FTR enabled with an initial pass was
also neutral: 4.352 s in-process versus 4.319 s process, with a paired median
change of -0.17% for process. Enabling EFD with zero retries likewise produced
zero children and a paired median total change of +0.04% in the repeat.

Conclusion: selecting process mode does not create a subprocess unless a retry
is admitted, and no reliable no-retry regression was measured.

## Retry Families

These rows use eight packages and `go test -p=8`. Positive change means process
is faster; negative change means process is slower. Startup-oriented retry rows
model 250 ms of child startup work.

| Scenario | Retries | In-process total | Process total | Process change |
| --- | ---: | ---: | ---: | ---: |
| ATR/FTR, initial pass | 0 | 4.302 s | 4.321 s | -0.4% |
| ATR/FTR, fail then pass | 8 | 4.468 s | 4.666 s | -4.4% |
| ATR/FTR, persistent failure | 40 | 4.511 s | 7.017 s | -55.6% |
| ATR/FTR, budget limited | 16 | 4.990 s | 4.999 s | -0.2% |
| EFD, sequential, pass | 80 | 4.938 s | 7.684 s | -55.6% |
| EFD, parallel, pass | 80 | 4.409 s | 5.451 s | -23.6% |
| EFD, parallel, persistent failure | 80 | 5.212 s | 6.517 s | -25.0% |
| A2F, all pass | 16 | 4.688 s | 6.741 s | -43.8% |
| A2F, persistent failure | 16 | 4.348 s | 5.416 s | -24.6% |
| A2F + EFD + FTR precedence | 16 | 6.072 s | 5.669 s | +6.6% |
| A2F + disabled | 16 | 4.397 s | 6.173 s | -40.4% |
| A2F + quarantined | 16 | 4.290 s | 4.958 s | -15.6% |
| ITR forced + FTR fail then pass | 8 | 4.418 s | 4.676 s | -5.8% |
| Coverage + parallel EFD | 16 | 4.997 s | 4.802 s | +3.9% |

Disabled and quarantined cases without A2F admitted no retries. Their observed
mode differences varied materially between repetitions and are treated as
process-launch noise rather than a backend effect.

The family table is deliberately startup-heavy. It shows the isolation tax,
not the throughput boundary where process parallelism becomes beneficial.

## EFD: Startup-Dominated Work

Single-package results below report `RunM` time. In-process attempts stay
serial even when parallel EFD is requested.

| Retries | In-process | Process sequential | Process parallel, concurrency 2 | Process parallel, concurrency 8 |
| ---: | ---: | ---: | ---: | ---: |
| 2 | 0.69 s | 1.05 s | 0.72 s | 0.73 s |
| 5 | 0.71 s | 2.32 s | 1.46 s | 0.87 s |
| 10 | 0.68 s | 3.91 s | 2.07 s | 1.04 s |

For almost-empty retries, process mode cannot beat in-process execution. Higher
concurrency limits the penalty but cannot eliminate process startup.

## EFD: Body-Dominated Work

Each execution performs a 250 ms body. This is where parallel retries amortize
process startup.

| Retries | In-process | Process sequential | Process parallel, concurrency 2 | Process parallel, concurrency 8 |
| ---: | ---: | ---: | ---: | ---: |
| 2 | 1.10 s | 1.37 s | 1.01 s | 1.11 s |
| 5 | 1.87 s | 2.59 s | 1.63 s | 1.06 s |
| 10 | 3.11 s | 4.02 s | 2.28 s | 1.37 s |

With five retries and concurrency eight, process mode is about 43% faster. With
ten retries it is about 56% faster. Process-sequential remains slower because it
adds process startup without parallel overlap.

## CPU Capacity

This benchmark uses four CPU-heavy EFD retries in one package.

| `GOMAXPROCS` | In-process | Process sequential | Process parallel | Parallel process change |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 3.462 s | 4.413 s | 3.761 s | -8.6% |
| 2 | 1.925 s | 2.617 s | 1.469 s | +23.7% |
| 4 | 1.177 s | 1.447 s | 1.046 s | +11.1% |

Process concurrency must not be interpreted as free CPU. On a saturated one-CPU
runner it adds overhead. It wins when the host has capacity for concurrent
retry work.

## Package Scheduler and Process Concurrency

These rows use eight packages, five parallel EFD retries, and the startup
profile.

| `go test -p` | In-process total | Process total | Process change |
| ---: | ---: | ---: | ---: |
| 1 | 10.362 s | 14.734 s | -42.2% |
| 2 | 5.081 s | 7.292 s | -43.5% |
| 4 | 4.169 s | 5.183 s | -24.4% |
| 8 | 4.497 s | 5.057 s | -12.5% |

| Per-package process concurrency | In-process total | Process total | Process change |
| ---: | ---: | ---: | ---: |
| 1 | 4.708 s | 6.480 s | -37.6% |
| 2 | 4.306 s | 5.300 s | -23.1% |
| Runtime-derived (4 here) | 4.363 s | 4.994 s | -14.5% |

With low package concurrency, one package's process-retry drain delays later
package binaries. With higher package and process concurrency, more work can
overlap, but short retries remain startup-bound.

## Multi-Package Workload Profiles

Eight packages run five EFD retries with the runtime-derived process limit.

| Profile | In-process first pass / total | Process first pass / total | First-pass change | Total change |
| --- | ---: | ---: | ---: | ---: |
| Startup | 5.569 / 5.670 s | 6.223 / 6.883 s | -11.7% | -21.4% |
| Body | 5.960 / 6.185 s | 4.707 / 5.407 s | +21.0% | +12.6% |
| CPU | 7.912 / 7.973 s | 4.875 / 5.672 s | +38.4% | +28.9% |

The body and CPU rows demonstrate the intended scheduler advantage. The
startup row demonstrates the fixed cost of process isolation.

## Resource Diagnostics

Resource sampling invokes `ps` every 50 ms and therefore runs separately from
the timing matrix. These are single diagnostic samples, not statistically
strong acceptance thresholds.

| Profile | Peak RSS in-process / process | Peak CPU in-process / process |
| --- | ---: | ---: |
| Startup | 2503 / 2917 MiB | 364% / 462% |
| Body | 2940 / 2812 MiB | 453% / 428% |
| CPU | 2690 / 2950 MiB | 570% / 449% |

No single resource multiplier applies to every workload. More child processes
can increase RSS, while shorter parallel completion can reduce the observed
peak in body-heavy cases.

## Correctness Coverage

The same generated scenarios passed for both execution modes with exact
accounting for:

- ATR/FTR initial pass, fail-to-pass, persistent failure, and budget limiting;
- sequential and parallel EFD, including persistent failures;
- A2F success/failure and precedence over EFD/FTR;
- disabled and quarantined tests, with and without A2F;
- ITR forced-run plus FTR;
- coverage plus EFD;
- retry reason and retry count; and
- zero process children for in-process retries.

Panic, `runtime.Goexit`, timeout, cancellation, malformed result, containment
loss, and unreaped-child behavior remain correctness tests rather than
performance cells. Their desired property is bounded and correct failure, not
minimum throughput.

## Conclusions

1. The no-retry path is neutral within local measurement noise.
2. Process mode has a meaningful fixed startup cost per retry.
3. Parallel process retries outperform in-process retries when body or CPU work
   is large enough and the runner has spare capacity.
4. Process-sequential is consistently slower than in-process execution.
5. Low `go test -p` and low process concurrency amplify process overhead.
6. Deferred execution improves scheduler availability only when independent
   work exists to overlap with the retry drain.
7. The reason to choose process mode remains isolation. Performance is a
   workload-specific secondary benefit, not a general guarantee.

## Reproduction

```sh
go test ./internal/civisibility/integrations/gotesting/retryprocess \
  -run '^TestProcessRetryBurstFamilyScenarios$' -count=1

go test ./internal/civisibility/integrations/gotesting/retryprocess \
  -run '^$' -bench '^BenchmarkRetryExecutionModeMatrix$' \
  -benchtime=3x -count=1

go test ./internal/civisibility/integrations/gotesting/retryprocess \
  -run '^$' -bench '^BenchmarkProcessRetryEFD$' \
  -benchtime=3x -count=1

go test ./internal/civisibility/integrations/gotesting/retryprocess \
  -run '^$' -bench '^BenchmarkProcessRetryParallelEFDCPU$' \
  -benchtime=3x -count=1

PROCESS_RETRY_BURST_SAMPLE_RESOURCES=true \
go test ./internal/civisibility/integrations/gotesting/retryprocess \
  -run '^$' -bench '^BenchmarkRetryExecutionModeMatrix/profile' \
  -benchtime=1x -count=1
```

## Limitations

- Measurements are local to Darwin arm64 on an Apple M2 Max. Process startup
  costs differ on Linux, Windows, loaded CI runners, and hosts with security
  tooling.
- Three samples identify large effects but cannot support sub-percent claims.
- Test-binary build and command startup impose a multi-second floor on the
  multi-package benchmark. The focused microbenchmarks are the stronger proof
  for the no-retry hot path.
- The fake intake and synthetic work provide repeatability, not a model of
  every customer repository.
- One CI Visibility session exists per package binary, matching `go test`; the
  benchmark does not model a repository-global FTR budget across processes.
