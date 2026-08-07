// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package cloudevents instruments the CloudEvents Go SDK. CloudEvents defines a
// common event envelope that can be transported over systems such as HTTP,
// Kafka, or Google Pub/Sub. This integration traces events as they are sent and
// received and propagates trace context in CloudEvent extension attributes.
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

// Option configures the CloudEvents observability service returned by New.
type Option func(*observabilityService)

// WithService sets the service name for spans created by the integration.
// By default, spans use the service configured on the tracer.
func WithService(name string) Option {
	return func(service *observabilityService) {
		service.service = name
	}
}

// WithMessagingSystem sets the underlying messaging system, such as "kafka".
// CloudEvents does not imply a messaging system, so no value is set by default.
func WithMessagingSystem(system string) Option {
	return func(service *observabilityService) {
		service.messagingSystem = system
	}
}

// WithDestinationName sets the name of the messaging destination, such as a
// topic or queue name.
func WithDestinationName(name string) Option {
	return func(service *observabilityService) {
		service.destination = name
	}
}

// WithSubject enables recording the CloudEvent subject on spans. The subject is
// omitted by default because it may contain sensitive or high-cardinality data.
func WithSubject() Option {
	return func(service *observabilityService) {
		service.recordSubject = true
	}
}

// New returns an observability service that the CloudEvents SDK calls around
// send, request, and receive operations. Install it on a client with
// client.WithObservabilityService.
//
// The service adds W3C trace context to outgoing event extensions. Callers that
// send the same event concurrently should clone it before each send.
func New(opts ...Option) client.ObservabilityService {
	service := &observabilityService{}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

type observabilityService struct {
	service         string
	messagingSystem string
	destination     string
	recordSubject   bool
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

	span := tracer.StartSpan(instr.OperationName(instrumentation.ComponentConsumer, nil), opts...)
	if event != nil {
		s.tagEvent(span, event)
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
	if links := extracted.SpanLinks(); len(links) != 0 {
		return links
	}
	if (extracted.TraceIDLower() == 0 && extracted.TraceIDUpper() == 0) || extracted.SpanID() == 0 {
		return nil
	}
	return []tracer.SpanLink{{
		TraceID:     extracted.TraceIDLower(),
		TraceIDHigh: extracted.TraceIDUpper(),
		SpanID:      extracted.SpanID(),
	}}
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
		instr.OperationName(instrumentation.ComponentConsumer, nil),
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
		instr.OperationName(instrumentation.ComponentProducer, nil),
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
	if s.service != "" {
		opts = append(opts, instrumentation.ServiceNameWithSource(s.service, instrumentation.ServiceSourceWithServiceOption))
	}
	if s.messagingSystem != "" {
		opts = append(opts, tracer.Tag(ext.MessagingSystem, s.messagingSystem))
	}
	if s.destination != "" {
		opts = append(opts, tracer.Tag(ext.MessagingDestinationName, s.destination))
	}
	return opts
}

// tagEvent records event-envelope metadata but never records the event payload.
// Subjects are included only when explicitly enabled.
func (s *observabilityService) tagEvent(span *tracer.Span, event *cloudevents.Event) {
	span.SetTag(ext.ResourceName, event.Type())
	span.SetTag("cloudevents.id", event.ID())
	span.SetTag("cloudevents.type", event.Type())
	span.SetTag("cloudevents.source", event.Source())
	span.SetTag("cloudevents.specversion", event.SpecVersion())
	if v := event.DataContentType(); v != "" {
		span.SetTag("cloudevents.datacontenttype", v)
	}
	if s.recordSubject {
		if v := event.Subject(); v != "" {
			span.SetTag("cloudevents.subject", v)
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
