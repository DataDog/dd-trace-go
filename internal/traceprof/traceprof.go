// Package traceprof contains shared logic for cross-cutting tracer/profiler features.
package traceprof

// pprof labels applied by the tracer to show up in the profiler's profiles.
const (
	SpanID          = "span id"
	LocalRootSpanID = "local root span id"
	TraceEndpoint   = "trace endpoint"
)

// env variables used to control cross-cutting tracer/profiling features.
const (
	EndpointEnvVar     = "DD_PROFILING_ENDPOINT_COLLECTION_ENABLED"
	CodeHotspotsEnvVar = "DD_PROFILING_CODE_HOTSPOTS_COLLECTION_ENABLED"
)
