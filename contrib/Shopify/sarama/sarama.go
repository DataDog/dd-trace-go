// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package sarama provides functions to trace the Shopify/sarama package (https://github.com/Shopify/sarama).
//
// Deprecated: github.com/Shopify/sarama is no longer maintained. Please migrate to github.com/IBM/sarama
// and use the corresponding instrumentation.
package sarama // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/Shopify/sarama"

import (
	"context"
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/datastreams"
	"gopkg.in/DataDog/dd-trace-go.v1/datastreams/options"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/Shopify/sarama"
)

const componentName = "Shopify/sarama"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/Shopify/sarama")
}

type partitionConsumer struct {
	sarama.PartitionConsumer
	messages chan *sarama.ConsumerMessage
}

// Messages returns the read channel for the messages that are returned by
// the broker.
func (pc *partitionConsumer) Messages() <-chan *sarama.ConsumerMessage {
	return pc.messages
}

// WrapPartitionConsumer wraps a sarama.PartitionConsumer causing each received
// message to be traced.
func WrapPartitionConsumer(pc sarama.PartitionConsumer, opts ...Option) sarama.PartitionConsumer {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}
	log.Debug("contrib/Shopify/sarama: Wrapping Partition Consumer: %#v", cfg)
	wrapped := &partitionConsumer{
		PartitionConsumer: pc,
		messages:          make(chan *sarama.ConsumerMessage),
	}
	go func() {
		msgs := pc.Messages()
		var prev ddtrace.Span
		for msg := range msgs {
			// create the next span from the message
			opts := []tracer.StartSpanOption{
				tracer.ServiceName(cfg.consumerServiceName),
				tracer.ResourceName("Consume Topic " + msg.Topic),
				tracer.SpanType(ext.SpanTypeMessageConsumer),
				tracer.Tag(ext.MessagingKafkaPartition, msg.Partition),
				tracer.Tag("offset", msg.Offset),
				tracer.Tag(ext.Component, componentName),
				tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
				tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
				tracer.Measured(),
			}
			if !math.IsNaN(cfg.analyticsRate) {
				opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
			}
			// kafka supports headers, so try to extract a span context
			carrier := NewConsumerMessageCarrier(msg)
			if spanctx, err := tracer.Extract(carrier); err == nil {
				opts = append(opts, tracer.ChildOf(spanctx))
			}
			next := tracer.StartSpan(cfg.consumerSpanName, opts...)
			// reinject the span context so consumers can pick it up
			tracer.Inject(next.Context(), carrier)
			setConsumeCheckpoint(cfg.dataStreamsEnabled, cfg.groupID, msg)

			wrapped.messages <- msg

			// if the next message was received, finish the previous span
			if prev != nil {
				prev.Finish()
			}
			prev = next
		}
		// finish any remaining span
		if prev != nil {
			prev.Finish()
		}
		close(wrapped.messages)
	}()
	return wrapped
}

type consumer struct {
	sarama.Consumer
	opts []Option
}

// ConsumePartition invokes Consumer.ConsumePartition and wraps the resulting
// PartitionConsumer.
func (c *consumer) ConsumePartition(topic string, partition int32, offset int64) (sarama.PartitionConsumer, error) {
	pc, err := c.Consumer.ConsumePartition(topic, partition, offset)
	if err != nil {
		return pc, err
	}
	return WrapPartitionConsumer(pc, c.opts...), nil
}

// WrapConsumer wraps a sarama.Consumer wrapping any PartitionConsumer created
// via Consumer.ConsumePartition.
func WrapConsumer(c sarama.Consumer, opts ...Option) sarama.Consumer {
	return &consumer{
		Consumer: c,
		opts:     opts,
	}
}

type syncProducer struct {
	sarama.SyncProducer
	version sarama.KafkaVersion
	cfg     *config
}

// SendMessage calls sarama.SyncProducer.SendMessage and traces the request.
func (p *syncProducer) SendMessage(msg *sarama.ProducerMessage) (partition int32, offset int64, err error) {
	span := startProducerSpan(p.cfg, p.version, msg)
	setProduceCheckpoint(p.cfg.dataStreamsEnabled, msg, p.version)
	partition, offset, err = p.SyncProducer.SendMessage(msg)
	finishProducerSpan(span, partition, offset, err)
	if err == nil && p.cfg.dataStreamsEnabled {
		tracer.TrackKafkaProduceOffset(msg.Topic, partition, offset)
	}
	return partition, offset, err
}

