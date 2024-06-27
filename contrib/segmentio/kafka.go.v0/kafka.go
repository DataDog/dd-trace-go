// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/segmentio/kafka.go.v0"

import (
	"context"
	"math"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/datastreams"
	"gopkg.in/DataDog/dd-trace-go.v1/datastreams/options"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/segmentio/kafka-go"
)

const componentName = "segmentio/kafka.go.v0"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/segmentio/kafka-go")
}

// NewReader calls kafka.NewReader and wraps the resulting Consumer.
func NewReader(conf kafka.ReaderConfig, opts ...Option) *Reader {
	return WrapReader(kafka.NewReader(conf), opts...)
}

// NewWriter calls kafka.NewWriter and wraps the resulting Producer.
func NewWriter(conf kafka.WriterConfig, opts ...Option) *Writer {
	return WrapWriter(kafka.NewWriter(conf), opts...)
}

// WrapReader wraps a kafka.Reader so that any consumed events are traced.
func WrapReader(c *kafka.Reader, opts ...Option) *Reader {
	wrapped := &Reader{
		Reader: c,
		cfg:    newConfig(opts...),
	}

	if c.Config().Brokers != nil {
		wrapped.bootstrapServers = strings.Join(c.Config().Brokers, ",")
	}

	if c.Config().GroupID != "" {
		wrapped.groupID = c.Config().GroupID
	}

	log.Debug("contrib/segmentio/kafka-go.v0/kafka: Wrapping Reader: %#v", wrapped.cfg)
	return wrapped
}

// A kafkaConfig struct holds information from the kafka config for span tags
type kafkaConfig struct {
	bootstrapServers string
	groupID          string
}

// A Reader wraps a kafka.Reader.
type Reader struct {
	*kafka.Reader
	kafkaConfig
	cfg  *config
	prev ddtrace.Span
}

func (r *Reader) startSpan(ctx context.Context, msg *kafka.Message) ddtrace.Span {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(r.cfg.consumerServiceName),
		tracer.ResourceName("Consume Topic " + msg.Topic),
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.Tag(ext.MessagingKafkaPartition, msg.Partition),
		tracer.Tag("offset", msg.Offset),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.KafkaBootstrapServers, r.bootstrapServers),
		tracer.Measured(),
	}

	if !math.IsNaN(r.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, r.cfg.analyticsRate))
	}
	// kafka supports headers, so try to extract a span context
	carrier := messageCarrier{msg}
	if spanctx, err := tracer.Extract(carrier); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, _ := tracer.StartSpanFromContext(ctx, r.cfg.consumerSpanName, opts...)
	// reinject the span context so consumers can pick it up
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		log.Debug("contrib/segmentio/kafka.go.v0: Failed to inject span context into carrier in reader, %v", err)
	}
	return span
}

// Close calls the underlying Reader.Close and if polling is enabled, finishes
// any remaining span.
func (r *Reader) Close() error {
	err := r.Reader.Close()
	if r.prev != nil {
		r.prev.Finish()
		r.prev = nil
	}
	return err
}

// ReadMessage polls the consumer for a message. Message will be traced.
func (r *Reader) ReadMessage(ctx context.Context) (kafka.Message, error) {
	if r.prev != nil {
		r.prev.Finish()
		r.prev = nil
	}
	msg, err := r.Reader.ReadMessage(ctx)
	if err != nil {
		return kafka.Message{}, err
	}
	r.prev = r.startSpan(ctx, &msg)
	setConsumeCheckpoint(r.cfg.dataStreamsEnabled, r.groupID, &msg)
	return msg, nil
}

// FetchMessage reads and returns the next message from the reader. Message will be traced.
func (r *Reader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	if r.prev != nil {
		r.prev.Finish()
		r.prev = nil
	}
	msg, err := r.Reader.FetchMessage(ctx)
	if err != nil {
		return msg, err
	}
	r.prev = r.startSpan(ctx, &msg)
	setConsumeCheckpoint(r.cfg.dataStreamsEnabled, r.groupID, &msg)
	return msg, nil
}

