package internal

import "github.com/DataDog/dd-trace-go/dd"

// GlobalTracer holds the currently active tracer. It's "zero value" should
// always be the NoopTracer.
var GlobalTracer dd.Tracer = &NoopTracer{}

var _ dd.Tracer = (*NoopTracer)(nil)

// NoopTracer is an implementation of dd.Tracer that is a no-op.k
type NoopTracer struct{}

func (NoopTracer) StartSpan(operationName string, opts ...dd.StartSpanOption) dd.Span {
	return NoopSpan{}
}

func (NoopTracer) SetServiceInfo(name, app, appType string)                 {}
func (NoopTracer) Extract(carrier interface{}) (dd.SpanContext, error)      { return NoopSpanContext{}, nil }
func (NoopTracer) Inject(context dd.SpanContext, carrier interface{}) error { return nil }
func (NoopTracer) Stop()                                                    {}

var _ dd.Span = (*NoopSpan)(nil)

type NoopSpan struct{}

func (NoopSpan) SetTag(key string, value interface{}) dd.Span  { return NoopSpan{} }
func (NoopSpan) SetOperationName(operationName string) dd.Span { return NoopSpan{} }
func (NoopSpan) BaggageItem(key string) string                 { return "" }
func (NoopSpan) SetBaggageItem(key, val string) dd.Span        { return NoopSpan{} }
func (NoopSpan) Finish(opts ...dd.FinishOption)                {}
func (NoopSpan) Tracer() dd.Tracer                             { return NoopTracer{} }
func (NoopSpan) Context() dd.SpanContext                       { return NoopSpanContext{} }

var _ dd.SpanContext = (*NoopSpanContext)(nil)

type NoopSpanContext struct{}

func (NoopSpanContext) ForeachBaggageItem(handler func(k, v string) bool) {}
