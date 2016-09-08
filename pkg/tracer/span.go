package tracer

// Span is the common struct we use to represent a dapper-like span.
// More information about the structure of the Span can be found
// here: http://research.google.com/pubs/pub36356.html
type Span struct {
	Name     string             `json:"name"`      // the name of what we're monitoring (e.g. redis.command)
	Service  string             `json:"service"`   // the service related to this trace (e.g. redis)
	Resource string             `json:"resource"`  // the natural key of what we measure (e.g. GET)
	Type     string             `json:"type"`      // protocol associated with the span
	Start    int64              `json:"start"`     // span start time expressed in nanoseconds since epoch
	Duration int64              `json:"duration"`  // duration of the span expressed in nanoseconds
	Error    int32              `json:"error"`     // error status of the span; 0 means no errors
	Meta     map[string]string  `json:"meta"`      // arbitrary map of metadata
	Metrics  map[string]float64 `json:"metrics"`   // arbitrary map of numeric metrics
	SpanID   uint64             `json:"span_id"`   // identifier of this span
	TraceID  uint64             `json:"trace_id"`  // identifier of the root span
	ParentID uint64             `json:"parent_id"` // identifier of the span's direct parent
}
