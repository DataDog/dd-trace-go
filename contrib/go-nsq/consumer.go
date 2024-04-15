// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.
// Author: DataDog (https://github.com/DataDog/)

package nsq

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/nsqio/go-nsq"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type spanContextKey struct{}

var activeSpnCtxKey = spanContextKey{}

// HandlerWithSpanContext is a function adapter for nsq.Consumer.AddHandler
type HandlerWithSpanContext func(ctx context.Context, message *nsq.Message) error

// HandleMessage adapte func(*nsq.Message)error to func(ddtrace.SpanContext, *nsq.Message)error
func (handler HandlerWithSpanContext) HandleMessage(message *nsq.Message) error {
	spnctx, body, err := extract(message.Body)
	if err != nil {
		return err
	}
	message.Body = body

	return handler(context.WithValue(context.Background(), activeSpnCtxKey, spnctx), message)
}

// Consumer is a wrap-up class of nsq Consumer.
type Consumer struct {
	resource string
	*nsq.Consumer
	cfg *clientConfig
}

// NewConsumer return a new nsq Consumer wrapped with tracing.
func NewConsumer(topic string, channel string, config *nsq.Config, opts ...Option) (*Consumer, error) {
	consu, err := nsq.NewConsumer(topic, channel, config)
	if err != nil {
		return nil, err
	}

	cfg := &clientConfig{}
	defaultConfig(cfg)
	for _, opt := range opts {
		opt(cfg)
	}

	return &Consumer{
		resource: fmt.Sprintf("%s:%s", topic, channel),
		Consumer: consu,
		cfg:      cfg,
	}, nil
}

// AddHandler is a nsq.Consumer.Addhandler wrapper with tracing operations injected into the original registered handler.
func (consu *Consumer) AddHandler(handler HandlerWithSpanContext) {
	consu.Consumer.AddHandler(HandlerWithSpanContext(func(ctx context.Context, message *nsq.Message) error {
		var err error
		span, ctxWithSpan := consu.startSpan(ctx, "consumer.Handler")
		defer span.Finish(tracer.WithError(err))

		stats := consu.Stats()
		span.SetTag("connections", stats.Connections)
		span.SetTag("nsq_msg_received", stats.MessagesReceived)
		span.SetTag("nsq_msg_finished", stats.MessagesFinished)
		span.SetTag("nsq_msg_requeued", stats.MessagesRequeued)
		span.SetTag("starved", consu.IsStarved())
		span.SetTag("msg_id", string(message.ID[:]))
		span.SetTag("msg_attempts", message.Attempts)
		span.SetTag("msg_body_size", len(message.Body))
		span.SetTag("msg_timestamp", message.Timestamp)
		span.SetTag("msg_src_nsqd", message.NSQDAddress)
		span.SetTag("dequeue_timestamp", time.Now().UnixNano())

		err = handler(ctxWithSpan, message)

		return err
	}))
}

// AddConcurrentHandlers is a nsq.Consumer.AddConcurrentHandlers wrapper with tracing operations injected into the original registered handler.
func (consu *Consumer) AddConcurrentHandlers(handler HandlerWithSpanContext, concurrency int) {
	consu.Consumer.AddConcurrentHandlers(HandlerWithSpanContext(func(ctx context.Context, message *nsq.Message) error {
		var err error
		span, ctxWithSpan := consu.startSpan(ctx, "consumer.ConcurrentHandler")
		defer span.Finish(tracer.WithError(err))

		stats := consu.Stats()
		span.SetTag("connections", stats.Connections)
		span.SetTag("nsq_msg_received", stats.MessagesReceived)
		span.SetTag("nsq_msg_finished", stats.MessagesFinished)
		span.SetTag("nsq_msg_requeued", stats.MessagesRequeued)
		span.SetTag("starved", consu.IsStarved())
		span.SetTag("msg_id", string(message.ID[:]))
		span.SetTag("msg_attempts", message.Attempts)
		span.SetTag("msg_body_size", len(message.Body))
		span.SetTag("msg_timestamp", message.Timestamp)
		span.SetTag("msg_src_nsqd", message.NSQDAddress)
		span.SetTag("concurrency", concurrency)
		span.SetTag("dequeue_timestamp", time.Now().UnixNano())

		err = handler(ctxWithSpan, message)

		return err
	}), concurrency)
}

func (consu *Consumer) startSpan(ctx context.Context, operation string) (tracer.Span, context.Context) {
	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(consu.cfg.service),
		tracer.ResourceName(consu.resource),
		tracer.SpanType(ext.SpanTypeMessageConsumer),
	}
	if spnctx, ok := ctx.Value(activeSpnCtxKey).(ddtrace.SpanContext); ok {
		opts = append(opts, tracer.ChildOf(spnctx))
	}
	if !math.IsNaN(consu.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, consu.cfg.analyticsRate))
	}

	return tracer.StartSpanFromContext(ctx, operation, opts...)
}
