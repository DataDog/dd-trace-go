// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package nsq

import (
	"time"

	"github.com/nsqio/go-nsq"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

// wrap up nsq.Producer
type Producer struct {
	*nsq.Producer
	*traceHelper
}

// nsq.NewProducer wrapper function
func NewProducer(addr string, config *nsq.Config, opts ...Option) (*Producer, error) {
	producer, err := nsq.NewProducer(addr, config)
	if err != nil {
		return nil, err
	}

	cfg := NewConfig(opts...)
	cfg.Config = config

	return &Producer{
		Producer:    producer,
		traceHelper: newTraceHelper(cfg),
	}, nil
}

// nsq.Producer.Ping wrapper function
func (prod *Producer) Ping() error {
	start := time.Now()
	err := prod.Producer.Ping()
	prod.traceHelper.trace(start, ext.SpanTypeMessageProducer, "Ping", err)

	return err
}

// nsq.Producer.Publish wrapper function
func (prod *Producer) Publish(topic string, body []byte) error {
	start := time.Now()
	err := prod.Producer.Publish(topic, body)
	prod.traceHelper.trace(start, ext.SpanTypeMessageProducer, "Publish", err)

	return err
}

// nsq.Producer.MultiPublish wrapper function
func (prod *Producer) MultiPublish(topic string, body [][]byte) error {
	start := time.Now()
	err := prod.Producer.MultiPublish(topic, body)
	prod.traceHelper.trace(start, ext.SpanTypeMessageProducer, "MultiPublish", err)

	return err
}

// nsq.Producer.PublishAsync wrapper function
func (prod *Producer) PublishAsync(topic string, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	start := time.Now()
	err := prod.Producer.PublishAsync(topic, body, doneChan, args...)
	prod.traceHelper.trace(start, ext.SpanTypeMessageProducer, "PublishAsync", err)

	return err
}

// nsq.Producer.MultiPublishAsync wrapper function
func (prod *Producer) MultiPublishAsync(topic string, body [][]byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	start := time.Now()
	err := prod.Producer.MultiPublishAsync(topic, body, doneChan, args...)
	prod.traceHelper.trace(start, ext.SpanTypeMessageProducer, "MultiPublishAsync", err)

	return err
}

// nsq.Producer.DeferredPublish wrapper function
func (prod *Producer) DeferredPublish(topic string, delay time.Duration, body []byte) error {
	start := time.Now()
	err := prod.Producer.DeferredPublish(topic, delay, body)
	prod.traceHelper.trace(start, ext.SpanTypeMessageProducer, "DeferredPublish", err)

	return err
}

// nsq.Producer.DeferredPublishAsync wrapper function
func (prod *Producer) DeferredPublishAsync(topic string, delay time.Duration, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error {
	start := time.Now()
	err := prod.Producer.DeferredPublishAsync(topic, delay, body, doneChan, args...)
	prod.traceHelper.trace(start, ext.SpanTypeMessageProducer, "DeferredPublishAsync", err)

	return err
}

// nsq.Producer.Stop wrapper function
func (prod *Producer) Stop() {
	start := time.Now()
	prod.Producer.Stop()
	prod.traceHelper.trace(start, ext.SpanTypeMessageProducer, "Stop", nil)
}