// SendMessages calls sarama.SyncProducer.SendMessages and traces the requests.
func (p *syncProducer) SendMessages(msgs []*sarama.ProducerMessage) error {
	// although there's only one call made to the SyncProducer, the messages are
	// treated individually, so we create a span for each one
	spans := make([]ddtrace.Span, len(msgs))
	for i, msg := range msgs {
		setProduceCheckpoint(p.cfg.dataStreamsEnabled, msg, p.version)
		spans[i] = startProducerSpan(p.cfg, p.version, msg)
	}
	err := p.SyncProducer.SendMessages(msgs)
	for i, span := range spans {
		finishProducerSpan(span, msgs[i].Partition, msgs[i].Offset, err)
	}
	if err == nil && p.cfg.dataStreamsEnabled {
		// we only track Kafka lag if messages have been sent successfully. Otherwise, we have no way to know to which partition data was sent to.
		for _, msg := range msgs {
			tracer.TrackKafkaProduceOffset(msg.Topic, msg.Partition, msg.Offset)
		}
	}
	return err
}

// WrapSyncProducer wraps a sarama.SyncProducer so that all produced messages
// are traced.
func WrapSyncProducer(saramaConfig *sarama.Config, producer sarama.SyncProducer, opts ...Option) sarama.SyncProducer {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}
	log.Debug("contrib/Shopify/sarama: Wrapping Sync Producer: %#v", cfg)
	if saramaConfig == nil {
		saramaConfig = sarama.NewConfig()
	}
	return &syncProducer{
		SyncProducer: producer,
		version:      saramaConfig.Version,
		cfg:          cfg,
	}
}

type asyncProducer struct {
	sarama.AsyncProducer
	input     chan *sarama.ProducerMessage
	successes chan *sarama.ProducerMessage
	errors    chan *sarama.ProducerError
}

// Input returns the input channel.
func (p *asyncProducer) Input() chan<- *sarama.ProducerMessage {
	return p.input
}

// Successes returns the successes channel.
func (p *asyncProducer) Successes() <-chan *sarama.ProducerMessage {
	return p.successes
}

// Errors returns the errors channel.
func (p *asyncProducer) Errors() <-chan *sarama.ProducerError {
	return p.errors
}

// WrapAsyncProducer wraps a sarama.AsyncProducer so that all produced messages
// are traced. It requires the underlying sarama Config so we can know whether
// or not successes will be returned. Tracing requires at least sarama.V0_11_0_0
// version which is the first version that supports headers. Only spans of
// successfully published messages have partition and offset tags set.
func WrapAsyncProducer(saramaConfig *sarama.Config, p sarama.AsyncProducer, opts ...Option) sarama.AsyncProducer {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}
	log.Debug("contrib/Shopify/sarama: Wrapping Async Producer: %#v", cfg)
	if saramaConfig == nil {
		saramaConfig = sarama.NewConfig()
		saramaConfig.Version = sarama.V0_11_0_0
	} else if !saramaConfig.Version.IsAtLeast(sarama.V0_11_0_0) {
		log.Error("Tracing Sarama async producer requires at least sarama.V0_11_0_0 version")
	}
	wrapped := &asyncProducer{
		AsyncProducer: p,
		input:         make(chan *sarama.ProducerMessage),
		successes:     make(chan *sarama.ProducerMessage),
		errors:        make(chan *sarama.ProducerError),
	}
	go func() {
		spans := make(map[uint64]ddtrace.Span)
		defer close(wrapped.input)
		defer close(wrapped.successes)
		defer close(wrapped.errors)
		for {
			select {
			case msg := <-wrapped.input:
				span := startProducerSpan(cfg, saramaConfig.Version, msg)
				setProduceCheckpoint(cfg.dataStreamsEnabled, msg, saramaConfig.Version)
				p.Input() <- msg
				if saramaConfig.Producer.Return.Successes {
					spanID := span.Context().SpanID()
					spans[spanID] = span
				} else {
					// if returning successes isn't enabled, we just finish the
					// span right away because there's no way to know when it will
					// be done
					span.Finish()
				}
			case msg, ok := <-p.Successes():
				if !ok {
					// producer was closed, so exit
					return
				}
				if cfg.dataStreamsEnabled {
					// we only track Kafka lag if returning successes is enabled. Otherwise, we have no way to know to which partition data was sent to.
					tracer.TrackKafkaProduceOffset(msg.Topic, msg.Partition, msg.Offset)
				}
				if spanctx, spanFound := getSpanContext(msg); spanFound {
					spanID := spanctx.SpanID()
					if span, ok := spans[spanID]; ok {
						delete(spans, spanID)
						finishProducerSpan(span, msg.Partition, msg.Offset, nil)
					}
				}
				wrapped.successes <- msg
			case err, ok := <-p.Errors():
				if !ok {
					// producer was closed
					return
				}
				if spanctx, spanFound := getSpanContext(err.Msg); spanFound {
					spanID := spanctx.SpanID()
					if span, ok := spans[spanID]; ok {
						delete(spans, spanID)
						span.Finish(tracer.WithError(err))
					}
				}
				wrapped.errors <- err
			}
		}
	}()
	return wrapped
}

