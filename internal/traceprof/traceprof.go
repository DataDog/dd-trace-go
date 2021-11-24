// Package traceprof contains shared logic for cross-cutting tracer/profiler features.
package traceprof

// pprof labels applied by the tracer to show up in the profiler's profiles.
const (
	SpanID          = "span id"
	LocalRootSpanID = "local root span id"
	TraceEndpoint   = "trace endpoint"
)
