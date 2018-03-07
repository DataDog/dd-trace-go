package opentracer

import (
	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace/tracer"

	opentracing "github.com/opentracing/opentracing-go"
)

// Start starts the tracer using the given options and registers it as the global
// opentracing tracer using opentracing.SetGlobalTracer. After calling Start, you
// may use the opentracing API as usual. Using this API in parallel with the tracer
// API is fully supported as both implementations are using the same tracer under
// the hood.
func Start(opts ...tracer.StartOption) {
	tracer.Start(opts...)
	t := &opentracer{internal.GlobalTracer}
	opentracing.SetGlobalTracer(t)
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
	opts := []ddtrace.StartSpanOption{tracer.StartTime(sso.StartTime)}
	for _, ref := range sso.References {
		if v, ok := ref.ReferencedContext.(ddtrace.SpanContext); ok && ref.Type == opentracing.ChildOfRef {
			opts = append(opts, tracer.ChildOf(v))
			break // can only have one parent
		}
	}
	for k, v := range sso.Tags {
		opts = append(opts, tracer.Tag(k, v))
	}
	return &span{
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
