package tracer

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"

	opentracing "github.com/opentracing/opentracing-go"
)

// NewOpenTracer creates, instantiates and returns an Opentracing compatible version of the
// Datadog tracer using the provided set of options.
func NewOpenTracer(opts ...StartOption) opentracing.Tracer {
	Start(opts...)
	return &opentracer{internal.GetGlobalTracer()}
}

var _ opentracing.Tracer = (*opentracer)(nil)

// opentracer implements opentracing.Tracer on top of ddtrace.Tracer.
type opentracer struct{ ddtrace.Tracer }

// StartSpan implements opentracing.Tracer.
func (t *opentracer) StartSpan(operationName string, options ...opentracing.StartSpanOption) opentracing.Span {
	var sso opentracing.StartSpanOptions
	for _, o := range options {
		o.Apply(&sso)
	}
	opts := []ddtrace.StartSpanOption{StartTime(sso.StartTime)}
	for _, ref := range sso.References {
		if v, ok := ref.ReferencedContext.(ddtrace.SpanContext); ok && ref.Type == opentracing.ChildOfRef {
			opts = append(opts, ChildOf(v))
			break // can only have one parent
		}
	}
	for k, v := range sso.Tags {
		opts = append(opts, Tag(k, v))
	}
	return &openSpan{
		Span:       t.Tracer.StartSpan(operationName, opts...),
		opentracer: t,
	}
}

// Inject implements opentracing.Tracer.
func (t *opentracer) Inject(ctx opentracing.SpanContext, format interface{}, carrier interface{}) error {
	sctx, ok := ctx.(ddtrace.SpanContext)
	if !ok {
		return opentracing.ErrUnsupportedFormat
	}
	switch format {
	case opentracing.TextMap, opentracing.HTTPHeaders:
		return t.Tracer.Inject(sctx, carrier)
	default:
		return opentracing.ErrUnsupportedFormat
	}
}

// Extract implements opentracing.Tracer.
func (t *opentracer) Extract(format interface{}, carrier interface{}) (opentracing.SpanContext, error) {
	switch format {
	case opentracing.TextMap, opentracing.HTTPHeaders:
		return t.Tracer.Extract(carrier)
	default:
		return nil, opentracing.ErrUnsupportedFormat
	}
}
