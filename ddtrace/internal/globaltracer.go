package internal

import "github.com/DataDog/dd-trace-go/ddtrace"

// GlobalTracer holds the currently active tracer. It's "zero value" should
// always be the NoopTracer.
var GlobalTracer ddtrace.Tracer = &NoopTracer{}

var _ ddtrace.Tracer = (*NoopTracer)(nil)

// NoopTracer is an implementation of ddtrace.Tracer that is a no-op.k
type NoopTracer struct{}

func (NoopTracer) StartSpan(operationName string, opts ...ddtrace.StartSpanOption) ddtrace.Span {
	return NoopSpan{}
}

func (NoopTracer) SetServiceInfo(name, app, appType string) {}
func (NoopTracer) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	return NoopSpanContext{}, nil
}
func (NoopTracer) Inject(context ddtrace.SpanContext, carrier interface{}) error { return nil }
func (NoopTracer) Stop()                                                         {}

var _ ddtrace.Span = (*NoopSpan)(nil)

type NoopSpan struct{}

func (NoopSpan) SetTag(key string, value interface{}) ddtrace.Span  { return NoopSpan{} }
func (NoopSpan) SetOperationName(operationName string) ddtrace.Span { return NoopSpan{} }
func (NoopSpan) BaggageItem(key string) string                      { return "" }
func (NoopSpan) SetBaggageItem(key, val string) ddtrace.Span        { return NoopSpan{} }
func (NoopSpan) Finish(opts ...ddtrace.FinishOption)                {}
func (NoopSpan) Tracer() ddtrace.Tracer                             { return NoopTracer{} }
func (NoopSpan) Context() ddtrace.SpanContext                       { return NoopSpanContext{} }

var _ ddtrace.SpanContext = (*NoopSpanContext)(nil)

type NoopSpanContext struct{}

func (NoopSpanContext) ForeachBaggageItem(handler func(k, v string) bool) {}
