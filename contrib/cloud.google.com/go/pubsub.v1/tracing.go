// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

// Package tracing contains tracing logic for the cloud.google.com/go/pubsub.v1 instrumentation.
//
// WARNING: this package SHOULD NOT import cloud.google.com/go/pubsub.
//
// The motivation of this package is to support orchestrion, which cannot use the main package because it imports
// the cloud.google.com/go/pubsub package, and since orchestrion modifies the library code itself,
// this would cause an import cycle.
package pubsub

import (
	"context"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const componentName = instrumentation.PackageGCPPubsub

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageGCPPubsub)
}

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

func TracePublish(ctx context.Context, topic Topic, msg *Message, opts ...Option) (context.Context, func(serverID string, err error)) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt.apply(cfg)
	}
	spanOpts := []tracer.StartSpanOption{
		tracer.ResourceName(topic.String()),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag("message_size", len(msg.Data)),
		tracer.Tag("ordering_key", msg.OrderingKey),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemGCPPubsub),
	}
	if cfg.serviceName != "" {
		spanOpts = append(spanOpts, tracer.ServiceName(cfg.serviceName))
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
		instr.Logger().Debug("contrib/cloud.google.com/go/pubsub.v1/trace: failed injecting tracing attributes: %v", err)
	}
	span.SetTag("num_attributes", len(msg.Attributes))

	var once sync.Once
	closeSpan := func(serverID string, err error) {
		once.Do(func() {
			span.SetTag("server_id", serverID)
			span.Finish(tracer.WithError(err))
		})
	}
	return ctx, closeSpan
}

func TraceReceiveFunc(s Subscription, opts ...Option) func(ctx context.Context, msg *Message) (context.Context, func()) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt.apply(cfg)
	}
	instr.Logger().Debug("contrib/cloud.google.com/go/pubsub.v1/trace: Wrapping Receive Handler: %#v", cfg)
	return func(ctx context.Context, msg *Message) (context.Context, func()) {
		parentSpanCtx, _ := tracer.Extract(tracer.TextMapCarrier(msg.Attributes))
		opts := []tracer.StartSpanOption{
			tracer.ResourceName(s.String()),
			tracer.SpanType(ext.SpanTypeMessageConsumer),
			tracer.Tag("message_size", len(msg.Data)),
			tracer.Tag("num_attributes", len(msg.Attributes)),
			tracer.Tag("ordering_key", msg.OrderingKey),
			tracer.Tag("message_id", msg.ID),
			tracer.Tag("publish_time", msg.PublishTime.String()),
			tracer.Tag(ext.Component, componentName),
			tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
			tracer.Tag(ext.MessagingSystem, ext.MessagingSystemGCPPubsub),
			tracer.ChildOf(parentSpanCtx),
		}
		if cfg.serviceName != "" {
			opts = append(opts, tracer.ServiceName(cfg.serviceName))
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
