// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package kafka provides functions to trace the confluentinc/confluent-kafka-go package (https://github.com/confluentinc/confluent-kafka-go).
package kafka // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/confluentinc/confluent-kafka-go/kafka"

import (
	"math"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/confluentinc/confluent-kafka-go/kafka"
)

// NewConsumer calls kafka.NewConsumer and wraps the resulting Consumer.
func NewConsumer(conf *kafka.ConfigMap, opts ...Option) (*Consumer, error) {
	c, err := kafka.NewConsumer(conf)
	if err != nil {
		return nil, err
	}
	return WrapConsumer(c, opts...), nil
}

// NewProducer calls kafka.NewProducer and wraps the resulting Producer.
func NewProducer(conf *kafka.ConfigMap, opts ...Option) (*Producer, error) {
	p, err := kafka.NewProducer(conf)
	if err != nil {
		return nil, err
	}
	return WrapProducer(p, opts...), nil
}

// A Consumer wraps a kafka.Consumer.
type Consumer struct {
	*kafka.Consumer
	cfg    *config
	events chan kafka.Event
	prev   ddtrace.Span
}

// WrapConsumer wraps a kafka.Consumer so that any consumed events are traced.
func WrapConsumer(c *kafka.Consumer, opts ...Option) *Consumer {
	wrapped := &Consumer{
		Consumer: c,
		cfg:      newConfig(opts...),
	}
	log.Debug("contrib/confluentinc/confluent-kafka-go/kafka: Wrapping Consumer: %#v", wrapped.cfg)
	wrapped.events = wrapped.traceEventsChannel(c.Events())
	return wrapped
}

func (c *Consumer) traceEventsChannel(in chan kafka.Event) chan kafka.Event {
	// in will be nil when consuming via the events channel is not enabled
	if in == nil {
		return nil
	}

	out := make(chan kafka.Event, 1)
	go func() {
		defer close(out)
		for evt := range in {
			var next ddtrace.Span

			// only trace messages
			if msg, ok := evt.(*kafka.Message); ok {
				next = c.startSpan(msg)
			}

			out <- evt

			if c.prev != nil {
				c.prev.Finish()
			}
			c.prev = next
		}
		// finish any remaining span
		if c.prev != nil {
			c.prev.Finish()
			c.prev = nil
		}
	}()

	return out
}

func (c *Consumer) startSpan(msg *kafka.Message) ddtrace.Span {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(c.cfg.consumerServiceName),
		tracer.ResourceName("Consume Topic " + *msg.TopicPartition.Topic),
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.Tag("partition", msg.TopicPartition.Partition),
		tracer.Tag("offset", msg.TopicPartition.Offset),
		tracer.Measured(),
	}
	if !math.IsNaN(c.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, c.cfg.analyticsRate))
	}
	// kafka supports headers, so try to extract a span context
	carrier := NewMessageCarrier(msg)
	if spanctx, err := tracer.Extract(carrier); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, _ := tracer.StartSpanFromContext(c.cfg.ctx, "kafka.consume", opts...)
	// reinject the span context so consumers can pick it up
	tracer.Inject(span.Context(), carrier)
	return span
}

// Close calls the underlying Consumer.Close and if polling is enabled, finishes
// any remaining span.
func (c *Consumer) Close() error {
	err := c.Consumer.Close()
	// we only close the previous span if consuming via the events channel is
	// not enabled, because otherwise there would be a data race from the
	// consuming goroutine.
	if c.events == nil && c.prev != nil {
		c.prev.Finish()
		c.prev = nil
	}
	return err
}

// Events returns the kafka Events channel (if enabled). Message events will be
// traced.
func (c *Consumer) Events() chan kafka.Event {
	return c.events
}

// Poll polls the consumer for messages or events. Message will be
// traced.
func (c *Consumer) Poll(timeoutMS int) (event kafka.Event) {
	if c.prev != nil {
		c.prev.Finish()
		c.prev = nil
	}
	evt := c.Consumer.Poll(timeoutMS)
	if msg, ok := evt.(*kafka.Message); ok {
		c.prev = c.startSpan(msg)
	}
	return evt
}

// ReadMessage polls the consumer for a message. Message will be traced.
func (c *Consumer) ReadMessage(timeout time.Duration) (*kafka.Message, error) {
	if c.prev != nil {
		c.prev.Finish()
		c.prev = nil
	}
	msg, err := c.Consumer.ReadMessage(timeout)
	if err != nil {
		return nil, err
	}
	c.prev = c.startSpan(msg)
	return msg, nil
}

// A Producer wraps a kafka.Producer.
type Producer struct {
	*kafka.Producer
	cfg            *config
	produceChannel chan *kafka.Message
}

// WrapProducer wraps a kafka.Producer so requests are traced.
func WrapProducer(p *kafka.Producer, opts ...Option) *Producer {
	wrapped := &Producer{
		Producer: p,
		cfg:      newConfig(opts...),
	}
	log.Debug("contrib/confluentinc/confluent-kafka-go/kafka: Wrapping Producer: %#v", wrapped.cfg)
	wrapped.produceChannel = wrapped.traceProduceChannel(p.ProduceChannel())
	return wrapped
}

func (p *Producer) traceProduceChannel(out chan *kafka.Message) chan *kafka.Message {
	if out == nil {
		return out
	}

	in := make(chan *kafka.Message, 1)
	go func() {
		for msg := range in {
			span := p.startSpan(msg)
			out <- msg
			span.Finish()
		}
	}()

	return in
}

func (p *Producer) startSpan(msg *kafka.Message) ddtrace.Span {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(p.cfg.producerServiceName),
		tracer.ResourceName("Produce Topic " + *msg.TopicPartition.Topic),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag("partition", msg.TopicPartition.Partition),
	}
	if !math.IsNaN(p.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, p.cfg.analyticsRate))
	}
	//if there's a span context in the headers, use that as the parent
	carrier := NewMessageCarrier(msg)
	if spanctx, err := tracer.Extract(carrier); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}

	span, _ := tracer.StartSpanFromContext(p.cfg.ctx, "kafka.produce", opts...)
	// inject the span context so consumers can pick it up
	tracer.Inject(span.Context(), carrier)
	return span
}

// Close calls the underlying Producer.Close and also closes the internal
// wrapping producer channel.
func (p *Producer) Close() {
	close(p.produceChannel)
	p.Producer.Close()
}

// Produce calls the underlying Producer.Produce and traces the request.
func (p *Producer) Produce(msg *kafka.Message, deliveryChan chan kafka.Event) error {
	span := p.startSpan(msg)

	// if the user has selected a delivery channel, we will wrap it and
	// wait for the delivery event to finish the span
	if deliveryChan != nil {
		oldDeliveryChan := deliveryChan
		deliveryChan = make(chan kafka.Event)
		go func() {
			var err error
			evt := <-deliveryChan
			if msg, ok := evt.(*kafka.Message); ok {
				// delivery errors are returned via TopicPartition.Error
				err = msg.TopicPartition.Error
			}
			span.Finish(tracer.WithError(err))
			oldDeliveryChan <- evt
		}()
	}

	err := p.Producer.Produce(msg, deliveryChan)
	// with no delivery channel, finish immediately
	if deliveryChan == nil {
		span.Finish(tracer.WithError(err))
	}

	return err
}

// ProduceChannel returns a channel which can receive kafka Messages and will
// send them to the underlying producer channel.
func (p *Producer) ProduceChannel() chan *kafka.Message {
	return p.produceChannel
}
