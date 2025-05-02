package internal

import "sync/atomic"

var (
	// globalTracer stores the current tracer as *ddtrace.Tracer (pointer to interface). The
	// atomic.Value type requires types to be consistent, which requires using *ddtrace.Tracer.
	GlobalTracer atomic.Value
)
