// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"context"
	"math"

	"github.com/IBM/sarama"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
	"github.com/DataDog/dd-trace-go/v2/ddtrace"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

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
	spans := make([]*tracer.Span, len(msgs))
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
		opt.apply(cfg)
	}
	instr.Logger().Debug("contrib/IBM/sarama: Wrapping Sync Producer: %#v", cfg)
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
		opt.apply(cfg)
	}
	instr.Logger().Debug("contrib/IBM/sarama: Wrapping Async Producer: %#v", cfg)
	if saramaConfig == nil {
		saramaConfig = sarama.NewConfig()
		saramaConfig.Version = sarama.V0_11_0_0
	} else if !saramaConfig.Version.IsAtLeast(sarama.V0_11_0_0) {
		instr.Logger().Error("Tracing Sarama async producer requires at least sarama.V0_11_0_0 version")
	}
	wrapped := &asyncProducer{
		AsyncProducer: p,
		input:         make(chan *sarama.ProducerMessage),
		successes:     make(chan *sarama.ProducerMessage),
		errors:        make(chan *sarama.ProducerError),
	}
	go func() {
		spans := make(map[uint64]*tracer.Span)
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
				if spanctx, spanFound := getProducerSpanContext(msg); spanFound {
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
				if spanctx, spanFound := getProducerSpanContext(err.Msg); spanFound {
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

func startProducerSpan(cfg *config, version sarama.KafkaVersion, msg *sarama.ProducerMessage) *tracer.Span {
	carrier := NewProducerMessageCarrier(msg)
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(cfg.producerServiceName),
		tracer.ResourceName("Produce Topic " + msg.Topic),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag(ext.Component, instrumentation.PackageIBMSarama),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.MessagingDestinationName, msg.Topic),
	}
	if !math.IsNaN(cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
	if len(cfg.producerCustomTags) > 0 {
		for tag, tagValueFn := range cfg.producerCustomTags {
			opts = append(opts, tracer.Tag(tag, tagValueFn(msg)))
		}
	}
	// if there's a span context in the headers, use that as the parent
	if spanctx, err := tracer.Extract(carrier); err == nil {
		// If there are span links as a result of context extraction, add them as a StartSpanOption
		if spanctx != nil && spanctx.SpanLinks() != nil {
			opts = append(opts, tracer.WithSpanLinks(spanctx.SpanLinks()))
		}
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span := tracer.StartSpan(cfg.producerSpanName, opts...)
	if version.IsAtLeast(sarama.V0_11_0_0) {
		// re-inject the span context so consumers can pick it up
		tracer.Inject(span.Context(), carrier)
	}
	return span
}

func finishProducerSpan(span *tracer.Span, partition int32, offset int64, err error) {
	span.SetTag(ext.MessagingKafkaPartition, partition)
	span.SetTag("offset", offset)
	span.Finish(tracer.WithError(err))
}

func getProducerSpanContext(msg *sarama.ProducerMessage) (ddtrace.SpanContext, bool) {
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