func startProducerSpan(cfg *config, version sarama.KafkaVersion, msg *sarama.ProducerMessage) ddtrace.Span {
	carrier := NewProducerMessageCarrier(msg)
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(cfg.producerServiceName),
		tracer.ResourceName("Produce Topic " + msg.Topic),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
	}
	if !math.IsNaN(cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
	// if there's a span context in the headers, use that as the parent
	if spanctx, err := tracer.Extract(carrier); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span := tracer.StartSpan(cfg.producerSpanName, opts...)
	if version.IsAtLeast(sarama.V0_11_0_0) {
		// re-inject the span context so consumers can pick it up
		tracer.Inject(span.Context(), carrier)
	}
	return span
}

func finishProducerSpan(span ddtrace.Span, partition int32, offset int64, err error) {
	span.SetTag(ext.MessagingKafkaPartition, partition)
	span.SetTag("offset", offset)
	span.Finish(tracer.WithError(err))
}

func getSpanContext(msg *sarama.ProducerMessage) (ddtrace.SpanContext, bool) {
	carrier := NewProducerMessageCarrier(msg)
	spanctx, err := tracer.Extract(carrier)
	if err != nil {
		return nil, false
	}

	return spanctx, true
}

func setProduceCheckpoint(enabled bool, msg *sarama.ProducerMessage, version sarama.KafkaVersion) {
	if !enabled || msg == nil {
		return
	}
	edges := []string{"direction:out", "topic:" + msg.Topic, "type:kafka"}
	carrier := NewProducerMessageCarrier(msg)
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(datastreams.ExtractFromBase64Carrier(context.Background(), carrier), options.CheckpointParams{PayloadSize: getProducerMsgSize(msg)}, edges...)
	if !ok || !version.IsAtLeast(sarama.V0_11_0_0) {
		return
	}
	datastreams.InjectToBase64Carrier(ctx, carrier)
}

func setConsumeCheckpoint(enabled bool, groupID string, msg *sarama.ConsumerMessage) {
	if !enabled || msg == nil {
		return
	}
	edges := []string{"direction:in", "topic:" + msg.Topic, "type:kafka"}
	if groupID != "" {
		edges = append(edges, "group:"+groupID)
	}
	carrier := NewConsumerMessageCarrier(msg)
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(datastreams.ExtractFromBase64Carrier(context.Background(), carrier), options.CheckpointParams{PayloadSize: getConsumerMsgSize(msg)}, edges...)
	if !ok {
		return
	}
	datastreams.InjectToBase64Carrier(ctx, carrier)
	if groupID != "" {
		// only track Kafka lag if a consumer group is set.
		// since there is no ack mechanism, we consider that messages read are committed right away.
		tracer.TrackKafkaCommitOffset(groupID, msg.Topic, msg.Partition, msg.Offset)
	}
}

func getProducerMsgSize(msg *sarama.ProducerMessage) (size int64) {
	for _, header := range msg.Headers {
		size += int64(len(header.Key) + len(header.Value))
	}
	if msg.Value != nil {
		size += int64(msg.Value.Length())
	}
	if msg.Key != nil {
		size += int64(msg.Key.Length())
	}
	return size
}

func getConsumerMsgSize(msg *sarama.ConsumerMessage) (size int64) {
	for _, header := range msg.Headers {
		size += int64(len(header.Key) + len(header.Value))
	}
	return size + int64(len(msg.Value)+len(msg.Key))
}
