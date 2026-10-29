// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build ignore

// This file is never compiled as part of this package. The add_file rule in
// otelc.yaml copies it into cloud.google.com/go/pubsub during an otelc build,
// where Topic, Subscription and Message exist. otelc rewrites the package
// clause and drops the build constraint on the way in.
package otelc

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/contrib/cloud.google.com/go/pubsubtrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

//revive:disable:var-naming

var __dd_pubsubTracer = pubsubtrace.NewTracer(
	instrumentation.Load(instrumentation.PackageGCPPubsub),
	instrumentation.PackageGCPPubsub,
)

// __dd_pubsubTracePublish starts the publish span of (*Topic).Publish. It
// returns a nil closer when it starts no span: for a nil message, left for
// Publish to handle, and for a publish the contrib already traces.
func __dd_pubsubTracePublish(ctx context.Context, t *Topic, msg *Message) (context.Context, func(string, error)) {
	if msg == nil || pubsubtrace.PublishTraced(ctx) {
		return ctx, nil
	}
	traceMsg := __dd_pubsubTraceMessage(msg)
	ctx, closeSpan := __dd_pubsubTracer.TracePublish(ctx, t, traceMsg)
	msg.Attributes = traceMsg.Attributes
	return ctx, closeSpan
}

// __dd_pubsubWrapReceiveHandler wraps the handler of (*Subscription).Receive
// so that each message gets a receive span.
func __dd_pubsubWrapReceiveHandler(s *Subscription, f func(context.Context, *Message)) func(context.Context, *Message) {
	if f == nil {
		return f
	}
	traceFn := __dd_pubsubTracer.TraceReceiveFunc(s)
	return func(ctx context.Context, msg *Message) {
		ctx, closeSpan := traceFn(ctx, __dd_pubsubTraceMessage(msg))
		defer closeSpan()
		f(ctx, msg)
	}
}

func __dd_pubsubTraceMessage(msg *Message) *pubsubtrace.Message {
	return &pubsubtrace.Message{
		ID:              msg.ID,
		Data:            msg.Data,
		OrderingKey:     msg.OrderingKey,
		Attributes:      msg.Attributes,
		DeliveryAttempt: msg.DeliveryAttempt,
		PublishTime:     msg.PublishTime,
	}
}
