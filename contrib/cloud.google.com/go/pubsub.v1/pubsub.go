// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package pubsub provides functions to trace the cloud.google.com/pubsub/go package.
package pubsub

import (
	"context"

	v2 "github.com/DataDog/dd-trace-go/contrib/cloud.google.com/go/pubsub.v1/v2"

	"cloud.google.com/go/pubsub"
)

// Publish publishes a message on the specified topic and returns a PublishResult.
// This function is functionally equivalent to t.Publish(ctx, msg), but it also starts a publish
// span and it ensures that the tracing metadata is propagated as attributes attached to
// the published message.
// It is required to call (*PublishResult).Get(ctx) on the value returned by Publish to complete
// the span.
func Publish(ctx context.Context, t *pubsub.Topic, msg *pubsub.Message, opts ...Option) *PublishResult {
	return v2.Publish(ctx, t, msg, opts...)
}

// PublishResult wraps *pubsub.PublishResult
type PublishResult = v2.PublishResult

// WrapReceiveHandler returns a receive handler that wraps the supplied handler,
// extracts any tracing metadata attached to the received message, and starts a
// receive span.
func WrapReceiveHandler(s *pubsub.Subscription, f func(context.Context, *pubsub.Message), opts ...Option) func(context.Context, *pubsub.Message) {
	return v2.WrapReceiveHandler(s, f, opts...)
}
