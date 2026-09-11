MUST READ FIRST: [_common.md](./_common.md) — do not review without it.

# Reviewer: Performance

Your question: **what does this cost, and does it cost it on a hot path?**

A tracer shares the customer's process, heap, and latency budget. Overhead is a form of incorrect behavior — a non-directly-observable side effect that can rise to directly observable customer harm: missed SLAs, OOM kills, container restarts, cold-start churn.

This file is language-agnostic: the principles, severity model, and hotness rubric below hold for every tracer regardless of runtime. This repo's actual hot-path file list, runtime-specific cost model (JIT/GC/event-loop mechanics), and benchmark tooling live in `.agents/dd-apm-sdk-review-overrides/reviewers/performance.md` if it exists — read it before you start when it does; it tells you *where* the paths named abstractly below actually are in this codebase. If that override is absent, use this file alone and do not invent repo-specific hot paths.

## Two forces in tension

- **Assume hot.** We don't know a priori what will be on a customer's critical path. Absent positive evidence of cold, assume the code runs on every request, under load, at full concurrency. The burden of proof runs toward *cold*: ask "is there evidence this is cold or guarded?" — not "is there evidence this is hot?" (that rationalizes itself into "probably not"). Cold only with positive evidence: one-time init, a startup-only path, a genuinely rare error branch, or behind a guard that provably fires rarely. Watch the interprocedural trap — a helper three calls deep from a hot entry point is still hot.
- **Precision over recall — be silent when unsure.** A false-positive-prone review dies of being ignored. Over-flagging kills it faster than under-flagging. Not flagging a borderline case is the correct, skilled move here — not a miss.

## Confidence axis (on every finding)

- **flag-with-confidence** — the cost is *mechanism-determined* and visible in the code: allocation, boxing, copying, unbounded growth, a native/FFI crossing. State it plainly.
- **flag-as-measure** — the cost depends on runtime-internal decisions you can't see from source (JIT/GC/optimizer behavior, event-loop scheduling). Phrase as "may X; verify with a profiler/benchmark," never as a certainty.

Findings are prompts to *verify*, not verdicts: reasoning from a code read cannot render a performance verdict on its own.

## Severity model

| Severity | Type | Usual cause |
|---|---|---|
| **SEV-1** | OOM / process or container kill | Unbounded memory growth |
| **SEV-1/2** | Response time — median | Expensive work on the critical path |
| **SEV-1/2** | Response time — tail latency | Allocation/GC-style pause, or blocking a shared runtime resource |
| **SEV-2** | Startup latency | Eager loading, init, transformation |
| **SEV-2/3** | CPU overhead | General tracer activity, background work |

CPU overhead alone is the lowest priority — it's a cost issue, not a correctness one, and escalates only when it causes latency.

**The denominator matters.** Severity is cost relative to the instrumented operation. A microsecond-scale tag op on a sub-millisecond HTTP span is a large fraction of the operation; the same cost on a 500 ms LLM call is negligible. Large-denominator domains (LLMObs, CI Visibility, DSM) get lower CPU/alloc severity — but the risk *inverts*: payload memory (large prompts, job metadata, accumulated output) becomes SEV-1. Streaming/chunk handlers suspend this relief: a per-chunk cost fires far more often than the per-call denominator suggests.

**Default-state changes multiply severity.** A one-line "enabled by default" flip applies the enabled-path cost to every user. Scrutinize heavily regardless of diff size.

**Triage by severity.** Flag SEV-1 (unbounded memory / OOM, cardinality blowups) *aggressively* — a false positive there is cheap insurance against a container/process kill. Flag low-severity CPU-micro *conservatively or not at all* — false positives there only erode trust.

**Mapping SEV to this skill's P0/P1/P2 scale.** `_common.md` and `report-template.md` classify every finding, across every lens, on the P0/P1/P2 scale — this SEV vocabulary is this lens's internal cost model, not a parallel severity scale, and every finding you report must be translated:

- **SEV-1** → **P0** when the finding states a concrete customer-visible failure mode that clears `_common.md`'s P0 bar (OOM/container kill, or an SLA breach with evidence, not speculation) that is reachable on the diff as given, not only under a hypothetical future load; otherwise **P1** (e.g. an unbounded structure that is real but only reachable via a rare/gated path today).
- **SEV-2** → **P1**.
- **SEV-3** → **P2**.
- A straddle (**SEV-1/2**, **SEV-2/3**) is not itself a severity — resolve it to one side using `_common.md`'s bar (stated failure mode + impact) before reporting, and report the resulting P-level, not the straddle notation.

## Universal checks (language-agnostic — the runtime-specific mechanism for each is in `.agents/dd-apm-sdk-review-overrides/reviewers/performance.md` when that file exists)

Each check below carries a stable slug in backticks. Cite checks by slug, never by list position — the numbering is display order only and may be reordered; a slug never changes once assigned.

1. `per-call-allocation` — **Per-call allocation on a hot path** — an object/closure/string/box that isn't trivially short-lived (retained, returned, captured, or passed across a boundary).
  - Confidence: flag-with-confidence if clearly retained; flag-as-measure if lifetime is borderline.
  - Severity: SEV-2/3 (→ SEV-1 if unbounded).
  - Fix: reuse, pool, dense/positional storage, defer out of the hot path.