func setConsumeCheckpoint(enabled bool, groupID string, msg *kafka.Message) {
	if !enabled || msg == nil {
		return
	}
	edges := []string{"direction:in", "topic:" + msg.Topic, "type:kafka"}
	if groupID != "" {
		edges = append(edges, "group:"+groupID)
	}
	carrier := messageCarrier{msg}
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(
		datastreams.ExtractFromBase64Carrier(context.Background(), carrier),
		options.CheckpointParams{PayloadSize: getConsumerMsgSize(msg)},
		edges...,
	)
	if !ok {
		return
	}
	datastreams.InjectToBase64Carrier(ctx, carrier)
	if groupID != "" {
		// only track Kafka lag if a consumer group is set.
		// since there is no ack mechanism, we consider that messages read are committed right away.
		tracer.TrackKafkaCommitOffset(groupID, msg.Topic, int32(msg.Partition), msg.Offset)
	}
}

// WrapWriter wraps a kafka.Writer so requests are traced.
func WrapWriter(w *kafka.Writer, opts ...Option) *Writer {
	writer := &Writer{
		Writer: w,
		cfg:    newConfig(opts...),
	}

	if w.Addr.String() != "" {
		writer.bootstrapServers = w.Addr.String()
	}
	log.Debug("contrib/segmentio/kafka.go.v0: Wrapping Writer: %#v", writer.cfg)
	return writer
}

// Writer wraps a kafka.Writer with tracing config data
type Writer struct {
	*kafka.Writer
	kafkaConfig
	cfg *config
}

func (w *Writer) startSpan(ctx context.Context, msg *kafka.Message) ddtrace.Span {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(w.cfg.producerServiceName),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.KafkaBootstrapServers, w.bootstrapServers),
	}
	if w.Writer.Topic != "" {
		opts = append(opts, tracer.ResourceName("Produce Topic "+w.Writer.Topic))
	} else {
		opts = append(opts, tracer.ResourceName("Produce Topic "+msg.Topic))
	}
	if !math.IsNaN(w.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, w.cfg.analyticsRate))
	}
	carrier := messageCarrier{msg}
	span, _ := tracer.StartSpanFromContext(ctx, w.cfg.producerSpanName, opts...)
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		log.Debug("contrib/segmentio/kafka.go.v0: Failed to inject span context into carrier in writer, %v", err)
	}
	return span
}

func finishSpan(span ddtrace.Span, partition int, offset int64, err error) {
	span.SetTag(ext.MessagingKafkaPartition, partition)
	span.SetTag("offset", offset)
	span.Finish(tracer.WithError(err))
}

// WriteMessages calls kafka.go.v0.Writer.WriteMessages and traces the requests.
func (w *Writer) WriteMessages(ctx context.Context, msgs ...kafka.Message) error {
	// although there's only one call made to the SyncProducer, the messages are
	// treated individually, so we create a span for each one
	spans := make([]ddtrace.Span, len(msgs))
	for i := range msgs {
		spans[i] = w.startSpan(ctx, &msgs[i])
		setProduceCheckpoint(w.cfg.dataStreamsEnabled, &msgs[i], w.Writer)
	}
	err := w.Writer.WriteMessages(ctx, msgs...)
	for i, span := range spans {
		finishSpan(span, msgs[i].Partition, msgs[i].Offset, err)
	}
	return err
}

func setProduceCheckpoint(enabled bool, msg *kafka.Message, writer *kafka.Writer) {
	if !enabled || msg == nil {
		return
	}

	var topic string
	if writer.Topic != "" {
		topic = writer.Topic
	} else {
		topic = msg.Topic
	}

	edges := []string{"direction:out", "topic:" + topic, "type:kafka"}
	carrier := messageCarrier{msg}
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(
		datastreams.ExtractFromBase64Carrier(context.Background(), carrier),
		options.CheckpointParams{PayloadSize: getProducerMsgSize(msg)},
		edges...,
	)
	if !ok {
		return
	}

	// Headers will be dropped if the current protocol does not support them
	datastreams.InjectToBase64Carrier(ctx, carrier)
}

func getProducerMsgSize(msg *kafka.Message) (size int64) {
	for _, header := range msg.Headers {
		size += int64(len(header.Key) + len(header.Value))
	}
	if msg.Value != nil {
		size += int64(len(msg.Value))
	}
	if msg.Key != nil {
		size += int64(len(msg.Key))
	}
	return size
}

func getConsumerMsgSize(msg *kafka.Message) (size int64) {
	for _, header := range msg.Headers {
		size += int64(len(header.Key) + len(header.Value))
	}
	return size + int64(len(msg.Value)+len(msg.Key))
}
