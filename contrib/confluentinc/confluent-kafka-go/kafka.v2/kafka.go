// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package kafka provides functions to trace the confluentinc/confluent-kafka-go package (https://github.com/confluentinc/confluent-kafka-go).
package kafka // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/confluentinc/confluent-kafka-go/kafka"

import (
	"context"
	"math"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/datastreams"
	"gopkg.in/DataDog/dd-trace-go.v1/datastreams/options"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

const (
	// make sure these 3 are updated to V2 for the V2 version.
	componentName   = "confluentinc/confluent-kafka-go/kafka.v2"
	packageName     = "contrib/confluentinc/confluent-kafka-go/kafka.v2"
	integrationName = "github.com/confluentinc/confluent-kafka-go/v2"
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
	log.Debug("%s: Wrapping Consumer: %#v", packageName, wrapped.cfg)
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
				setConsumeCheckpoint(c.cfg.dataStreamsEnabled, c.cfg.groupID, msg)
			} else if offset, ok := evt.(kafka.OffsetsCommitted); ok {
				commitOffsets(c.cfg.dataStreamsEnabled, c.cfg.groupID, offset.Offsets, offset.Error)
				c.trackHighWatermark(c.cfg.dataStreamsEnabled, offset.Offsets)
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
		tracer.Tag(ext.MessagingKafkaPartition, msg.TopicPartition.Partition),
		tracer.Tag("offset", msg.TopicPartition.Offset),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Measured(),
	}
	if c.cfg.bootstrapServers != "" {
		opts = append(opts, tracer.Tag(ext.KafkaBootstrapServers, c.cfg.bootstrapServers))
	}
	if c.cfg.tagFns != nil {
		for key, tagFn := range c.cfg.tagFns {
			opts = append(opts, tracer.Tag(key, tagFn(msg)))
		}
	}
	if !math.IsNaN(c.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, c.cfg.analyticsRate))
	}
	// kafka supports headers, so try to extract a span context
	carrier := NewMessageCarrier(msg)
	if spanctx, err := tracer.Extract(carrier); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, _ := tracer.StartSpanFromContext(c.cfg.ctx, c.cfg.consumerSpanName, opts...)
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
		setConsumeCheckpoint(c.cfg.dataStreamsEnabled, c.cfg.groupID, msg)
		c.prev = c.startSpan(msg)
	} else if offset, ok := evt.(kafka.OffsetsCommitted); ok {
		commitOffsets(c.cfg.dataStreamsEnabled, c.cfg.groupID, offset.Offsets, offset.Error)
		c.trackHighWatermark(c.cfg.dataStreamsEnabled, offset.Offsets)
	}
	return evt
}

func (c *Consumer) trackHighWatermark(dataStreamsEnabled bool, offsets []kafka.TopicPartition) {
	if !dataStreamsEnabled {
		return
	}
	for _, tp := range offsets {
		if _, high, err := c.Consumer.GetWatermarkOffsets(*tp.Topic, tp.Partition); err == nil {
			tracer.TrackKafkaHighWatermarkOffset("", *tp.Topic, tp.Partition, high)
		}
	}
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
	setConsumeCheckpoint(c.cfg.dataStreamsEnabled, c.cfg.groupID, msg)
	c.prev = c.startSpan(msg)
	return msg, nil
}

// Commit commits current offsets and tracks the commit offsets if data streams is enabled.
func (c *Consumer) Commit() ([]kafka.TopicPartition, error) {
	tps, err := c.Consumer.Commit()
	commitOffsets(c.cfg.dataStreamsEnabled, c.cfg.groupID, tps, err)
	c.trackHighWatermark(c.cfg.dataStreamsEnabled, tps)
	return tps, err
}

// CommitMessage commits a message and tracks the commit offsets if data streams is enabled.
func (c *Consumer) CommitMessage(msg *kafka.Message) ([]kafka.TopicPartition, error) {
	tps, err := c.Consumer.CommitMessage(msg)
	commitOffsets(c.cfg.dataStreamsEnabled, c.cfg.groupID, tps, err)
	c.trackHighWatermark(c.cfg.dataStreamsEnabled, tps)
	return tps, err
}

// CommitOffsets commits provided offsets and tracks the commit offsets if data streams is enabled.
func (c *Consumer) CommitOffsets(offsets []kafka.TopicPartition) ([]kafka.TopicPartition, error) {
	tps, err := c.Consumer.CommitOffsets(offsets)
	commitOffsets(c.cfg.dataStreamsEnabled, c.cfg.groupID, tps, err)
	c.trackHighWatermark(c.cfg.dataStreamsEnabled, tps)
	return tps, err
}

func commitOffsets(dataStreamsEnabled bool, groupID string, tps []kafka.TopicPartition, err error) {
	if err != nil || groupID == "" || !dataStreamsEnabled {
		return
	}
	for _, tp := range tps {
		tracer.TrackKafkaCommitOffset(groupID, *tp.Topic, tp.Partition, int64(tp.Offset))
	}
}

