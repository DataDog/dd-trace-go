// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package export provides an offline client for exporting already-built LLM
// Observability spans and evaluations to Datadog.
//
// It is an offline export API, not a live instrumentation API: it does not start
// the tracer, does not depend on the live LLM Obs lifecycle, and never replays
// payloads through StartSpan/Finish. It is intended for tools that reconstruct
// telemetry offline (for example, rebuilding finished agent sessions from logs)
// and need the SDK to own the export mechanics — endpoint derivation, auth,
// HTTP transport, retry classification, size handling, and structured results.
//
// Caller-assigned IDs (trace_id, span_id, parent_id) are preserved verbatim as
// LLM Obs payload fields. They are opaque strings (decimal uint64 strings are
// valid; 128-bit hex is not required) and are never routed into APM span IDs,
// APM trace IDs, or sampling.
//
// A [Client] is built with [NewClient], which takes the required ML app name and
// functional options. Exactly one route must be selected — [WithDatadogIntake]
// (direct, agentless) or [WithAgentURL] (via the Agent's EVP proxy) — alongside
// optional defaults ([WithService], [WithEnv], [WithVersion], batch-size and
// payload-size options). Spans are submitted with [Client.SubmitSpans] and
// evaluations with [Client.SubmitEvaluations].
//
// Multi-destination routing is modeled as one isolated [Client] per destination:
// construct N clients, each with its own site, API key, service and defaults.
// The client does not deduplicate, spool, or durably retry — callers retain
// ownership of reconstruction, projection, privacy filtering, deterministic ID
// generation, dedup, and outbox/backfill behavior.
//
// # Performance
//
// Export is CPU- and memory-bounded by the batch, not the whole input. The input
// slice is scanned in windows of the span/evaluation batch size
// ([WithSpanBatchSize] / [WithEvalBatchSize]; defaults 50 and 1000); each row is
// JSON-encoded once, when its batch is
// assembled, and a batch's encoded body is released before the next window is
// built — the client never holds the whole input encoded at once. Peak additional
// memory is therefore roughly one batch's encoded size (input/output values
// dominate for spans), and smaller batch sizes lower that peak at the cost of more
// requests.
//
// Requests are sent one batch at a time. Because a [Client] is safe for concurrent
// use and holds no shared mutable state, throughput for large backfills is scaled
// by the caller (e.g. a worker pool over batches or destinations), not by the
// client internally. The span size guard splits an oversized batch by bisection
// and truncates a single span's input/output only as a last resort (a rare path
// that re-encodes the affected batch). See the package benchmarks
// (BenchmarkSubmitSpans, BenchmarkSubmitEvaluations) for allocations/op and
// bytes/op.
package export
