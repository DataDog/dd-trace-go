// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.
// Author: CodapeWild (https://github.com/CodapeWild/)

package nsq

import (
	"context"
	"fmt"
	"math"

	"github.com/nsqio/go-nsq"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/tracer"
)

// HandlerWithSpanContext is a function adapter for nsq.Consumer.AddHandler
type HandlerWithSpanContext func(spctx ddtrace.SpanContext, message *nsq.Message) error

// HandleMessage adapte func(*nsq.Message)error to func(ddtrace.SpanContext, *nsq.Message)error
func (handler HandlerWithSpanContext) HandleMessage(message *nsq.Message) error {
	spctx, body, err := extract(message.Body)
	if err != nil {
		return err
	}
	message.Body = body

	return handler(spctx, message)
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
	consu.Consumer.AddHandler(HandlerWithSpanContext(func(spctx ddtrace.SpanContext, message *nsq.Message) error {
		var (
			span, _ = consu.startSpan(spctx, "consumer.Handler")
			err     = handler(spctx, message)
			stats   = consu.Stats()
			tags    = map[string]interface{}{
				"connections":      stats.Connections,
				"nsq_msg_received": stats.MessagesReceived,
				"nsq_msg_finished": stats.MessagesFinished,
				"nsq_msg_requeued": stats.MessagesRequeued,
				"starved":          consu.IsStarved(),
				"msg_id":           message.ID,
				"msg_attempts":     message.Attempts,
				"msg_body_size":    len(message.Body),
				"msg_timestamp":    message.Timestamp,
				"msg_src_nsqd":     message.NSQDAddress,
			}
		)
		consu.finishSpan(span, tags, err)

		return err
	}))
}

// AddConcurrentHandlers is a nsq.Consumer.AddConcurrentHandlers wrapper with tracing operations injected into the original registered handler.
func (consu *Consumer) AddConcurrentHandlers(handler HandlerWithSpanContext, concurrency int) {
	consu.Consumer.AddConcurrentHandlers(HandlerWithSpanContext(func(spctx ddtrace.SpanContext, message *nsq.Message) error {
		var (
			span, _ = consu.startSpan(spctx, "consumer.Handler")
			err     = handler(spctx, message)
			stats   = consu.Stats()
			tags    = map[string]interface{}{
				"connections":      stats.Connections,
				"nsq_msg_received": stats.MessagesReceived,
				"nsq_msg_finished": stats.MessagesFinished,
				"nsq_msg_requeued": stats.MessagesRequeued,
				"starved":          consu.IsStarved(),
				"msg_id":           message.ID,
				"msg_attempts":     message.Attempts,
				"msg_body_size":    len(message.Body),
				"msg_timestamp":    message.Timestamp,
				"msg_src_nsqd":     message.NSQDAddress,
				"concurrency":      concurrency,
			}
		)
		consu.finishSpan(span, tags, err)

		return err
	}), concurrency)
}

func (consu *Consumer) startSpan(spctx ddtrace.SpanContext, operation string) (tracer.Span, context.Context) {
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.ServiceName(consu.cfg.service),
		tracer.ResourceName(consu.resource),
		tracer.ChildOf(spctx),
	}
	if !math.IsNaN(consu.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, consu.cfg.analyticsRate))
	}

	return tracer.StartSpanFromContext(context.Background(), operation, opts...)
}

func (*Consumer) finishSpan(span tracer.Span, tags map[string]interface{}, err error) {
	for k, v := range tags {
		span.SetTag(k, v)
	}
	span.Finish(tracer.WithError(err))
}