func trackProduceOffsets(dataStreamsEnabled bool, msg *kafka.Message, err error) {
	if err != nil || !dataStreamsEnabled || msg.TopicPartition.Topic == nil {
		return
	}
	tracer.TrackKafkaProduceOffset(*msg.TopicPartition.Topic, msg.TopicPartition.Partition, int64(msg.TopicPartition.Offset))
}

// A Producer wraps a kafka.Producer.
type Producer struct {
	*kafka.Producer
	cfg            *config
	produceChannel chan *kafka.Message
	events         chan kafka.Event
	libraryVersion int
}

// WrapProducer wraps a kafka.Producer so requests are traced.
func WrapProducer(p *kafka.Producer, opts ...Option) *Producer {
	version, _ := kafka.LibraryVersion()
	wrapped := &Producer{
		Producer:       p,
		cfg:            newConfig(opts...),
		events:         p.Events(),
		libraryVersion: version,
	}
	log.Debug("%s: Wrapping Producer: %#v", packageName, wrapped.cfg)
	wrapped.produceChannel = wrapped.traceProduceChannel(p.ProduceChannel())
	if wrapped.cfg.dataStreamsEnabled {
		wrapped.events = wrapped.traceEventsChannel(p.Events())
	}
	return wrapped
}

// Events returns the kafka Events channel (if enabled). Message events will be monitored
// with data streams monitoring (if enabled)
func (p *Producer) Events() chan kafka.Event {
	return p.events
}

func (p *Producer) traceProduceChannel(out chan *kafka.Message) chan *kafka.Message {
	if out == nil {
		return out
	}
	in := make(chan *kafka.Message, 1)
	go func() {
		for msg := range in {
			span := p.startSpan(msg)
			setProduceCheckpoint(p.cfg.dataStreamsEnabled, p.libraryVersion, msg)
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
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.MessagingKafkaPartition, msg.TopicPartition.Partition),
	}
	if p.cfg.bootstrapServers != "" {
		opts = append(opts, tracer.Tag(ext.KafkaBootstrapServers, p.cfg.bootstrapServers))
	}
	if !math.IsNaN(p.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, p.cfg.analyticsRate))
	}
	//if there's a span context in the headers, use that as the parent
	carrier := NewMessageCarrier(msg)
	if spanctx, err := tracer.Extract(carrier); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, _ := tracer.StartSpanFromContext(p.cfg.ctx, p.cfg.producerSpanName, opts...)
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
				trackProduceOffsets(p.cfg.dataStreamsEnabled, msg, err)
			}
			span.Finish(tracer.WithError(err))
			oldDeliveryChan <- evt
		}()
	}

	setProduceCheckpoint(p.cfg.dataStreamsEnabled, p.libraryVersion, msg)
	err := p.Producer.Produce(msg, deliveryChan)
	// with no delivery channel or enqueue error, finish immediately
	if err != nil || deliveryChan == nil {
		span.Finish(tracer.WithError(err))
	}

	return err
}

// ProduceChannel returns a channel which can receive kafka Messages and will
// send them to the underlying producer channel.
func (p *Producer) ProduceChannel() chan *kafka.Message {
	return p.produceChannel
}

func (p *Producer) traceEventsChannel(in chan kafka.Event) chan kafka.Event {
	if in == nil {
		return nil
	}
	out := make(chan kafka.Event, 1)
	go func() {
		defer close(out)
		for evt := range in {
			if msg, ok := evt.(*kafka.Message); ok {
				trackProduceOffsets(p.cfg.dataStreamsEnabled, msg, msg.TopicPartition.Error)
			}
			out <- evt
		}
	}()
	return out
}

func setConsumeCheckpoint(dataStreamsEnabled bool, groupID string, msg *kafka.Message) {
	if !dataStreamsEnabled || msg == nil {
		return
	}
	edges := []string{"direction:in", "topic:" + *msg.TopicPartition.Topic, "type:kafka"}
	if groupID != "" {
		edges = append(edges, "group:"+groupID)
	}
	carrier := NewMessageCarrier(msg)
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(datastreams.ExtractFromBase64Carrier(context.Background(), carrier), options.CheckpointParams{PayloadSize: getMsgSize(msg)}, edges...)
	if !ok {
		return
	}
	datastreams.InjectToBase64Carrier(ctx, carrier)
}

func setProduceCheckpoint(dataStreamsEnabled bool, version int, msg *kafka.Message) {
	if !dataStreamsEnabled || msg == nil {
		return
	}
	edges := []string{"direction:out", "topic:" + *msg.TopicPartition.Topic, "type:kafka"}
	carrier := NewMessageCarrier(msg)
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(datastreams.ExtractFromBase64Carrier(context.Background(), carrier), options.CheckpointParams{PayloadSize: getMsgSize(msg)}, edges...)
	if !ok || version < 0x000b0400 {
		// headers not supported before librdkafka >=0.11.4
		return
	}
	datastreams.InjectToBase64Carrier(ctx, carrier)
}

func getMsgSize(msg *kafka.Message) (size int64) {
	for _, header := range msg.Headers {
		size += int64(len(header.Key) + len(header.Value))
	}
	return size + int64(len(msg.Value)+len(msg.Key))
}
