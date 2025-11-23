// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package sdkgov2 provides tracing for the cloudevents/sdk-go/v2 package.
package sdkgov2

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/event"
)

const (
	componentName = instrumentation.PackageCloudEventsV2
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(componentName)
}

var _ interface {
	tracer.TextMapReader
	tracer.TextMapWriter
} = (*EventCarrier)(nil)

// EventCarrier wraps a CloudEvent to implement tracer.TextMapReader and tracer.TextMapWriter
// for extracting and injecting trace context via CloudEvent extensions.
type EventCarrier struct {
	event *cloudevents.Event
}

// NewEventCarrier creates a new EventCarrier that wraps a CloudEvent.
// This carrier can be used with tracer.Extract and tracer.Inject to propagate
// trace context through CloudEvent extensions.
//
// Note: CloudEvents extension names cannot contain hyphens. This carrier only propagates
// W3C trace context headers (traceparent, tracestate) which are CloudEvents-compliant.
// Ensure that DD_TRACE_PROPAGATION_STYLE includes "tracecontext" (W3C format) for proper
// trace propagation. Pure Datadog-style propagation will not work as headers like
// "x-datadog-trace-id" contain hyphens and cannot be CloudEvents extensions.
//
// Example (extracting):
//
//	carrier := NewEventCarrier(&event)
//	spanCtx, _ := tracer.Extract(carrier)
//
// Example (injecting):
//
//	carrier := NewEventCarrier(&event)
//	tracer.Inject(span.Context(), carrier)
func NewEventCarrier(e *cloudevents.Event) EventCarrier {
	return EventCarrier{event: e}
}

// ForeachKey iterates over all CloudEvent extensions that can be used for trace propagation.
func (c EventCarrier) ForeachKey(handler func(key, val string) error) error {
	extensions := c.event.Extensions()
	if len(extensions) == 0 {
		return nil
	}

	// Iterate over CloudEvent extensions and pass string values to the handler
	for key, val := range extensions {
		if strVal, ok := val.(string); ok {
			if err := handler(key, strVal); err != nil {
				return err
			}
		}
	}
	return nil
}

// Set sets a CloudEvent extension with the given key/value pair.
// Only sets extensions with valid CloudEvents-compliant names (no hyphens).
func (c EventCarrier) Set(key, val string) {
	// Only propagate W3C trace context headers and other valid extension names
	// CloudEvents extension names must not contain hyphens, so this filters out
	// Datadog-specific headers like x-datadog-* which can't be properly propagated
	if event.IsExtensionNameValid(key) {
		c.event.SetExtension(key, val)
	}
}

// HandlerFunc defines the signature for CloudEvents handlers for most messaging systems.
type HandlerFunc func(context.Context, cloudevents.Event) error

// WrapHandler wraps a CloudEvents handler to enable distributed tracing.
// It extracts trace context from the event, creates a consumer span, and propagates the
// context to the handler.
//
// By default, the subject field is not included in tags as it may contain sensitive data.
// Use WithSubject() option to include it. Use WithResourceName() to set a custom resource name.
//
// For more control over tracing, you can use NewEventCarrier with tracer.Extract and
// tracer.StartSpanFromContext directly.
func WrapHandler(originalHandler HandlerFunc, opts ...Option) HandlerFunc {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	return func(ctx context.Context, event cloudevents.Event) (err error) {
		carrier := NewEventCarrier(&event)
		parentSpanCtx, _ := tracer.Extract(carrier)

		spanOpts := []tracer.StartSpanOption{
			tracer.ResourceName(cfg.resourceName),
			tracer.SpanType(ext.SpanTypeMessageConsumer),
			tracer.Tag("event.id", event.ID()),
			tracer.Tag("event.type", event.Type()),
			tracer.Tag("event.source", event.Source()),
			tracer.Tag("message_size", len(event.Data())),
			tracer.Tag(ext.Component, "cloudevents"),
			tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
		}

		if parentSpanCtx != nil {
			spanOpts = append(spanOpts, tracer.ChildOf(parentSpanCtx))
			if parentSpanCtx.SpanLinks() != nil {
				spanOpts = append(spanOpts, tracer.WithSpanLinks(parentSpanCtx.SpanLinks()))
			}
		}

		if cfg.messagingSystem != "" {
			spanOpts = append(spanOpts, tracer.Tag(ext.MessagingSystem, cfg.messagingSystem))
		} else {
			spanOpts = append(spanOpts, tracer.Tag(ext.MessagingSystem, "cloudevents"))
		}

		if cfg.includeSubject && event.Subject() != "" {
			spanOpts = append(spanOpts, tracer.Tag("event.subject", event.Subject()))
		}

		span, ctx := tracer.StartSpanFromContext(
			ctx,
			instr.OperationName(instrumentation.ComponentConsumer, instrumentation.OperationContext{
				"cloudeventsOperation": "receive",
			}),
			spanOpts...)
		defer span.Finish(tracer.WithError(err))

		return originalHandler(ctx, event)
	}
}
