// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package nsq

import (
	"time"

	"github.com/nsqio/go-nsq"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// wrap up nsq.Consumer
type Consumer struct {
	*nsq.Consumer
	*traceHelper
}

// nsq.NewConsumer wrapper function
func NewConsumer(topic string, channel string, config *nsq.Config, opts ...Option) (*Consumer, error) {
	consumer, err := nsq.NewConsumer(topic, channel, config)
	if err != nil {
		return nil, err
	}

	cfg := NewConfig(opts...)
	cfg.Config = config

	return &Consumer{
		Consumer:    consumer,
		traceHelper: newTraceHelper(cfg),
	}, nil
}

// nsq.Consumer.Stats wrapper function
func (cons *Consumer) Stats() *nsq.ConsumerStats {
	start := time.Now()
	stats := cons.Consumer.Stats()
	cons.traceHelper.trace(start, ext.SpanTypeMessageConsumer, "Stats", nil)

	return stats
}

// nsq.Consumer.SetBehaviorDelegate wrapper function
func (cons *Consumer) SetBehaviorDelegate(cb interface{}) {
	start := time.Now()
	cons.Consumer.SetBehaviorDelegate(cb)
	cons.traceHelper.trace(start, ext.SpanTypeMessageConsumer, "SetBehaviorDelegate", nil)
}

// nsq.Consumer.IsStarved wrapper function
func (cons *Consumer) IsStarved() bool {
	start := time.Now()
	is := cons.Consumer.IsStarved()
	cons.traceHelper.trace(start, ext.SpanTypeMessageConsumer, "IsStarved", nil)

	return is
}

// nsq.Consumer.ChangeMaxInFlight wrapper function
func (cons *Consumer) ChangeMaxInFlight(maxInFlight int) {
	start := time.Now()
	cons.Consumer.ChangeMaxInFlight(maxInFlight)
	cons.traceHelper.trace(start, ext.SpanTypeMessageConsumer, "ChangeMaxInFlight", nil)
}

// nsq.Consumer.ConnectToNSQLookupd wrapper function
func (cons *Consumer) ConnectToNSQLookupd(addr string) error {
	start := time.Now()
	err := cons.Consumer.ConnectToNSQLookupd(addr)
	cons.traceHelper.trace(start, ext.SpanTypeMessageConsumer, "ConnectToNSQLookupd", err)

	return err
}

// nsq.Consumer.ConnectToNSQLookupds wrapper function
func (cons *Consumer) ConnectToNSQLookupds(addresses []string) error {
	start := time.Now()
	err := cons.Consumer.ConnectToNSQLookupds(addresses)
	cons.traceHelper.trace(start, ext.SpanTypeMessageConsumer, "ConnectToNSQLookupds", err)

	return err
}

// nsq.Consumer.ConnectToNSQDs wrapper function
func (cons *Consumer) ConnectToNSQDs(addresses []string) error {
	start := time.Now()
	err := cons.Consumer.ConnectToNSQDs(addresses)
	cons.traceHelper.trace(start, ext.SpanTypeMessageConsumer, "ConnectToNSQDs", err)

	return err
}

// nsq.Consumer.ConnectToNSQD wrapper function
func (cons *Consumer) ConnectToNSQD(addr string) error {
	start := time.Now()
	err := cons.Consumer.ConnectToNSQD(addr)
	cons.traceHelper.trace(start, ext.SpanTypeMessageConsumer, "ConnectToNSQD", err)

	return err
}

// nsq.Consumer.DisconnectFromNSQD wrapper function
func (cons *Consumer) DisconnectFromNSQD(addr string) error {
	start := time.Now()
	err := cons.Consumer.DisconnectFromNSQD(addr)
	cons.traceHelper.trace(start, ext.SpanTypeMessageConsumer, "DisconnectFromNSQD", err)

	return err
}

// nsq.Consumer.DisconnectFromNSQLookupd wrapper function
func (cons *Consumer) DisconnectFromNSQLookupd(addr string) error {
	start := time.Now()
	err := cons.Consumer.DisconnectFromNSQLookupd(addr)
	cons.traceHelper.trace(start, ext.SpanTypeMessageConsumer, "DisconnectFromNSQLookupd", err)

	return err
}

// nsq.Consumer.AddHandler wrapper function
func (cons *Consumer) AddHandler(handler nsq.Handler) {
	start := time.Now()
	cons.Consumer.AddHandler(func(next nsq.Handler) nsq.Handler {
		return nsq.HandlerFunc(func(message *nsq.Message) error {
			opts := []ddtrace.StartSpanOption{
				tracer.ServiceName(cons.cfg.service),
				tracer.ResourceName("nsq.Consumer.MessageHandler"),
				tracer.SpanType(ext.SpanTypeMessageProducer),
			}

			span, ctx := tracer.StartSpanFromContext(cons.cfg.ctx, "Consumer.HandleMessage", opts...)
			defer span.Finish(tracer.FinishTime(time.Now()))

			cons.cfg.ctx = ctx

			err := next.HandleMessage(message)
			if err != nil {
				span.SetTag("HandleMessage.Error", err)
			}

			return err
		})
	}(handler))
	cons.traceHelper.trace(start, ext.SpanTypeMessageConsumer, "AddHandler", nil)
}

// nsq.Consumer.AddConcurrentHandlers wrapper function
func (cons *Consumer) AddConcurrentHandlers(handler nsq.Handler, concurrency int) {
	start := time.Now()
	cons.Consumer.AddConcurrentHandlers(handler, concurrency)
	cons.traceHelper.trace(start, ext.SpanTypeMessageConsumer, "AddConcurrentHandlers", nil)
}

// nsq.Consumer.Stop wrapper function
func (cons *Consumer) Stop() {
	start := time.Now()
	cons.Consumer.Stop()
	cons.traceHelper.trace(start, ext.SpanTypeMessageConsumer, "Stop", nil)
}
