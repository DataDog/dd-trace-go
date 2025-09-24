// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package pubsub provides functions to trace the cloud.google.com/pubsub/go package.
package pubsub

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/contrib/cloud.google.com/go/pubsubtrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"cloud.google.com/go/pubsub"
)

const componentName = instrumentation.PackageGCPPubsub

var (
	instr   *instrumentation.Instrumentation
	pstrace *pubsubtrace.Tracer
)

func init() {
	instr = instrumentation.Load(componentName)
	pstrace = pubsubtrace.NewTracer(instr, componentName)
}

// Publish publishes a message on the specified topic and returns a PublishResult.
// This function is functionally equivalent to t.Publish(ctx, msg), but it also starts a publish
// span and it ensures that the tracing metadata is propagated as attributes attached to
// the published message.
// It is required to call (*PublishResult).Get(ctx) on the value returned by Publish to complete
// the span.
func Publish(ctx context.Context, t *pubsub.Topic, msg *pubsub.Message, opts ...Option) *PublishResult {
	traceMsg := newTraceMessage(msg)
	ctx, closeSpan := pstrace.TracePublish(ctx, t, traceMsg, opts...)
	msg.Attributes = traceMsg.Attributes

	return &PublishResult{
		PublishResult: t.Publish(ctx, msg),
		closeSpan:     closeSpan,
	}
}

// PublishResult wraps *pubsub.PublishResult
type PublishResult struct {
	*pubsub.PublishResult
	closeSpan func(serverID string, err error)
}

// Get wraps (pubsub.PublishResult).Get(ctx). When this function returns the publish
// span created in Publish is completed.
func (r *PublishResult) Get(ctx context.Context) (string, error) {
	serverID, err := r.PublishResult.Get(ctx)
	r.closeSpan(serverID, err)
	return serverID, err
}

// WrapReceiveHandler returns a receive handler that wraps the supplied handler,
// extracts any tracing metadata attached to the received message, and starts a
// receive span.
func WrapReceiveHandler(s *pubsub.Subscription, f func(context.Context, *pubsub.Message), opts ...Option) func(context.Context, *pubsub.Message) {
	traceFn := pstrace.TraceReceiveFunc(s, opts...)
	return func(ctx context.Context, msg *pubsub.Message) {
		ctx, closeSpan := traceFn(ctx, newTraceMessage(msg))
		defer closeSpan()
		f(ctx, msg)
	}
}

func newTraceMessage(msg *pubsub.Message) *pubsubtrace.Message {
	if msg == nil {
		return nil
	}
	return &pubsubtrace.Message{
		ID:              msg.ID,
		Data:            msg.Data,
		OrderingKey:     msg.OrderingKey,
		Attributes:      msg.Attributes,
		DeliveryAttempt: msg.DeliveryAttempt,
		PublishTime:     msg.PublishTime,
	}
}
