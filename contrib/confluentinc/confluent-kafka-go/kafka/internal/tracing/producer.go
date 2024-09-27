// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracing

import (
	"context"
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/datastreams"
	"gopkg.in/DataDog/dd-trace-go.v1/datastreams/options"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/confluentinc/confluent-kafka-go/kafka"
)

type ProducerTracer struct {
	Ctx                context.Context
	DataStreamsEnabled bool
	LibraryVersion     int
	ProduceChannel     chan *kafka.Message
	Events             chan kafka.Event
	StartSpanConfig    StartSpanConfig
}

func NewProducerTracer(ctx context.Context, p *kafka.Producer, dataStreamsEnabled bool, startSpanConfig StartSpanConfig) *ProducerTracer {
	version, _ := kafka.LibraryVersion()
	tracer := &ProducerTracer{
		Ctx:                ctx,
		DataStreamsEnabled: dataStreamsEnabled,
		LibraryVersion:     version,
		StartSpanConfig:    startSpanConfig,
	}
	tracer.traceProduceChannel(p.ProduceChannel())
	tracer.traceEventsChannel(p.Events())
	return tracer
}

func (pt *ProducerTracer) traceProduceChannel(out chan *kafka.Message) {
	if out == nil {
		pt.ProduceChannel = out
		return
	}
	in := make(chan *kafka.Message, 1)
	go func() {
		for msg := range in {
			span := pt.startSpan(msg)
			setProduceCheckpoint(pt.DataStreamsEnabled, pt.LibraryVersion, msg)
			out <- msg
			span.Finish()
		}
	}()
	pt.ProduceChannel = in
}

func (pt *ProducerTracer) traceEventsChannel(in chan kafka.Event) {
	pt.Events = in
	if !pt.DataStreamsEnabled || in == nil {
		return
	}
	out := make(chan kafka.Event, 1)
	go func() {
		defer close(out)
		for evt := range in {
			if msg, ok := evt.(*kafka.Message); ok {
				trackProduceOffsets(pt.DataStreamsEnabled, msg, msg.TopicPartition.Error)
			}
			out <- evt
		}
	}()
	pt.Events = out
}

func (pt *ProducerTracer) WrapProduce(produceFn func(*kafka.Message, chan kafka.Event) error, msg *kafka.Message, deliveryChan chan kafka.Event) error {
	span := pt.startSpan(msg)

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
				trackProduceOffsets(pt.DataStreamsEnabled, msg, err)
			}
			span.Finish(tracer.WithError(err))
			oldDeliveryChan <- evt
		}()
	}

	setProduceCheckpoint(pt.DataStreamsEnabled, pt.LibraryVersion, msg)

	err := produceFn(msg, deliveryChan)
	// with no delivery channel or enqueue error, finish immediately
	if err != nil || deliveryChan == nil {
		span.Finish(tracer.WithError(err))
	}
	return err
}

func (pt *ProducerTracer) startSpan(msg *kafka.Message) ddtrace.Span {
	cfg := pt.StartSpanConfig
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(cfg.Service),
		tracer.ResourceName("Produce Topic " + *msg.TopicPartition.Topic),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag(ext.Component, ComponentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.MessagingKafkaPartition, msg.TopicPartition.Partition),
	}
	if cfg.BootstrapServers != "" {
		opts = append(opts, tracer.Tag(ext.KafkaBootstrapServers, cfg.BootstrapServers))
	}
	if !math.IsNaN(cfg.AnalyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.AnalyticsRate))
	}
	carrier := NewMessageCarrier(msg)
	if spanctx, err := tracer.Extract(carrier); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, _ := tracer.StartSpanFromContext(pt.Ctx, cfg.Operation, opts...)
	// inject the span context so consumers can pick it up
	tracer.Inject(span.Context(), carrier)
	return span
}

func (pt *ProducerTracer) Close() {
	close(pt.ProduceChannel)
}

type StartSpanConfig struct {
	Service          string
	Operation        string
	BootstrapServers string
	AnalyticsRate    float64
	TagFns           map[string]func(msg *kafka.Message) interface{}
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

func trackProduceOffsets(dataStreamsEnabled bool, msg *kafka.Message, err error) {
	if err != nil || !dataStreamsEnabled || msg.TopicPartition.Topic == nil {
		return
	}
	tracer.TrackKafkaProduceOffset(*msg.TopicPartition.Topic, msg.TopicPartition.Partition, int64(msg.TopicPartition.Offset))
}
