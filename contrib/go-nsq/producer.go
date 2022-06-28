// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.
// Author: CodapeWild (https://github.com/CodapeWild/)

package nsq

import (
	"context"
	"time"

	"github.com/nsqio/go-nsq"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/tracer"
)

// Producer is a wrap-up class of nsq Producer.
type Producer struct {
	p      *nsq.Producer
	nsqcfg *nsq.Config
	ccfg   *clientConfig
}

// NewProducer return a go-nsq *Producer, default tracing options will be used if no option assigned.
func NewProducer(addr string, config *nsq.Config, opts ...Option) (*Producer, error) {
	prodc, err := nsq.NewProducer(addr, config)
	if err != nil {
		return nil, err
	}

	cfg := new(clientConfig)
	defaultConfig(cfg)
	for k := range opts {
		opts[k](cfg)
	}

	return &Producer{
		p:      prodc,
		nsqcfg: config,
		ccfg:   cfg}, nil
}

// Publish is a wrap-up function of PublishWithContext with nil context.
func (prodc *Producer) Publish(topic string, body []byte) error {
	return prodc.PublishWithContext(nil, topic, body)
}

// PublishFromContext is a wrp-up function of nsq Publish with a given context.
func (prodc *Producer) PublishWithContext(ctx context.Context, topic string, body []byte) error {
	var err error
	span, ctx := startSpanFromContext(ctx, prodc.ccfg, prodc.nsqcfg, topic, "PublishWithContext")
	defer span.Finish(tracer.WithError(err))

	var combBody []byte
	if combBody, err = inject(span, body); err != nil {
		return err
	}

	span.SetTag(MsgCount, 1)
	span.SetTag(MsgSize, len(body))

	if err = prodc.p.Publish(topic, combBody); err == nil {
		span.SetTag(EnqueueTime, time.Now().UnixNano())
	}

	return err
}

// MultiPublish is a wrap-up function of MultiPublishWithContext with nil context.
func (prodc *Producer) MultiPublish(topic string, body [][]byte) error {
	return prodc.MultiPublishWithContext(nil, topic, body)
}

// MultiPublishWithContext is a wrp-up function of nsq MultiPublish with a given context.
func (prodc *Producer) MultiPublishWithContext(ctx context.Context, topic string, body [][]byte) error {
	var err error
	span, ctx := startSpanFromContext(ctx, prodc.ccfg, prodc.nsqcfg, topic, "MultiPublishWithContext")
	defer span.Finish(tracer.WithError(err))

	combBody := make([][]byte, len(body))
	for i := range body {
		if combBody[i], err = inject(span, body[i]); err != nil {
			return err
		}
	}

	span.SetTag(MsgCount, len(body))
	span.SetTag(MsgSize, bodySize(body))

	if err = prodc.p.MultiPublish(topic, combBody); err == nil {
		span.SetTag(EnqueueTime, time.Now().UnixNano())
	}

	return err
}

// PublishAsync is a wrap-up function of PublishAsyncWithContext with nil context.
func (prodc *Producer) PublishAsync(topic string, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	return prodc.PublishAsyncWithContext(nil, topic, body, doneChan, args...)
}

// PublishAsyncWithContext is a wrp-up function of nsq PublishAsync with a given context.
func (prodc *Producer) PublishAsyncWithContext(ctx context.Context, topic string, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	var err error
	span, ctx := startSpanFromContext(ctx, prodc.ccfg, prodc.nsqcfg, topic, "PublishAsyncWithContext")
	defer span.Finish(tracer.WithError(err))

	var combBody []byte
	if combBody, err = inject(span, body); err != nil {
		return err
	}

	span.SetTag(MsgCount, 1)
	span.SetTag(MsgSize, len(body))

	if err = prodc.p.PublishAsync(topic, combBody, doneChan, args...); err == nil {
		span.SetTag(EnqueueTime, time.Now().UnixNano())
	}

	return err
}

// MultiPublishAsync is a wrap-up function of MultiPublishAsyncWithContext with nil context.
func (prodc *Producer) MultiPublishAsync(topic string, body [][]byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	return prodc.MultiPublishAsyncWithContext(nil, topic, body, doneChan, args...)
}

// MultiPublishAsyncWithContext is a wrp-up function of nsq MultiPublishAsync with a given context.
func (prodc *Producer) MultiPublishAsyncWithContext(ctx context.Context, topic string, body [][]byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	var err error
	span, ctx := startSpanFromContext(ctx, prodc.ccfg, prodc.nsqcfg, topic, "MultiPublishAsyncWithContext")
	defer span.Finish(tracer.WithError(err))

	combBody := make([][]byte, len(body))
	for i := range body {
		if combBody[i], err = inject(span, body[i]); err != nil {
			return err
		}
	}

	span.SetTag(MsgCount, len(body))
	span.SetTag(MsgSize, bodySize(body))

	if err = prodc.p.MultiPublishAsync(topic, combBody, doneChan, args...); err == nil {
		span.SetTag(EnqueueTime, time.Now().UnixNano())
	}

	return err
}

// DeferredPublish is a wrap-up function of DeferredPublishWithContext with nil context.
func (prodc *Producer) DeferredPublish(topic string, delay time.Duration, body []byte) error {
	return prodc.DeferredPublishWithContext(nil, topic, delay, body)
}

// DeferredPublishWithContext is a wrp-up function of nsq DeferredPublish with a given context.
func (prodc *Producer) DeferredPublishWithContext(ctx context.Context, topic string, delay time.Duration, body []byte) error {
	var err error
	span, ctx := startSpanFromContext(ctx, prodc.ccfg, prodc.nsqcfg, topic, "DeferredPublishWithContext")
	defer span.Finish(tracer.WithError(err))

	var combBody []byte
	if combBody, err = inject(span, body); err != nil {
		return err
	}

	span.SetTag(MsgCount, 1)
	span.SetTag(MsgSize, len(body))
	span.SetTag(DeferredDuration, delay)

	if err = prodc.p.DeferredPublish(topic, delay, combBody); err == nil {
		span.SetTag(EnqueueTime, time.Now().UnixNano())
	}

	return err
}

// DeferredPublishAsync is a wrap-up function of DeferredPublishAsyncWithContext with nil context.
func (prodc *Producer) DeferredPublishAsync(topic string, delay time.Duration, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	return prodc.DeferredPublishAsyncWithContext(nil, topic, delay, body, doneChan, args...)
}

// DeferredPublishAsyncWithContext is a wrp-up function of nsq DeferredPublishAsync with a given context.
func (prodc *Producer) DeferredPublishAsyncWithContext(ctx context.Context, topic string, delay time.Duration, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	var err error
	span, ctx := startSpanFromContext(ctx, prodc.ccfg, prodc.nsqcfg, topic, "DeferredPublishAsyncWithContext")
	defer span.Finish(tracer.WithError(err))

	var combBody []byte
	if combBody, err = inject(span, body); err != nil {
		return err
	}

	span.SetTag(MsgCount, 1)
	span.SetTag(MsgSize, len(body))

	if err = prodc.DeferredPublishAsync(topic, delay, combBody, doneChan, args...); err == nil {
		span.SetTag(EnqueueTime, time.Now().UnixNano())
	}

	return err
}
