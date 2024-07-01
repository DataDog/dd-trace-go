package ext

const (
	// LogKeyTraceID is used by log integrations to correlate logs with a given trace.
	LogKeyTraceID = "dd.trace_id"
	// LogKeySpanID is used by log integrations to correlate logs with a given span.
	LogKeySpanID = "dd.span_id"
)
