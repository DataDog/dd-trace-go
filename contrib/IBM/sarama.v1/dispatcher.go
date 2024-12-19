// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"math"

	"github.com/IBM/sarama"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type dispatcher interface {
	Messages() <-chan *sarama.ConsumerMessage
}

type wrappedDispatcher struct {
	d        dispatcher
	messages chan *sarama.ConsumerMessage

	cfg *config
}

func wrapDispatcher(d dispatcher, cfg *config) *wrappedDispatcher {
	return &wrappedDispatcher{
		d:        d,
		messages: make(chan *sarama.ConsumerMessage),
		cfg:      cfg,
	}
}

func (w *wrappedDispatcher) Messages() <-chan *sarama.ConsumerMessage {
	return w.messages
}

func (w *wrappedDispatcher) Run() {
	msgs := w.d.Messages()
	var prev ddtrace.Span

	for msg := range msgs {
		// create the next span from the message
		opts := []tracer.StartSpanOption{
			tracer.ServiceName(w.cfg.consumerServiceName),
			tracer.ResourceName("Consume Topic " + msg.Topic),
			tracer.SpanType(ext.SpanTypeMessageConsumer),
			tracer.Tag(ext.MessagingKafkaPartition, msg.Partition),
			tracer.Tag("offset", msg.Offset),
			tracer.Tag(ext.Component, componentName),
			tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
			tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
			tracer.Measured(),
		}
		if !math.IsNaN(w.cfg.analyticsRate) {
			opts = append(opts, tracer.Tag(ext.EventSampleRate, w.cfg.analyticsRate))
		}
		// kafka supports headers, so try to extract a span context
		carrier := NewConsumerMessageCarrier(msg)
		if spanctx, err := tracer.Extract(carrier); err == nil {
			// If there are span links as a result of context extraction, add them as a StartSpanOption
			if linksCtx, ok := spanctx.(ddtrace.SpanContextWithLinks); ok && linksCtx.SpanLinks() != nil {
				opts = append(opts, tracer.WithSpanLinks(linksCtx.SpanLinks()))
			}
			opts = append(opts, tracer.ChildOf(spanctx))
		}
		next := tracer.StartSpan(w.cfg.consumerSpanName, opts...)
		// reinject the span context so consumers can pick it up
		tracer.Inject(next.Context(), carrier)
		setConsumeCheckpoint(w.cfg.dataStreamsEnabled, w.cfg.groupID, msg)
		w.messages <- msg

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
	close(w.messages)
}
