package sdkgov2

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	cloudevents "github.com/cloudevents/sdk-go/v2"
)

// Option is a functional option for configuring CloudEvents tracing.
type Option func(*config)

type config struct {
	includeSubject bool
}

func defaultConfig() *config {
	return &config{
		includeSubject: false,
	}
}

// WithSubject enables inclusion of the event subject in the span tags.
// Note: Event subjects may contain sensitive data, so this is opt-in.
func WithSubject() Option {
	return func(c *config) {
		c.includeSubject = true
	}
}

// TraceWrapCloudEventsHandler wraps a CloudEvents handler to enable distributed tracing.
// It extracts trace context from the event, creates a consumer span, and propagates the
// context to the handler. The resourceName is used as the span's resource name.
//
// By default, the subject field is not included in tags as it may contain sensitive data.
// Use WithSubject() option to include it.
func TraceWrapCloudEventsHandler(originalHandler func(context.Context, cloudevents.Event) error, resourceName string, opts ...Option) func(context.Context, cloudevents.Event) error {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	return func(ctx context.Context, event cloudevents.Event) (err error) {
		parentSpanCtx, _ := ExtractTraceContext(event)

		spanOpts := []tracer.StartSpanOption{
			tracer.ResourceName(resourceName),
			tracer.SpanType(ext.SpanTypeMessageConsumer),
			tracer.Tag("event.id", event.ID()),
			tracer.Tag("event.type", event.Type()),
			tracer.Tag("event.source", event.Source()),
			tracer.Tag("message_size", len(event.Data())),
			tracer.Tag(ext.Component, "cloudevents"),
			tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
			tracer.ChildOf(parentSpanCtx),
		}

		if cfg.includeSubject && event.Subject() != "" {
			spanOpts = append(spanOpts, tracer.Tag("event.subject", event.Subject()))
		}

		if parentSpanCtx != nil && parentSpanCtx.SpanLinks() != nil {
			spanOpts = append(spanOpts, tracer.WithSpanLinks(parentSpanCtx.SpanLinks()))
		}

		span, ctx := tracer.StartSpanFromContext(ctx, "cloudevents.receive", spanOpts...)
		defer span.Finish(tracer.WithError(err))

		return originalHandler(ctx, event)
	}
}

// InjectTraceContext injects Datadog trace context into a CloudEvent's extensions.
// This should be called by publishers to propagate trace context across service boundaries.
//
// Example:
//
//	span, ctx := tracer.StartSpanFromContext(ctx, "publish.event")
//	defer span.Finish()
//	event := cloudevents.NewEvent()
//	if err := InjectTraceContext(span.Context(), &event); err != nil {
//	    return err
//	}
func InjectTraceContext(spanCtx *tracer.SpanContext, event *cloudevents.Event) error {
	carrier := tracer.TextMapCarrier{}
	if err := tracer.Inject(spanCtx, carrier); err != nil {
		return err
	}

	// Transfer trace headers from carrier to CloudEvent extensions
	// CloudEvents will preserve W3C trace context headers (traceparent/tracestate)
	for key, value := range carrier {
		event.SetExtension(key, value)
	}

	return nil
}

// ExtractTraceContext extracts Datadog trace context from a CloudEvent's extensions.
// This should be called by consumers to continue distributed tracing.
// Returns nil if no trace context is found (not an error condition).
//
// Example:
//
//	func handleEvent(ctx context.Context, event cloudevents.Event) error {
//	    parentSpanCtx, _ := ExtractTraceContext(event)
//	    opts := []tracer.StartSpanOption{...}
//	    if parentSpanCtx != nil {
//	        opts = append(opts, tracer.ChildOf(parentSpanCtx))
//	    }
//	    span, ctx := tracer.StartSpanFromContext(ctx, "consume.event", opts...)
//	    defer span.Finish()
//	    // ... process event
//	}
func ExtractTraceContext(event cloudevents.Event) (*tracer.SpanContext, error) {
	extensions := event.Extensions()
	if len(extensions) == 0 {
		return nil, nil
	}

	// Convert CloudEvent extensions map[string]interface{} to map[string]string
	// for tracer.Extract to work properly
	carrier := make(tracer.TextMapCarrier)
	for key, val := range extensions {
		if strVal, ok := val.(string); ok {
			carrier[key] = strVal
		}
	}

	// Extract trace context from the carrier
	// This will work with W3C trace context headers (traceparent/tracestate)
	// that CloudEvents preserves
	spanCtx, err := tracer.Extract(carrier)
	if err != nil {
		// Not finding trace context is not necessarily an error
		// It just means there's no parent trace to link to
		return nil, nil
	}

	return spanCtx, nil
}
