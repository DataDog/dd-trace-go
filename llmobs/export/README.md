# llmobs/export

Offline export client for **already-built** LLM Observability spans and
evaluations. It POSTs payloads you have reconstructed out-of-band to Datadog
**without** starting the tracer or running live instrumentation, reusing the
SDK's transport mechanics — endpoint/auth derivation, HTTP, retry
classification, size limits, and structured per-request results — instead of
maintaining a separate exporter.

This is a supported public interaction path distinct from the live tracer. See
the [package documentation](https://pkg.go.dev/github.com/DataDog/dd-trace-go/v2/llmobs/export)
for the full API; `otlp/export` is a stacked sibling for offline OTLP
trace/metric/log export.

## Quick start

```go
client, err := export.NewClient("my-ml-app",
    export.WithDatadogIntake("datadoghq.com", os.Getenv("DD_API_KEY")), // or export.WithAgentURL("http://localhost:8126")
    export.WithService("my-service"),
)
if err != nil {
    return err
}

res, err := client.SubmitSpans(ctx, []export.SpanEvent{
    {
        TraceID: "1234567890",
        SpanID:  "2345678901",
        Kind:    export.KindLLM,
        Name:    "chat",
        Input:   "hello",
        Output:  "hi there",
    },
})
// res.Sent / res.Dropped / res.Failed and res.ValidationErrors report per-row
// and per-request outcomes; err is non-nil if any request failed.
```

## Conventions

Keep these invariants when changing this package:

- **Caller-assigned IDs are payload fields only.** `trace_id`/`span_id`/`parent_id`
  are opaque, caller-owned strings preserved verbatim on the wire; they are never
  routed into APM span/trace IDs or sampling.
- **Reuse the internal wire structs.** Lower the public types into
  [`internal/llmobs/transport`](../../internal/llmobs/transport) rather than
  defining parallel wire structs, so the emitted shape cannot drift from the rest
  of the SDK.
- **Row-level validation, batch-safe.** Invalid rows are dropped and reported in
  the result, never failing a whole batch.
- **One client per destination.** Multi-destination export is modeled as N
  isolated clients, each with its own route and defaults.
