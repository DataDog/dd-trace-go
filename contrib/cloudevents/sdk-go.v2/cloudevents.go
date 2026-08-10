// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package cloudevents instruments the CloudEvents Go SDK. CloudEvents defines a
// common event envelope that can be transported over systems such as HTTP,
// Kafka, or Google Pub/Sub. This integration traces events as they are sent and
// received and propagates trace context in CloudEvent extension attributes.
//
// Propagation requires the tracecontext propagation style, which is enabled by
// default. The CloudEvents SDK does not invoke completion callbacks for
// undelivered requests or receiver-handler panics, so the corresponding spans
// cannot finish until that behavior is fixed upstream.
package cloudevents

import (
	"context"
	"errors"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/cloudevents/sdk-go/v2/client"
	"github.com/cloudevents/sdk-go/v2/protocol"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const componentName = instrumentation.PackageCloudEventsSDKGoV2

var instr = instrumentation.Load(componentName)

// New returns an observability service that the CloudEvents SDK calls around
// send, request, and receive operations. Install it on a client with
// client.WithObservabilityService.
//
// The service adds W3C trace context to outgoing event extensions. Callers that
// send the same event concurrently should clone it before each send.
func New(opts ...Option) client.ObservabilityService {
	service := &observabilityService{}
	defaults(&service.config)
	for _, opt := range opts {
		opt.apply(&service.config)
	}
	return service
}

// NewClient creates a CloudEvents client with Datadog observability enabled.
// It has the same signature as client.New and is used by Orchestrion.
func NewClient(obj any, opts ...client.Option) (client.Client, error) {
	opts = append(opts, client.WithObservabilityService(New()))
	return client.New(obj, opts...)
}

type observabilityService struct {
	config
}

var _ client.ObservabilityService = (*observabilityService)(nil)

type extractedContextKey struct{}

// InboundContextDecorators extracts W3C trace context and baggage from event
// metadata before the SDK decodes and dispatches an inbound message.
func (s *observabilityService) InboundContextDecorators() []func(context.Context, binding.Message) context.Context {
	return []func(context.Context, binding.Message) context.Context{s.extractInboundContext}
}

// extractInboundContext preserves propagation data until the SDK invokes the
// application receiver.
func (*observabilityService) extractInboundContext(
	ctx context.Context,
	message binding.Message,
) context.Context {
	reader, ok := message.(binding.MessageMetadataReader)
	if !ok {
		return ctx
	}
	extracted, err := tracer.Extract(messageCarrier{reader: reader})
	if err != nil || extracted == nil {
		return ctx
	}
	return context.WithValue(ctx, extractedContextKey{}, extracted)
}

// RecordCallingInvoker starts a consumer span before the SDK calls the
// application receiver. It starts a new trace linked to the sending span because
// event delivery is asynchronous.
func (s *observabilityService) RecordCallingInvoker(
	ctx context.Context,
	event *cloudevents.Event,
) (context.Context, func(error)) {
	opts := s.spanOptions(instrumentation.ComponentConsumer, "process")
	extracted := extractedContext(ctx, event)
	if links := spanLinks(extracted); len(links) != 0 {
		opts = append(opts, tracer.WithSpanLinks(links))
	}

	span := tracer.StartSpan("cloudevents.process", opts...)
	if event != nil {
		s.tagEvent(span, event)
	} else {
		span.SetTag(ext.ResourceName, "unknown")
	}
	if extracted != nil {
		// Span links do not carry baggage, so copy it onto the new consumer root.
		extracted.ForeachBaggageItem(func(key, value string) bool {
			span.SetBaggageItem(key, value)
			return true
		})
	}
	return tracer.ContextWithSpan(ctx, span), func(result error) {
		finishSpan(span, result)
	}
}

// extractedContext reads propagation data from transport metadata, or from the
// decoded event when all metadata was serialized into the event body.
func extractedContext(ctx context.Context, event *cloudevents.Event) *tracer.SpanContext {
	extracted, _ := ctx.Value(extractedContextKey{}).(*tracer.SpanContext)
	if extracted != nil || event == nil {
		return extracted
	}

	// Structured messages keep extensions in the decoded event body instead of
	// exposing them through the original message metadata.
	extracted, _ = tracer.Extract(messageCarrier{reader: (*binding.EventMessage)(event)})
	return extracted
}

// spanLinks represents the sending trace as a link on the consumer span while
// preserving links already supplied by the configured propagator.
func spanLinks(extracted *tracer.SpanContext) []tracer.SpanLink {
	if extracted == nil {
		return nil
	}
	if (extracted.TraceIDLower() == 0 && extracted.TraceIDUpper() == 0) || extracted.SpanID() == 0 {
		return extracted.SpanLinks()
	}
	producer := tracer.SpanLink{
		TraceID:     extracted.TraceIDLower(),
		TraceIDHigh: extracted.TraceIDUpper(),
		SpanID:      extracted.SpanID(),
	}
	links := extracted.SpanLinks()
	for _, link := range links {
		if link.TraceID == producer.TraceID && link.TraceIDHigh == producer.TraceIDHigh && link.SpanID == producer.SpanID {
			return links
		}
	}
	return append(links, producer)
}

// RecordSendingEvent traces an event send and adds trace context to the outgoing
// event. The SDK reports the delivery result through the returned callback.
func (s *observabilityService) RecordSendingEvent(
	ctx context.Context,
	event cloudevents.Event,
) (context.Context, func(error)) {
	span, spanCtx := s.startProducerSpan(ctx, &event)
	return spanCtx, func(result error) {
		finishSpan(span, result)
	}
}

// RecordRequestEvent traces a request/reply operation and adds trace context to
// the outgoing event. The SDK reports the result through the returned callback.
func (s *observabilityService) RecordRequestEvent(
	ctx context.Context,
	event cloudevents.Event,
) (context.Context, func(error, *cloudevents.Event)) {
	span, spanCtx := s.startProducerSpan(ctx, &event)
	return spanCtx, func(result error, response *cloudevents.Event) {
		if isSuccess(result) && response == nil {
			result = errors.New("cloudevents: response conversion returned nil event")
		}
		finishSpan(span, result)
	}
}

// RecordReceivedMalformedEvent records a consumer error when an inbound message
// cannot be decoded as a CloudEvent.
func (s *observabilityService) RecordReceivedMalformedEvent(_ context.Context, err error) {
	span := tracer.StartSpan(
		"cloudevents.process",
		s.spanOptions(instrumentation.ComponentConsumer, "process")...,
	)
	span.SetTag(ext.ResourceName, "malformed")
	finishSpan(span, err)
}

// startProducerSpan creates the span shared by send and request operations and
// adds its propagation context to the event.
func (s *observabilityService) startProducerSpan(
	ctx context.Context,
	event *cloudevents.Event,
) (*tracer.Span, context.Context) {
	span, spanCtx := tracer.StartSpanFromContext(
		ctx,
		"cloudevents.send",
		s.spanOptions(instrumentation.ComponentProducer, "send")...,
	)
	s.tagEvent(span, event)
	// Propagation is best effort and must not prevent the SDK from sending the event.
	_ = tracer.Inject(span.Context(), eventCarrier{writer: event})
	return span, spanCtx
}

// spanOptions builds the Datadog messaging tags shared by send and receive spans.
func (s *observabilityService) spanOptions(
	component instrumentation.Component,
	operation string,
) []tracer.StartSpanOption {
	kind := ext.SpanKindProducer
	spanType := ext.SpanTypeMessageProducer
	if component == instrumentation.ComponentConsumer {
		kind = ext.SpanKindConsumer
		spanType = ext.SpanTypeMessageConsumer
	}
	opts := []tracer.StartSpanOption{
		tracer.SpanType(spanType),
		tracer.Tag(ext.SpanKind, kind),
		tracer.Tag(ext.Component, string(componentName)),
		tracer.Tag(ext.MessagingOperationName, operation),
		tracer.AnalyticsRate(instr.AnalyticsRate(false)),
	}
	opts = append(opts, instrumentation.ServiceNameWithSource(s.service, s.serviceSource))
	if s.messagingSystem != "" {
		opts = append(opts, tracer.Tag(ext.MessagingSystem, s.messagingSystem))
	}
	if s.destination != "" {
		opts = append(opts, tracer.Tag(ext.MessagingDestinationName, s.destination))
	}
	for key, value := range s.customTags {
		opts = append(opts, tracer.Tag(key, value))
	}
	return opts
}

// tagEvent records event-envelope metadata but never records the event payload.
// Subjects are included only when explicitly enabled.
func (s *observabilityService) tagEvent(span *tracer.Span, event *cloudevents.Event) {
	span.SetTag(ext.ResourceName, event.Type())
	span.SetTag("cloudevents.event_id", event.ID())
	span.SetTag("cloudevents.event_type", event.Type())
	span.SetTag("cloudevents.event_source", event.Source())
	span.SetTag("cloudevents.event_spec_version", event.SpecVersion())
	if v := event.DataContentType(); v != "" {
		span.SetTag("cloudevents.datacontenttype", v)
	}
	if s.recordSubject {
		if v := event.Subject(); v != "" {
			span.SetTag("cloudevents.event_subject", v)
		}
	}
}

// isSuccess reports whether the transport acknowledged the operation.
func isSuccess(result error) bool {
	return protocol.IsACK(result)
}

// finishSpan records an unsuccessful transport result as a span error.
func finishSpan(span *tracer.Span, result error) {
	if !isSuccess(result) {
		span.Finish(tracer.WithError(result))
		return
	}
	span.Finish()
}
