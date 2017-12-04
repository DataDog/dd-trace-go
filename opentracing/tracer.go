package opentracing

import (
	"time"

	datadog "github.com/DataDog/dd-trace-go/tracer"
	ot "github.com/opentracing/opentracing-go"
)

// Tracer is a simple, thin interface for Span creation and SpanContext
// propagation. In the current state, this Tracer is a compatibility layer
// that wraps the Datadog Tracer implementation.
type Tracer struct {
	impl        *datadog.Tracer // a Datadog Tracer implementation
	serviceName string          // default Service Name defined in the configuration
}

// StartSpan creates, starts, and returns a new Span with the given `operationName`
// A Span with no SpanReference options (e.g., opentracing.ChildOf() or
// opentracing.FollowsFrom()) becomes the root of its own trace.
func (t *Tracer) StartSpan(operationName string, options ...ot.StartSpanOption) ot.Span {
	sso := ot.StartSpanOptions{}
	for _, o := range options {
		o.Apply(&sso)
	}

	return t.startSpanWithOptions(operationName, sso)
}

func (t *Tracer) startSpanWithOptions(operationName string, options ot.StartSpanOptions) ot.Span {
	if options.StartTime.IsZero() {
		// TODO: we should set this value
		options.StartTime = time.Now().UTC()
	}

	var parent *Span
	var span *datadog.Span

	for _, ref := range options.References {
		ctx, ok := ref.ReferencedContext.(SpanContext)
		if !ok {
			// ignore the SpanContext since it's not valid
			continue
		}

		// if we have parenting define it
		if ref.Type == ot.ChildOfRef {
			parent = ctx.span
		}
	}

	if parent == nil {
		// create a root Span with the default service name and resource
		span = t.impl.NewRootSpan(operationName, t.serviceName, operationName)
	} else {
		// create a child Span that inherits from a parent
		span = t.impl.NewChildSpan(operationName, parent.Span)
	}

	otSpan := &Span{
		Span: span,
		context: SpanContext{
			traceID:  span.TraceID,
			spanID:   span.SpanID,
			parentID: span.ParentID,
			sampled:  span.Sampled,
		},
	}

	otSpan.context.span = otSpan

	if parent != nil {
		// propagate baggage items
		if l := len(parent.context.baggage); l > 0 {
			otSpan.context.baggage = make(map[string]string, len(parent.context.baggage))
			for k, v := range parent.context.baggage {
				otSpan.context.baggage[k] = v
			}
		}
	}

	// set tags if available
	if len(options.Tags) > 0 {
		for k, v := range options.Tags {
			otSpan.SetTag(k, v)
		}
	}

	return otSpan
}

// Inject takes the `sm` SpanContext instance and injects it for
// propagation within `carrier`. The actual type of `carrier` depends on
// the value of `format`.
func (t *Tracer) Inject(sp ot.SpanContext, format interface{}, carrier interface{}) error {
	return nil
}

// Extract returns a SpanContext instance given `format` and `carrier`.
func (t *Tracer) Extract(format interface{}, carrier interface{}) (ot.SpanContext, error) {
	return nil, nil
}

// Close method implements `io.Closer` interface to graceful shutdown the Datadog
// Tracer. Note that this is a blocking operation that waits for the flushing Go
// routine.
func (t *Tracer) Close() error {
	t.impl.Stop()
	return nil
}
