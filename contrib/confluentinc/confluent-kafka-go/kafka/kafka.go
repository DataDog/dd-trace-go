// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package kafka provides functions to trace the confluentinc/confluent-kafka-go package (https://github.com/confluentinc/confluent-kafka-go).
package kafka // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/confluentinc/confluent-kafka-go/kafka"

import (
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/confluentinc/confluent-kafka-go/kafka/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/confluentinc/confluent-kafka-go/kafka/internal/tracing"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/confluentinc/confluent-kafka-go/kafka"
)

const (
	// make sure these 3 are updated to V2 for the V2 version.
	componentName   = tracing.ComponentName
	packageName     = "contrib/confluentinc/confluent-kafka-go/kafka"
	integrationName = "github.com/confluentinc/confluent-kafka-go"
)

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported(integrationName)
}

// NewConsumer calls kafka.NewConsumer and wraps the resulting Consumer.
func NewConsumer(conf *kafka.ConfigMap, opts ...Option) (*Consumer, error) {
	c, err := kafka.NewConsumer(conf)
	if err != nil {
		return nil, err
	}
	opts = append(opts, WithConfig(conf))
	return WrapConsumer(c, opts...), nil
}

// NewProducer calls kafka.NewProducer and wraps the resulting Producer.
func NewProducer(conf *kafka.ConfigMap, opts ...Option) (*Producer, error) {
	p, err := kafka.NewProducer(conf)
	if err != nil {
		return nil, err
	}
	opts = append(opts, WithConfig(conf))
	return WrapProducer(p, opts...), nil
}

// A Consumer wraps a kafka.Consumer.
type Consumer struct {
	*kafka.Consumer
	cfg    *internal.Config
	tracer *tracing.ConsumerTracer
}

// WrapConsumer wraps a kafka.Consumer so that any consumed events are traced.
func WrapConsumer(c *kafka.Consumer, opts ...Option) *Consumer {
	cfg := internal.NewConfig(opts...)
	wrapped := &Consumer{
		Consumer: c,
		cfg:      cfg,
		tracer: tracing.NewConsumerTracer(cfg.Ctx, c, cfg.DataStreamsEnabled, cfg.GroupID, tracing.StartSpanConfig{
			Service:          cfg.ConsumerServiceName,
			Operation:        cfg.ConsumerSpanName,
			BootstrapServers: cfg.BootstrapServers,
			AnalyticsRate:    cfg.AnalyticsRate,
			TagFns:           cfg.TagFns,
		}),
	}
	log.Debug("%s: Wrapping Consumer: %#v", packageName, wrapped.cfg)
	return wrapped
}

// Close calls the underlying Consumer.Close and if polling is enabled, finishes
// any remaining span.
func (c *Consumer) Close() error {
	c.tracer.Close()
	return c.Consumer.Close()
}

// Events returns the kafka Events channel (if enabled). Message events will be
// traced.
func (c *Consumer) Events() chan kafka.Event {
	return c.tracer.Events
}

// Poll polls the consumer for messages or events. Message will be
// traced.
func (c *Consumer) Poll(timeoutMS int) (event kafka.Event) {
	return c.tracer.WrapPoll(func() kafka.Event {
		return c.Consumer.Poll(timeoutMS)
	})
}

// ReadMessage polls the consumer for a message. Message will be traced.
func (c *Consumer) ReadMessage(timeout time.Duration) (*kafka.Message, error) {
	return c.tracer.WrapReadMessage(func() (*kafka.Message, error) {
		return c.Consumer.ReadMessage(timeout)
	})
}

// Commit commits current offsets and tracks the commit offsets if data streams is enabled.
func (c *Consumer) Commit() ([]kafka.TopicPartition, error) {
	return c.tracer.WrapCommit(func() ([]kafka.TopicPartition, error) {
		return c.Consumer.Commit()
	})
}

// CommitMessage commits a message and tracks the commit offsets if data streams is enabled.
func (c *Consumer) CommitMessage(msg *kafka.Message) ([]kafka.TopicPartition, error) {
	return c.tracer.WrapCommit(func() ([]kafka.TopicPartition, error) {
		return c.Consumer.CommitMessage(msg)
	})
}

// CommitOffsets commits provided offsets and tracks the commit offsets if data streams is enabled.
func (c *Consumer) CommitOffsets(offsets []kafka.TopicPartition) ([]kafka.TopicPartition, error) {
	return c.tracer.WrapCommit(func() ([]kafka.TopicPartition, error) {
		return c.Consumer.CommitOffsets(offsets)
	})
}

// A Producer wraps a kafka.Producer.
type Producer struct {
	*kafka.Producer
	cfg    *internal.Config
	tracer *tracing.ProducerTracer
}

// WrapProducer wraps a kafka.Producer so requests are traced.
func WrapProducer(p *kafka.Producer, opts ...Option) *Producer {
	cfg := internal.NewConfig(opts...)
	wrapped := &Producer{
		Producer: p,
		cfg:      cfg,
		tracer: tracing.NewProducerTracer(cfg.Ctx, p, cfg.DataStreamsEnabled, tracing.StartSpanConfig{
			Service:          cfg.ProducerServiceName,
			Operation:        cfg.ProducerSpanName,
			BootstrapServers: cfg.BootstrapServers,
			AnalyticsRate:    cfg.AnalyticsRate,
		}),
	}
	log.Debug("%s: Wrapping Producer: %#v", packageName, wrapped.cfg)
	return wrapped
}

// Events returns the kafka Events channel (if enabled). Message events will be monitored
// with data streams monitoring (if enabled)
func (p *Producer) Events() chan kafka.Event {
	return p.tracer.Events
}

// Close calls the underlying Producer.Close and also closes the internal
// wrapping producer channel.
func (p *Producer) Close() {
	p.tracer.Close()
	p.Producer.Close()
}

// Produce calls the underlying Producer.Produce and traces the request.
func (p *Producer) Produce(msg *kafka.Message, deliveryChan chan kafka.Event) error {
	var err error
	stop := p.tracer.AroundProduce(msg, deliveryChan)
	defer stop(err)

	err = p.Producer.Produce(msg, deliveryChan)
	return err
}

// ProduceChannel returns a channel which can receive kafka Messages and will
// send them to the underlying producer channel.
func (p *Producer) ProduceChannel() chan *kafka.Message {
	return p.tracer.ProduceChannel
}
