// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

// Package pubsub provides functions to trace the cloud.google.com/pubsub/go package.
package pubsub

import (
	"context"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"cloud.google.com/go/pubsub"
)

// Publish publishes a message on the specified topic and returns a PublishResult.
// This function is functionally equivalent to t.Publish(ctx, msg), but it also starts a publish
// span and it ensures that the datadog tracing metadata is propagated as attributes attached to
// the published message.
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

// Get wraps (pubsub.PublishResult).Get(ctx). When this function returns the publish span
// created in Publish is completed.
func (r *PublishResult) Get(ctx context.Context) (string, error) {
	serverID, err := r.PublishResult.Get(ctx)
	r.once.Do(func() {
		r.span.SetTag("server_id", serverID)
		r.span.Finish(tracer.WithError(err))
	})
	return serverID, err
}

// ReceiveTracer returns a receive callback that wraps the supplied callback, and extracts the datadog tracing metadata
// if it exists attached to the received message.
func ReceiveTracer(s *pubsub.Subscription, f func(context.Context, *pubsub.Message)) func(context.Context, *pubsub.Message) {
	return func(ctx context.Context, msg *pubsub.Message) {
		parentSpanCtx, _ := tracer.Extract(tracer.TextMapCarrier(msg.Attributes))
		span, ctx := tracer.StartSpanFromContext(
			ctx,
			"pubsub.receive",
			tracer.ResourceName(s.String()),
			tracer.SpanType(ext.SpanTypeMessageConsumer),
			tracer.Tag("message_size", len(msg.Data)),
			tracer.Tag("num_attributes", len(msg.Attributes)),
			tracer.Tag("ordering_key", msg.OrderingKey),
			tracer.Tag("message_id", msg.ID),
			tracer.Tag("publish_time", msg.PublishTime.String()),
			tracer.ChildOf(parentSpanCtx),
		)
		if msg.DeliveryAttempt != nil {
			span.SetTag("delivery_attempt", *msg.DeliveryAttempt)
		}
		defer span.Finish()
		f(ctx, msg)
	}
}
