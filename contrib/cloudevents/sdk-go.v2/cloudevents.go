// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package cloudevents traces github.com/cloudevents/sdk-go/v2 clients.
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

// Option configures the observability service returned by New.
type Option func(*observabilityService)

// WithService sets the service name for CloudEvents spans.
func WithService(name string) Option { return func(s *observabilityService) { s.service = name } }

// WithMessagingSystem sets the messaging system tag (for example, "kafka").
func WithMessagingSystem(system string) Option {
	return func(s *observabilityService) { s.messagingSystem = system }
}

// WithDestinationName sets the messaging destination name tag.
func WithDestinationName(name string) Option {
	return func(s *observabilityService) { s.destination = name }
}

// WithSubject enables recording the CloudEvent subject on spans.
func WithSubject() Option { return func(s *observabilityService) { s.subject = true } }

// New returns a Datadog implementation of client.ObservabilityService.
// Tracing updates the event's propagation extensions, so callers must not send
// the same event concurrently. Clone the event first when isolation is needed.
//
// CloudEvents SDK v2.16.2 does not invoke observability completion callbacks
// for undelivered Request calls. Such request spans cannot finish until that
// behavior is fixed upstream.
func New(opts ...Option) client.ObservabilityService {
	s := &observabilityService{}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type observabilityService struct {
	service, messagingSystem, destination string
	subject                               bool
}

var _ client.ObservabilityService = (*observabilityService)(nil)

type extractedContextKey struct{}

func (s *observabilityService) InboundContextDecorators() []func(context.Context, binding.Message) context.Context {
	return []func(context.Context, binding.Message) context.Context{func(ctx context.Context, msg binding.Message) context.Context {
		reader, ok := msg.(binding.MessageMetadataReader)
		if !ok {
			return ctx
		}
		extracted, err := tracer.Extract(messageCarrier{reader})
		if err != nil || extracted == nil {
			return ctx
		}
		return context.WithValue(ctx, extractedContextKey{}, extracted)
	}}
}

func (s *observabilityService) RecordCallingInvoker(ctx context.Context, event *cloudevents.Event) (context.Context, func(error)) {
	opts := s.spanOptions(instrumentation.ComponentConsumer, "process")
	extracted, _ := ctx.Value(extractedContextKey{}).(*tracer.SpanContext)
	if extracted == nil && event != nil {
		// Structured messages may carry extensions in the decoded event body
		// instead of exposing them through the original message metadata.
		extracted, _ = tracer.Extract(messageCarrier{(*binding.EventMessage)(event)})
	}
	if extracted != nil {
		links := extracted.SpanLinks()
		if len(links) == 0 && (extracted.TraceIDLower() != 0 || extracted.TraceIDUpper() != 0) && extracted.SpanID() != 0 {
			links = []tracer.SpanLink{{TraceID: extracted.TraceIDLower(), TraceIDHigh: extracted.TraceIDUpper(), SpanID: extracted.SpanID()}}
		}
		if len(links) != 0 {
			opts = append(opts, tracer.WithSpanLinks(links))
		}
	}
	span := tracer.StartSpan(instr.OperationName(instrumentation.ComponentConsumer, nil), opts...)
	if event != nil {
		s.tagEvent(span, event)
	}
	if extracted != nil {
		extracted.ForeachBaggageItem(func(k, v string) bool { span.SetBaggageItem(k, v); return true })
	}
	return tracer.ContextWithSpan(ctx, span), func(result error) { finish(span, result) }
}

func (s *observabilityService) RecordSendingEvent(ctx context.Context, event cloudevents.Event) (context.Context, func(error)) {
	span, spanCtx := tracer.StartSpanFromContext(ctx, instr.OperationName(instrumentation.ComponentProducer, nil), s.spanOptions(instrumentation.ComponentProducer, "send")...)
	s.tagEvent(span, &event)
	_ = tracer.Inject(span.Context(), eventCarrier{&event})
	return spanCtx, func(result error) { finish(span, result) }
}

func (s *observabilityService) RecordRequestEvent(ctx context.Context, event cloudevents.Event) (context.Context, func(error, *cloudevents.Event)) {
	span, spanCtx := tracer.StartSpanFromContext(ctx, instr.OperationName(instrumentation.ComponentProducer, nil), s.spanOptions(instrumentation.ComponentProducer, "send")...)
	s.tagEvent(span, &event)
	_ = tracer.Inject(span.Context(), eventCarrier{&event})
	return spanCtx, func(result error, response *cloudevents.Event) {
		if isSuccess(result) && response == nil {
			result = errors.New("cloudevents: response conversion returned nil event")
		}
		finish(span, result)
	}
}

func (s *observabilityService) RecordReceivedMalformedEvent(_ context.Context, err error) {
	span := tracer.StartSpan(instr.OperationName(instrumentation.ComponentConsumer, nil), s.spanOptions(instrumentation.ComponentConsumer, "process")...)
	span.SetTag(ext.ResourceName, "malformed")
	finish(span, err)
}

func (s *observabilityService) spanOptions(component instrumentation.Component, operation string) []tracer.StartSpanOption {
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

func (s *observabilityService) tagEvent(span *tracer.Span, event *cloudevents.Event) {
	span.SetTag(ext.ResourceName, event.Type())
	span.SetTag("cloudevents.id", event.ID())
	span.SetTag("cloudevents.type", event.Type())
	span.SetTag("cloudevents.source", event.Source())
	span.SetTag("cloudevents.specversion", event.SpecVersion())
	if v := event.DataContentType(); v != "" {
		span.SetTag("cloudevents.datacontenttype", v)
	}
	if s.subject {
		if v := event.Subject(); v != "" {
			span.SetTag("cloudevents.subject", v)
		}
	}
}

func isSuccess(result error) bool { return protocol.IsACK(result) }
func finish(span *tracer.Span, result error) {
	if !isSuccess(result) {
		span.Finish(tracer.WithError(result))
		return
	}
	span.Finish()
}
