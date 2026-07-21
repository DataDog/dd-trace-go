// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package export provides offline clients for exporting already-built OTLP
// traces, metrics, and logs to Datadog.
//
// It accepts OTLP collector proto requests
// (go.opentelemetry.io/proto/otlp/collector/{trace,metrics,logs}/v1) and posts
// them, protobuf-encoded, to Datadog's agentless OTLP intake (derived from the
// configured site as https://otlp.<site>/v1/{traces,metrics,logs}) or to a
// caller-provided collector/Agent endpoint. It uses a raw-proto transport and
// coexists with the OTel-SDK-based exporters in ddtrace/opentelemetry.
//
// It is an offline export API, not live instrumentation: the SDK owns endpoint
// derivation, auth/header injection, HTTP transport, retry classification, and
// structured results. Callers own proto construction, projection, temporality
// and histogram semantics, deterministic IDs, dedup, and durable retry.
//
// Each input request is treated atomically: one *Export*ServiceRequest becomes
// one POST and one result row. The SDK does not merge requests or split an
// oversized request. Caller-provided trace IDs, trace flags and tracestate are
// preserved as-is; the SDK does not infer W3C trace-ID randomness for
// reconstructed spans.
//
// Multiple destinations are modeled as one isolated client per destination.
//
// # Performance
//
// Requests are exported sequentially — one POST at a time — and each request
// holds its proto value plus its marshaled protobuf body in memory for the
// duration of that POST, so a slow destination serializes the rest of the batch.
// A Client is safe for concurrent use and holds no shared mutable state, so
// throughput for large exports is scaled by the caller: fan out across requests
// (or destinations) with a worker pool sized to taste, rather than relying on
// internal concurrency. See BenchmarkExportTraces for allocations/op and
// bytes/op.
//
// This package intentionally provides its own raw-proto transport rather than
// reusing ddtrace/tracer's OTLP writer; the owning team is recorded in
// CODEOWNERS so the two OTLP paths stay coherent.
package export
