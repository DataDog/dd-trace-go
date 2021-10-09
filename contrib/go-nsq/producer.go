// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.
// Author: CodapeWild (https://github.com/CodapeWild/)

package nsq

import (
	"context"
	"math"
	"time"

	"github.com/nsqio/go-nsq"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/tracer"
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
	var (
		span, _ = prodc.startSpanFromContext(ctx, topic, "producer.Publish")
		tags    = map[string]interface{}{
			"body_count": 1,
			"body_size":  len(body),
		}
		err error
	)
	defer prodc.finishSpan(span, tags, err)

	var injectedBody []byte
	if injectedBody, err = inject(span, body); err != nil {
		return err
	}

	err = prodc.Producer.Publish(topic, injectedBody)

	return err
}

// MultiPublish is a nsq Producer MultiPublish wrapper with tracing.
func (prodc *Producer) MultiPublish(topic string, body [][]byte) error {
	return prodc.MultiPublishWithContext(context.Background(), topic, body)
}

// MultiPublishWithContext starts span with given context and wrap the nsq.Producer.MultiPublish
func (prodc *Producer) MultiPublishWithContext(ctx context.Context, topic string, body [][]byte) error {
	var (
		span, _ = prodc.startSpanFromContext(ctx, topic, "producer.MultiPublish")
		tags    = map[string]interface{}{
			"body_count": len(body),
			"body_size":  bodySize(body),
		}
		err error
	)
	defer prodc.finishSpan(span, tags, err)

	injectedBody := make([][]byte, len(body))
	for i := range body {
		if injectedBody[i], err = inject(span, body[i]); err != nil {
			return err
		}
	}

	err = prodc.Producer.MultiPublish(topic, injectedBody)

	return err
}

// PublishAsync is a nsq Producer PublishAsync wrapper with tracing.
func (prodc *Producer) PublishAsync(topic string, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	return prodc.PublishAsyncWithContext(context.Background(), topic, body, doneChan, args...)
}

// PublishAsyncWithContext starts span with given context and wrap the nsq.Producer.PublishAsync
func (prodc *Producer) PublishAsyncWithContext(ctx context.Context, topic string, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	var (
		span, _ = prodc.startSpanFromContext(ctx, topic, "producer.PublishAsync")
		tags    = map[string]interface{}{
			"body_count": 1,
			"body_size":  len(body),
			"arg_count":  len(args),
		}
		err error
	)
	defer prodc.finishSpan(span, tags, err)

	var injectedBody []byte
	if injectedBody, err = inject(span, body); err != nil {
		return err
	}

	err = prodc.Producer.PublishAsync(topic, injectedBody, doneChan, args...)

	return err
}

// MultiPublishAsync is a nsq Producer MultiPublishAsync wrapper with tracing.
func (prodc *Producer) MultiPublishAsync(topic string, body [][]byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	return prodc.MultiPublishAsyncWithContext(context.Background(), topic, body, doneChan, args...)
}

// MultiPublishAsyncWithContext starts span with given context and wrap the nsq.Producer.MultiPublishAsync
func (prodc *Producer) MultiPublishAsyncWithContext(ctx context.Context, topic string, body [][]byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	var (
		span, _ = prodc.startSpanFromContext(ctx, topic, "producer.MultiPublishAsync")
		tags    = map[string]interface{}{
			"body_count": len(body),
			"body_size":  bodySize(body),
			"arg_count":  len(args),
		}
		err error
	)
	defer prodc.finishSpan(span, tags, err)

	injectedBody := make([][]byte, len(body))
	for i := range body {
		if injectedBody[i], err = inject(span, body[i]); err != nil {
			return err
		}
	}

	err = prodc.Producer.MultiPublishAsync(topic, injectedBody, doneChan, args...)

	return err
}

// DeferredPublish is a nsq Producer DeferredPublish wrapper with tracing.
func (prodc *Producer) DeferredPublish(topic string, delay time.Duration, body []byte) error {
	return prodc.DeferredPublishWithContext(context.Background(), topic, delay, body)
}

// DeferredPublishWithContext starts span with given context and wrap the nsq.Producer.DeferredPublish
func (prodc *Producer) DeferredPublishWithContext(ctx context.Context, topic string, delay time.Duration, body []byte) error {
	var (
		span, _ = prodc.startSpanFromContext(ctx, topic, "producer.DeferredPublish")
		tags    = map[string]interface{}{
			"body_count": 1,
			"body_size":  len(body),
			"delay":      delay,
		}
		err error
	)
	defer prodc.finishSpan(span, tags, err)

	var injectedBody []byte
	if injectedBody, err = inject(span, body); err != nil {
		return err
	}

	err = prodc.Producer.DeferredPublish(topic, delay, injectedBody)

	return err
}

// DeferredPublishAsync is a nsq Producer DeferredPublishAsync wrapper with tracing.
func (prodc *Producer) DeferredPublishAsync(topic string, delay time.Duration, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	return prodc.DeferredPublishAsyncWithContext(context.Background(), topic, delay, body, doneChan, args...)
}

func (prodc *Producer) DeferredPublishAsyncWithContext(ctx context.Context, topic string, delay time.Duration, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	var (
		span, _ = prodc.startSpanFromContext(ctx, topic, "producer.DeferredPublishAsync")
		tags    = map[string]interface{}{
			"body_count": 1,
			"body_size":  len(body),
			"arg_count":  len(args),
			"delay":      delay,
		}
		err error
	)
	defer prodc.finishSpan(span, tags, err)

	var injectedBody []byte
	if injectedBody, err = inject(span, body); err != nil {
		return err
	}

	err = prodc.DeferredPublishAsync(topic, delay, injectedBody, doneChan, args...)

	return err
}

func (prodc *Producer) startSpanFromContext(ctx context.Context, topic, operation string) (tracer.Span, context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}

	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(prodc.cfg.service),
		tracer.ResourceName(topic),
		tracer.SpanType(ext.SpanTypeMessageProducer),
	}
	if !math.IsNaN(prodc.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, prodc.cfg.analyticsRate))
	}

	return tracer.StartSpanFromContext(ctx, operation, opts...)
}

func (prodc *Producer) finishSpan(span tracer.Span, tags map[string]interface{}, err error) {
	for k, v := range tags {
		span.SetTag(k, v)
	}
	span.Finish(tracer.WithError(err))
}
