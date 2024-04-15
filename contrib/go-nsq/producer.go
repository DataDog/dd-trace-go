// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.
// Author: DataDog (https://github.com/DataDog/)

package nsq

import (
	"context"
	"math"
	"time"

	"github.com/nsqio/go-nsq"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// Producer is a wrap-up class of nsq Producer.
type Producer struct {
	*nsq.Producer
	cfg *clientConfig
}

// NewProducer return a new wrapped nsq Producer that is traced with the configurable client with opts.
func NewProducer(addr string, config *nsq.Config, opts ...Option) (*Producer, error) {
	prodc, err := nsq.NewProducer(addr, config)
	if err != nil {
		return nil, err
	}

	cfg := &clientConfig{}
	defaultConfig(cfg)
	for _, opt := range opts {
		opt(cfg)
	}

	return &Producer{
		Producer: prodc,
		cfg:      cfg,
	}, nil
}

// Publish is a nsq.Producer.Publish wrapper with tracing.
func (prodc *Producer) Publish(topic string, body []byte) error {
	return prodc.PublishWithContext(context.Background(), topic, body)
}

// PublishWithContext starts span with given context and wrap the nsq.Producer.Publish
func (prodc *Producer) PublishWithContext(ctx context.Context, topic string, body []byte) error {
	var err error
	span, ctx := prodc.startSpan(ctx, topic, "Publish")
	defer span.Finish(tracer.WithError(err))

	var injectedBody []byte
	if injectedBody, err = inject(span, body); err != nil {
		return err
	}

	span.SetTag("body_count", 1)
	span.SetTag("body_size", len(body))

	if err = prodc.Producer.Publish(topic, injectedBody); err == nil {
		span.SetTag("enqueue_timestamp", time.Now().UnixNano())
	}

	return err
}

// MultiPublish is a nsq Producer MultiPublish wrapper with tracing.
func (prodc *Producer) MultiPublish(topic string, body [][]byte) error {
	return prodc.MultiPublishWithContext(context.Background(), topic, body)
}

// MultiPublishWithContext starts span with given context and wrap the nsq.Producer.MultiPublish
func (prodc *Producer) MultiPublishWithContext(ctx context.Context, topic string, body [][]byte) error {
	var err error
	span, ctx := prodc.startSpan(ctx, topic, "MultiPlulish")
	defer span.Finish(tracer.WithError(err))

	injectedBody := make([][]byte, len(body))
	for i := range body {
		if injectedBody[i], err = inject(span, body[i]); err != nil {
			return err
		}
	}

	span.SetTag("body_count", len(body))
	span.SetTag("body_size", bodySize(body))

	if err = prodc.Producer.MultiPublish(topic, injectedBody); err == nil {
		span.SetTag("enqueue_timestamp", time.Now().UnixNano())
	}

	return err
}

// PublishAsync is a nsq Producer PublishAsync wrapper with tracing.
func (prodc *Producer) PublishAsync(topic string, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	return prodc.PublishAsyncWithContext(context.Background(), topic, body, doneChan, args...)
}

// PublishAsyncWithContext starts span with given context and wrap the nsq.Producer.PublishAsync
func (prodc *Producer) PublishAsyncWithContext(ctx context.Context, topic string, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	var err error
	span, ctx := prodc.startSpan(ctx, topic, "PublishAsync")
	defer span.Finish(tracer.WithError(err))

	var injectedBody []byte
	if injectedBody, err = inject(span, body); err != nil {
		return err
	}

	span.SetTag("body_count", 1)
	span.SetTag("body_size", len(body))
	span.SetTag("is_done_chan_nil", doneChan == nil)

	if err = prodc.Producer.PublishAsync(topic, injectedBody, doneChan, args...); err == nil {
		span.SetTag("enqueue_timestamp", time.Now().UnixNano())
	}

	return err
}

// MultiPublishAsync is a nsq Producer MultiPublishAsync wrapper with tracing.
func (prodc *Producer) MultiPublishAsync(topic string, body [][]byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	return prodc.MultiPublishAsyncWithContext(context.Background(), topic, body, doneChan, args...)
}

// MultiPublishAsyncWithContext starts span with given context and wrap the nsq.Producer.MultiPublishAsync
func (prodc *Producer) MultiPublishAsyncWithContext(ctx context.Context, topic string, body [][]byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	var err error
	span, ctx := prodc.startSpan(ctx, topic, "MultiPublishAsync")
	defer span.Finish(tracer.WithError(err))

	injectedBody := make([][]byte, len(body))
	for i := range body {
		if injectedBody[i], err = inject(span, body[i]); err != nil {
			return err
		}
	}

	span.SetTag("body_count", len(body))
	span.SetTag("body_size", bodySize(body))
	span.SetTag("is_done_chan_nil", doneChan == nil)

	if err = prodc.Producer.MultiPublishAsync(topic, injectedBody, doneChan, args...); err == nil {
		span.SetTag("enqueue_timestamp", time.Now().UnixNano())
	}

	return err
}

// DeferredPublish is a nsq Producer DeferredPublish wrapper with tracing.
func (prodc *Producer) DeferredPublish(topic string, delay time.Duration, body []byte) error {
	return prodc.DeferredPublishWithContext(context.Background(), topic, delay, body)
}

// DeferredPublishWithContext starts span with given context and wrap the nsq.Producer.DeferredPublish
func (prodc *Producer) DeferredPublishWithContext(ctx context.Context, topic string, delay time.Duration, body []byte) error {
	var err error
	span, ctx := prodc.startSpan(ctx, topic, "DeferredPublish")
	defer span.Finish(tracer.WithError(err))

	var injectedBody []byte
	if injectedBody, err = inject(span, body); err != nil {
		return err
	}

	span.SetTag("body_count", 1)
	span.SetTag("body_size", len(body))
	span.SetTag("delay", delay)

	if err = prodc.Producer.DeferredPublish(topic, delay, injectedBody); err == nil {
		span.SetTag("enqueue_timestamp", time.Now().UnixNano())
	}

	return err
}

// DeferredPublishAsync is a nsq Producer DeferredPublishAsync wrapper with tracing.
func (prodc *Producer) DeferredPublishAsync(topic string, delay time.Duration, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	return prodc.DeferredPublishAsyncWithContext(context.Background(), topic, delay, body, doneChan, args...)
}

func (prodc *Producer) DeferredPublishAsyncWithContext(ctx context.Context, topic string, delay time.Duration, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	var err error
	span, ctx := prodc.startSpan(ctx, topic, "DeferredPublish")
	defer span.Finish(tracer.WithError(err))

	var injectedBody []byte
	if injectedBody, err = inject(span, body); err != nil {
		return err
	}

	span.SetTag("body_count", 1)
	span.SetTag("body_size", len(body))
	span.SetTag("is_done_chan_nil", doneChan == nil)

	if err = prodc.DeferredPublishAsync(topic, delay, injectedBody, doneChan, args...); err == nil {
		span.SetTag("enqueue_timestamp", time.Now().UnixNano())
	}

	return err
}

func (prodc *Producer) startSpan(ctx context.Context, topic, operation string) (tracer.Span, context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}

	opts := []tracer.StartSpanOption{
		tracer.ServiceName(prodc.cfg.service),
		tracer.ResourceName(topic),
		tracer.SpanType(ext.SpanTypeMessageProducer),
	}
	if !math.IsNaN(prodc.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, prodc.cfg.analyticsRate))
	}

	return tracer.StartSpanFromContext(ctx, operation, opts...)
}
