// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.
// Author: CodapeWild (https://github.com/CodapeWild/)

package nsq

import (
	"context"
	"fmt"
	"time"

	"github.com/nsqio/go-nsq"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/tracer"
)

type spanContextKey struct{}

var activeSpnCtxKey = spanContextKey{}

// HandlerWithContext is a function adapter for nsq.Consumer.AddHandler
type HandlerWithContext func(ctx context.Context, message *nsq.Message) error

// HandleMessage adapte func(*nsq.Message)error to func(ddtrace.SpanContext, *nsq.Message)error
func (handler HandlerWithContext) HandleMessage(message *nsq.Message) error {
	spnctx, body, err := extract(message.Body)
	if err != nil {
		return err
	}
	message.Body = body

	return handler(context.WithValue(context.Background(), activeSpnCtxKey, spnctx), message)
}

// Consumer is a wrap-up class of nsq Consumer.
type Consumer struct {
	c      *nsq.Consumer
	nsqcfg *nsq.Config
	ccfg   *clientConfig
	res    string
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
		c:      consu,
		nsqcfg: config,
		ccfg:   cfg,
		res:    fmt.Sprintf("%s:%s", topic, channel),
	}, nil
}

// AddHandler is a nsq.Consumer.Addhandler wrapper with tracing operations injected into the original registered handler.
func (consu *Consumer) AddHandler(handler HandlerWithContext) {
	funcName := getFuncName(handler)
	consu.c.AddHandler(HandlerWithContext(func(ctx context.Context, message *nsq.Message) error {
		var err error
		span, ctxWithSpan := startSpanFromContext(ctx, consu.ccfg, consu.nsqcfg, consu.res, funcName)
		defer span.Finish(tracer.WithError(err))

		stats := consu.c.Stats()
		span.SetTag(Connections, stats.Connections)
		span.SetTag(MsgReceived, stats.MessagesReceived)
		span.SetTag(MsgFinished, stats.MessagesFinished)
		span.SetTag(MsgRequeued, stats.MessagesRequeued)
		span.SetTag(IsStarved, consu.c.IsStarved())
		span.SetTag(MsgID, string(message.ID[:]))
		span.SetTag(MsgSize, len(message.Body))
		span.SetTag(MsgAttempts, message.Attempts)
		span.SetTag(MsgTimestamp, message.Timestamp)
		span.SetTag(MsgSrcNSQD, message.NSQDAddress)
		span.SetTag(DequeueTime, time.Now().UnixNano())

		err = handler(ctxWithSpan, message)

		return err
	}))
}

// AddConcurrentHandlers is a nsq.Consumer.AddConcurrentHandlers wrapper with tracing operations injected into the original registered handler.
func (consu *Consumer) AddConcurrentHandlers(handler HandlerWithContext, concurrency int) {
	funcName := getFuncName(handler)
	consu.c.AddConcurrentHandlers(HandlerWithContext(func(ctx context.Context, message *nsq.Message) error {
		var err error
		span, ctxWithSpan := startSpanFromContext(ctx, consu.ccfg, consu.nsqcfg, consu.res, funcName)
		defer span.Finish(tracer.WithError(err))

		stats := consu.c.Stats()
		span.SetTag(Connections, stats.Connections)
		span.SetTag(MsgReceived, stats.MessagesReceived)
		span.SetTag(MsgFinished, stats.MessagesFinished)
		span.SetTag(MsgRequeued, stats.MessagesRequeued)
		span.SetTag(IsStarved, consu.c.IsStarved())
		span.SetTag(MsgID, string(message.ID[:]))
		span.SetTag(MsgSize, len(message.Body))
		span.SetTag(MsgAttempts, message.Attempts)
		span.SetTag(MsgTimestamp, message.Timestamp)
		span.SetTag(MsgSrcNSQD, message.NSQDAddress)
		span.SetTag(DequeueTime, time.Now().UnixNano())
		span.SetTag(Concurrency, concurrency)

		err = handler(ctxWithSpan, message)

		return err
	}), concurrency)
}