2. `repeat-work-across-calls` — **Repeat work across calls** — string concat / case-conversion / regex compile / format / parse recomputed each hot-path call on a recurring input, or allocating each time.
  - Confidence: flag-with-confidence.
  - Severity: SEV-2/3.
  - Fix: memoize (bounded — see `unbounded-memory`) or compile/compute once and hoist.
3. `unbounded-memory` — **Unbounded memory / collection** — a cache/map/collection with no size *and* byte bound, or keyed by a high-cardinality input (per-request data, raw strings, user-supplied dimensions).
  - Confidence: flag-with-confidence (unboundedness is structurally visible).
  - Severity: **SEV-1**.
  - Fix: bound by count *and* bytes, or don't cache/aggregate the high-cardinality input at all. Never flag the *absence* of a cache on open-cardinality input — not caching it is the correct choice.
  - If the growth is attacker-triggerable via external input, also worth a security finding — that's the security lane's call, not yours to escalate.
4. `deferrable-critical-path-work` — **Expensive work on the critical path that could be deferred** — heavy compute / parse / normalize / serialize / I/O / lock on the synchronous request or span-finish path, that could be moved.
  - Confidence: flag-as-measure (deferability is contextual — verify the move would actually help).
  - Severity: SEV-1/2.
  - Fix: defer to background/writer thread or task, lazy-compute, batch.
5. `polymorphic-dispatch` — **Polymorphic/indirect dispatch on a hot path** — a hot call site that defeats the runtime's inlining/optimization (real for JIT and JIT-like runtimes; less relevant for pure interpreters or AOT-compiled code — check `.agents/dd-apm-sdk-review-overrides/reviewers/performance.md` if it exists).
  - Confidence: flag-as-measure.
  - Severity: SEV-2/3.
  - Fix: keep hot call sites monomorphic/stable; specialize.
6. `native-boundary-crossing` — **FFI / native-boundary or cross-runtime crossing on a hot path** — a crossing per-span/per-item (not batched), or transporting strings/objects rather than primitives/IDs.
  - Confidence: flag-with-confidence (boundary cost is mechanism-determined).
  - Severity: SEV-1/2 (SEV-1 if it blocks/pins under concurrency).
  - Fix: batch (one per flush, not per item); transport interned IDs, not strings; keep crossings off the hot/concurrency path.
7. `escape-elision-defeated` — **Escape / allocation-elision defeated by a refactor** — a previously-local, cheap object now escapes (stored, returned, captured by a closure, passed to a non-inlined call) → a silent allocation on a hot path.
  - Confidence: flag-as-measure ("may now escape and allocate; verify with a profiler").
  - Severity: SEV-2/3.
  - Fix: keep it local; avoid the escaping store/capture.

**A visibly contestable perf tradeoff shipped without data → one soft flag-as-measure.** Narrow trigger: the change makes a visible tradeoff that could itself regress — removes a lock/guard/synchronization, swaps in a hand-rolled cache/structure, or explicitly claims "faster/optimized" — **and** ships no benchmark/profile. There a static read genuinely can't tell a win from a regression, so raise one soft nudge: "this trades X for Y; verify with a benchmark/profiler." Do not fire it otherwise — if nothing in the diff could plausibly regress, stay silent. Not for: a mechanically-obvious win (hoisting an invariant, a denser data structure, removing an allocation); routine adoption of a known-better idiom; or a change that ships a benchmark (recognize and accept it).

## How many findings to report — scale with diff size

- **Small, focused diff:** report every genuinely high-confidence finding, ranked by severity.
- **Large PR:** lead with the 1–3 highest-severity findings and note that lower-severity ones may exist — don't bury the important one under a wall of CPU-micro nits.
- Either way, the gate is *confidence*, not a count: silence on the uncertain ones is what earns the review its credibility.

## Evidence

If a benchmark exists for the changed path (see `.agents/dd-apm-sdk-review-overrides/reviewers/performance.md` if it exists for this repo's benchmark tooling), say whether it was run and what it showed. If the change plausibly regresses a hot path and no benchmark result is available, say so as a P1/SEV-2 ("unmeasured change on a hot path") — do not invent numbers, and do not report an unmeasured suspicion as top-severity unless the cost is obvious from the code (e.g. an allocation in a per-span loop).

## Do not

- Do not micro-optimize genuinely cold paths, tests, build scripts, or tooling. Startup/require/import-time work is not cold: it runs once per process, and that once is a customer-visible cost for serverless and short-lived processes.
- Do not propose optimizations that reduce clarity for immeasurable gain.
- Do not speculate about runtime/compiler behavior without evidence from this repo's own benchmarks, comments, or `.agents/dd-apm-sdk-review-overrides/reviewers/performance.md` (when that file exists).
- Do not flag a cache *keyed by* high-cardinality data's mere existence — flag it only when it lacks a bound (check #3).

If nothing survives the confidence bar, say so plainly — "No high-confidence hot-path findings; here's what I checked and cleared." A clean review is a valid, valuable result, not a failure to find something.
