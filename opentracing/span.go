package opentracing

import (
	"github.com/DataDog/dd-trace-go/tracer"
	ot "github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/log"
)

// Span represents an active, un-finished span in the OpenTracing system.
// Spans are created by the Tracer interface.
type Span struct {
	*tracer.Span
	context SpanContext
	tracer  *Tracer
}

// Context yields the SpanContext for this Span. Note that the return
// value of Context() is still valid after a call to Span.Finish(), as is
// a call to Span.Context() after a call to Span.Finish().
func (s *Span) Context() ot.SpanContext {
	return s.context
}

// SetBaggageItem sets a key:value pair on this Span and its SpanContext
// that also propagates to descendants of this Span.
func (s *Span) SetBaggageItem(key, val string) ot.Span {
	return s
}

// BaggageItem gets the value for a baggage item given its key. Returns the empty string
// if the value isn't found in this Span.
func (s *Span) BaggageItem(key string) string {
	return ""
}

// SetTag adds a tag to the span, overwriting pre-existing values for
// the given `key`.
func (s *Span) SetTag(key string, value interface{}) ot.Span {
	return s
}

// LogFields is an efficient and type-checked way to record key:value
// logging data about a Span, though the programming interface is a little
// more verbose than LogKV().
func (s *Span) LogFields(fields ...log.Field) {
}

// LogKV is a concise, readable way to record key:value logging data about
// a Span, though unfortunately this also makes it less efficient and less
// type-safe than LogFields().
func (s *Span) LogKV(keyVals ...interface{}) {
}

// FinishWithOptions is like Finish() but with explicit control over
// timestamps and log data.
func (s *Span) FinishWithOptions(opts ot.FinishOptions) {
}

// SetOperationName sets or changes the operation name.
func (s *Span) SetOperationName(operationName string) ot.Span {
	return s
}

// Tracer provides access to the `Tracer`` that created this Span.
func (s *Span) Tracer() ot.Tracer {
	return s.tracer
}

// LogEvent is deprecated: use LogFields or LogKV
func (s *Span) LogEvent(event string) {
}

// LogEventWithPayload deprecated: use LogFields or LogKV
func (s *Span) LogEventWithPayload(event string, payload interface{}) {
}

// Log is deprecated: use LogFields or LogKV
func (s *Span) Log(data ot.LogData) {
}
