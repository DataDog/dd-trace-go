# Core dd-trace-go Implementations

## Read README.md First

**BEFORE making ANY code changes**, you MUST read [README.md](./README.md) for information about API changes and how to handle them. Namely:

```bash
# Full diff (compatible + incompatible)
make apidiff

# Breaking changes only — exits 1 if any are found
make apidiff/incompatible
```

For information about the tracer's abilities, you MUST read [doc.go](./doc.go). Some key takeaways are:

The tracer starts in `tracer.Start(...)` with options and can be stopped using `defer tracer.Stop()`. While the tracer is running, it can:

* Sample traces to reduce overhead using rate samplers (`tracer.NewRateSampler(...)`), sampling rules (`tracer.WithSamplingRule(...)`), or environment variables `DD_TRACE_SAMPLING_RULES` or `DD_SPAN_SAMPLING_RULES`. 
* Create spans using `StartSpan()` or `StartSpanFromContext()` with optional start options that configure the created spans
* Span contexts provide information about a span and are especially important for distributed tracing (ie injecting a context into HTTP requests).