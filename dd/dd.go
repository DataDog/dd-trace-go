// dd holds the interfaces and structures shared by Datadog's tracer packages.
// It is a good overview of the available API and functionalities.
package dd

import "time"

// Tracer describes the form of the Datadog tracer.
type Tracer interface {
	// StartSpan starts a span with the given operation name and options.
	StartSpan(operationName string, opts ...StartSpanOption) Span

	// SetServiceInfo sets information about the service with the given name.
	SetServiceInfo(name, app, appType string)

	// Extract extracts a span context from a given carrier.
	Extract(carrier interface{}) (SpanContext, error)

	// Inject injects a span context into the given carrier.
	Inject(context SpanContext, carrier interface{}) error

	// Stop stops the active tracer and sets the global tracer to a no-op.
	Stop()
}

// Span represents a computation.
type Span interface {
	// SetTag sets a given tag on the span.
	SetTag(key string, value interface{}) Span

	// SetOperationName resets the original operation name to the given one.
	SetOperationName(operationName string) Span

	// BaggageItem returns the baggage item with the given key.
	BaggageItem(key string) string

	// SetBaggageItem sets a new baggage item at the given key. The baggage
	// item should propagate to all descendant spans, both in- and cross-process.
	SetBaggageItem(key, val string) Span

	// Finish finishes the current span with the given options.
	Finish(opts ...FinishOption)

	// Context returns the SpanContext of this Span.
	Context() SpanContext
}

// SpanContext holds information about a span which propagates from child to parent,
// both in the same process as well as cross-process.
type SpanContext interface {
	// ForeachBaggageItem provides an iterator over the key/value pairs set
	// as baggage within this context.
	ForeachBaggageItem(handler func(k, v string) bool)
}

// StartSpanOption is a configuration option for StartSpan.
type StartSpanOption func(cfg *StartSpanConfig)

// FinishOption is a configuration option for FinishSpan.
type FinishOption func(cfg *FinishConfig)

// FinishConfig holds the configuration for finishing a span.
type FinishConfig struct {
	// FinishTime represents the time that should be set as finishing time
	// for the span.
	FinishTime time.Time

	// Error holds an optional error that should be set on the span before
	// finishing.
	Error error
}

// StartSpanConfig holds the configuration for starting a new span.
type StartSpanConfig struct {
	// Parent holds the SpanContext that should be used as parent for the
	// started span. If nil, a root span should be returned by the implementation.
	Parent SpanContext

	// StartTime holds the time that should be used as start time for the span.
	// Most implementations should default this to now if the time IsZero().
	StartTime time.Time

	// Tags holds a set of tags which should be set on the span at creation time.
	Tags map[string]interface{}
}
