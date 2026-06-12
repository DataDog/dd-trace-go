// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package pubsubtrace contains tracing logic for the cloud.google.com/go/pubsub instrumentation.
//
// WARNING: this package SHOULD NOT import cloud.google.com/go/pubsub or cloud.google.com/go/pubsub/v2.
//
// The motivation of this package is to support orchestrion, which cannot use the main package because it imports
// the package, and since orchestrion modifies the library code itself,
// this would cause an import cycle.
package pubsubtrace

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type Message struct {
	ID              string
	Data            []byte
	OrderingKey     string
	Attributes      map[string]string
	DeliveryAttempt *int
	PublishTime     time.Time
}

type Topic interface {
	String() string
}

type Subscription interface {
	String() string
}

type Tracer struct {
	instr     *instrumentation.Instrumentation
	component instrumentation.Package
}

func NewTracer(instr *instrumentation.Instrumentation, componentName instrumentation.Package) *Tracer {
	return &Tracer{
		instr:     instr,
		component: componentName,
	}
}

func (tr *Tracer) TracePublish(ctx context.Context, topic Topic, msg *Message, opts ...Option) (context.Context, func(serverID string, err error)) {
	cfg := tr.defaultConfig()
	for _, opt := range opts {
		opt.apply(cfg)
	}
	spanOpts := []tracer.StartSpanOption{
		tracer.ResourceName(topic.String()),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag("message_size", len(msg.Data)),
		tracer.Tag("ordering_key", msg.OrderingKey),
		tracer.Tag(ext.Component, tr.component),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemGCPPubsub),
		tracer.Tag(ext.MessagingOperationName, "send"),
		tracer.Tag(ext.MessagingDestinationName, topic.String()),
	}
	if projectID := projectIDFromResourceName(topic.String()); projectID != "" {
		spanOpts = append(spanOpts, tracer.Tag("gcloud.project_id", projectID))
	}
	if cfg.serviceName != "" {
		spanOpts = append(spanOpts, instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource))
	}
	if cfg.measured {
		spanOpts = append(spanOpts, tracer.Measured())
	}
	span, ctx := tracer.StartSpanFromContext(
		ctx,
		cfg.publishSpanName,
		spanOpts...,
	)
	if msg.Attributes == nil {
		msg.Attributes = make(map[string]string)
	}
	if err := tracer.Inject(span.Context(), tracer.TextMapCarrier(msg.Attributes)); err != nil {
		tr.instr.Logger().Debug("contrib/cloud.google.com/go/pubsubtrace: failed injecting tracing attributes: %s", err.Error())
	}
	span.SetTag("num_attributes", len(msg.Attributes))

	var once sync.Once
	closeSpan := func(serverID string, err error) {
		once.Do(func() {
			span.SetTag("server_id", serverID)
			span.SetTag(ext.MessagingMessageID, serverID)
			span.Finish(tracer.WithError(err))
		})
	}
	return ctx, closeSpan
}

func (tr *Tracer) TraceReceiveFunc(s Subscription, opts ...Option) func(ctx context.Context, msg *Message) (context.Context, func()) {
	cfg := tr.defaultConfig()
	for _, opt := range opts {
		opt.apply(cfg)
	}
	tr.instr.Logger().Debug("contrib/cloud.google.com/go/pubsubtrace: Wrapping Receive Handler: %#v", cfg)
	return func(ctx context.Context, msg *Message) (context.Context, func()) {
		parentSpanCtx, _ := tracer.Extract(tracer.TextMapCarrier(msg.Attributes))
		opts := []tracer.StartSpanOption{
			tracer.ResourceName(s.String()),
			tracer.SpanType(ext.SpanTypeMessageConsumer),
			tracer.Tag("message_size", len(msg.Data)),
			tracer.Tag("num_attributes", len(msg.Attributes)),
			tracer.Tag("ordering_key", msg.OrderingKey),
			tracer.Tag("message_id", msg.ID),
			tracer.Tag(ext.MessagingMessageID, msg.ID),
			tracer.Tag("publish_time", msg.PublishTime.String()),
			tracer.Tag(ext.Component, tr.component),
			tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
			tracer.Tag(ext.MessagingSystem, ext.MessagingSystemGCPPubsub),
			tracer.Tag(ext.MessagingOperationName, "receive"),
			tracer.Tag(ext.MessagingDestinationName, s.String()),
			tracer.ChildOf(parentSpanCtx),
		}
		if projectID := projectIDFromResourceName(s.String()); projectID != "" {
			opts = append(opts, tracer.Tag("gcloud.project_id", projectID))
		}
		if cfg.serviceName != "" {
			opts = append(opts, instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource))
		}
		if cfg.measured {
			opts = append(opts, tracer.Measured())
		}
		// If there are span links as a result of context extraction, add them as a StartSpanOption
		if parentSpanCtx != nil && parentSpanCtx.SpanLinks() != nil {
			opts = append(opts, tracer.WithSpanLinks(parentSpanCtx.SpanLinks()))
		}
		span, ctx := tracer.StartSpanFromContext(ctx, cfg.receiveSpanName, opts...)
		if msg.DeliveryAttempt != nil {
			span.SetTag("delivery_attempt", *msg.DeliveryAttempt)
		}
		return ctx, func() { span.Finish() }
	}
}

// extracts the GCP project ID from a Pubsub resource name of the form
// "projects/{project}/topics/{topic}" or "projects/{project}/subscriptions/{subscription}"
func projectIDFromResourceName(name string) string {
	const prefix = "projects/"
	if !strings.HasPrefix(name, prefix) {
		return ""
	}
	rest := name[len(prefix):]
	project, _, _ := strings.Cut(rest, "/")
	return project
}
