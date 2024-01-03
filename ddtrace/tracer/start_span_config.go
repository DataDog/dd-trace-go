package tracer

import (
	"context"
	"time"
)

// StartSpanOption is a configuration option that can be used with a Tracer's StartSpan method.
type StartSpanOption func(cfg *StartSpanConfig)

// StartSpanConfig holds the configuration for starting a new span. It is usually passed
// around by reference to one or more StartSpanOption functions which shape it into its
// final form.
type StartSpanConfig struct {
	// Parent holds the SpanContext that should be used as a parent for the
	// new span. If nil, implementations should return a root span.
	Parent *SpanContext

	// StartTime holds the time that should be used as the start time of the span.
	// Implementations should use the current time when StartTime.IsZero().
	StartTime time.Time

	// Tags holds a set of key/value pairs that should be set as metadata on the
	// new span.
	Tags map[string]interface{}

	// SpanID will be the SpanID of the Span, overriding the random number that would
	// be generated. If no Parent SpanContext is present, then this will also set the
	// TraceID to the same value.
	SpanID uint64

	// Context is the parent context where the span should be stored.
	Context context.Context
}

// NewStartSpanConfig allows to build a base config struct. It accepts the same options as StartSpan.
// It's useful to reduce the number of operations in any hot path and update it for request/operation specifics.
func NewStartSpanConfig(opts ...StartSpanOption) StartSpanConfig {
	var cfg StartSpanConfig
	for _, fn := range opts {
		fn(&cfg)
	}
	return cfg
}
