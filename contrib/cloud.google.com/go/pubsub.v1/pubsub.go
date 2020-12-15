// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

// Package pubsub provides functions to trace the cloud.google.com/pubsub/go package.
package pubsub

import (
	"context"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"cloud.google.com/go/pubsub"
)

// Publish publishes a message on the specified topic and returns a PublishResult.
// This function is functionally equivalent to t.Publish(ctx, msg), but it also starts a publish
// span and it ensures that the tracing metadata is propagated as attributes attached to
// the published message.
// It is required to call (*PublishResult).Get(ctx) on the value returned by Publish to complete
// the span.
func Publish(ctx context.Context, t *pubsub.Topic, msg *pubsub.Message) *PublishResult {
	span, ctx := tracer.StartSpanFromContext(
		ctx,
		"pubsub.publish",
		tracer.ResourceName(t.String()),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag("message_size", len(msg.Data)),
		tracer.Tag("ordering_key", msg.OrderingKey),
	)
	if msg.Attributes == nil {
		msg.Attributes = make(map[string]string)
	}
	if err := tracer.Inject(span.Context(), tracer.TextMapCarrier(msg.Attributes)); err != nil {
		log.Debug("contrib/cloud.google.com/go/pubsub.v1/: failed injecting tracing attributes: %v", err)
	}
	span.SetTag("num_attributes", len(msg.Attributes))
	return &PublishResult{
		PublishResult: t.Publish(ctx, msg),
		span:          span,
	}
}

// PublishResult wraps *pubsub.PublishResult
type PublishResult struct {
	*pubsub.PublishResult
	once sync.Once
	span tracer.Span
}

// Get wraps (pubsub.PublishResult).Get(ctx). When this function returns the publish
// span created in Publish is completed.
func (r *PublishResult) Get(ctx context.Context) (string, error) {
	serverID, err := r.PublishResult.Get(ctx)
	r.once.Do(func() {
		r.span.SetTag("server_id", serverID)
		r.span.Finish(tracer.WithError(err))
	})
	return serverID, err
}

type config struct {
	serviceName string
}

// A ReceiveOption is used to customize spans started by WrapReceiveHandler.
type ReceiveOption func(cfg *config)

// WithServiceName sets the service name tag for traces started by WrapReceiveHandler.
func WithServiceName(serviceName string) ReceiveOption {
	return func(cfg *config) {
		cfg.serviceName = serviceName
	}
}

// WrapReceiveHandler returns a receive handler that wraps the supplied handler,
// extracts any tracing metadata attached to the received message, and starts a
// receive span.
func WrapReceiveHandler(s *pubsub.Subscription, f func(context.Context, *pubsub.Message), opts ...ReceiveOption) func(context.Context, *pubsub.Message) {
	var cfg config
	for _, opt := range opts {
		opt(&cfg)
	}
	return func(ctx context.Context, msg *pubsub.Message) {
		parentSpanCtx, _ := tracer.Extract(tracer.TextMapCarrier(msg.Attributes))
		opts := []ddtrace.StartSpanOption{
			tracer.ResourceName(s.String()),
			tracer.SpanType(ext.SpanTypeMessageConsumer),
			tracer.Tag("message_size", len(msg.Data)),
			tracer.Tag("num_attributes", len(msg.Attributes)),
			tracer.Tag("ordering_key", msg.OrderingKey),
			tracer.Tag("message_id", msg.ID),
			tracer.Tag("publish_time", msg.PublishTime.String()),
			tracer.ChildOf(parentSpanCtx),
		}
		if cfg.serviceName != "" {
			opts = append(opts, tracer.ServiceName(cfg.serviceName))
		}
		span, ctx := tracer.StartSpanFromContext(ctx, "pubsub.receive", opts...)
		if msg.DeliveryAttempt != nil {
			span.SetTag("delivery_attempt", *msg.DeliveryAttempt)
		}
		defer span.Finish()
		f(ctx, msg)
	}
}
