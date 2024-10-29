// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka // import "github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2"

import (
	"context"
	"math"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/segmentio/kafka-go"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageSegmentioKafkaGo)
}

// NewReader calls kafka.NewReader and wraps the resulting Consumer.
func NewReader(conf kafka.ReaderConfig, opts ...Option) *Reader {
	return WrapReader(kafka.NewReader(conf), opts...)
}

// WrapReader wraps a kafka.Reader so that any consumed events are traced.
func WrapReader(c *kafka.Reader, opts ...Option) *Reader {
	wrapped := &Reader{
		Reader: c,
	}
	kafkaCfg := KafkaConfig{}
	if c.Config().Brokers != nil {
		kafkaCfg.BootstrapServers = strings.Join(c.Config().Brokers, ",")
	}
	if c.Config().GroupID != "" {
		kafkaCfg.ConsumerGroupID = c.Config().GroupID
	}

	instr.Logger().Debug("contrib/segmentio/kafka-go.v0/kafka: Wrapping Reader: %#v", wrapped.cfg)
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
	tracer *Tracer
	kafkaConfig
	cfg  *config
	prev *tracer.Span
}

func (r *Reader) startSpan(ctx context.Context, msg *kafka.Message) *tracer.Span {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(r.cfg.consumerServiceName),
		tracer.ResourceName("Consume Topic " + msg.Topic),
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.Tag(ext.MessagingKafkaPartition, msg.Partition),
		tracer.Tag("offset", msg.Offset),
		tracer.Tag(ext.Component, instrumentation.PackageSegmentioKafkaGo),
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
		instr.Logger().Debug("contrib/segmentio/kafka-go: Failed to inject span context into carrier in reader, %v", err)
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
	tMsg := wrapMessage(&msg)
	r.prev = r.tracer.StartConsumeSpan(ctx, tMsg)
	r.tracer.SetConsumeDSMCheckpoint(tMsg)
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
	tMsg := wrapMessage(&msg)
	r.prev = r.tracer.StartConsumeSpan(ctx, tMsg)
	r.tracer.SetConsumeDSMCheckpoint(tMsg)
	return msg, nil
}

// Writer wraps a kafka.Writer with tracing config data
type KafkaWriter struct {
	*kafka.Writer
	tracer *Tracer
}

// NewWriter calls kafka.NewWriter and wraps the resulting Producer.
func NewWriter(conf kafka.WriterConfig, opts ...Option) *KafkaWriter {
	return WrapWriter(kafka.NewWriter(conf), opts...)
}

// WrapWriter wraps a kafka.Writer so requests are traced.
func WrapWriter(w *kafka.Writer, opts ...Option) *KafkaWriter {
	writer := &KafkaWriter{
		Writer: w,
	}
	kafkaCfg := KafkaConfig{}
	if w.Addr.String() != "" {
		kafkaCfg.BootstrapServers = w.Addr.String()
	}
	instr.Logger().Debug("contrib/segmentio/kafka-go: Wrapping Writer: %#v", writer.tracer.kafkaCfg)
	return writer
}

func (w *KafkaWriter) startSpan(ctx context.Context, msg *kafka.Message) *tracer.Span {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(w.tracer.producerServiceName),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag(ext.Component, instrumentation.PackageSegmentioKafkaGo),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.KafkaBootstrapServers, w.tracer.kafkaCfg.BootstrapServers),
	}
	if w.Writer.Topic != "" {
		opts = append(opts, tracer.ResourceName("Produce Topic "+w.Writer.Topic))
	} else {
		opts = append(opts, tracer.ResourceName("Produce Topic "+msg.Topic))
	}
	if !math.IsNaN(w.tracer.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, w.tracer.analyticsRate))
	}
	carrier := messageCarrier{msg}
	span, _ := tracer.StartSpanFromContext(ctx, w.tracer.producerSpanName, opts...)
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		instr.Logger().Debug("contrib/segmentio/kafka-go: Failed to inject span context into carrier in writer, %v", err)
	}
	return span
}

func finishSpan(span *tracer.Span, partition int, offset int64, err error) {
	span.SetTag(ext.MessagingKafkaPartition, partition)
	span.SetTag("offset", offset)
	span.Finish(tracer.WithError(err))
}

// WriteMessages calls kafka-go.Writer.WriteMessages and traces the requests.
func (w *KafkaWriter) WriteMessages(ctx context.Context, msgs ...kafka.Message) error {
	// although there's only one call made to the SyncProducer, the messages are
	// treated individually, so we create a span for each one
	spans := make([]*tracer.Span, len(msgs))
	for i := range msgs {
		tMsg := wrapMessage(&msgs[i])
		tWriter := wrapTracingWriter(w.Writer)
		spans[i] = w.tracer.StartProduceSpan(ctx, tWriter, tMsg)
		w.tracer.SetProduceDSMCheckpoint(tMsg, tWriter)
	}
	err := w.Writer.WriteMessages(ctx, msgs...)
	for i, span := range spans {
		w.tracer.FinishProduceSpan(span, msgs[i].Partition, msgs[i].Offset, err)
	}
	return err
}
