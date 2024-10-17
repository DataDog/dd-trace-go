// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracing

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func WrapConsumeEventsChannel[E any, TE Event](tr *KafkaTracer, in chan E, consumer Consumer, translateFn func(E) TE) chan E {
	// in will be nil when consuming via the events channel is not enabled
	if in == nil {
		return nil
	}

	out := make(chan E, 1)
	go func() {
		defer close(out)
		for evt := range in {
			tEvt := translateFn(evt)
			var next ddtrace.Span

			// only trace messages
			if msg, ok := tEvt.KafkaMessage(); ok {
				next = tr.StartConsumeSpan(msg)
				tr.SetConsumeCheckpoint(msg)
			} else if offset, ok := tEvt.KafkaOffsetsCommitted(); ok {
				tr.TrackCommitOffsets(offset.GetOffsets(), offset.GetError())
				tr.TrackHighWatermarkOffset(offset.GetOffsets(), consumer)
			}

			out <- evt

			if tr.PrevSpan != nil {
				tr.PrevSpan.Finish()
			}
			tr.PrevSpan = next
		}
		// finish any remaining span
		if tr.PrevSpan != nil {
			tr.PrevSpan.Finish()
			tr.PrevSpan = nil
		}
	}()
	return out
}

func (tr *KafkaTracer) StartConsumeSpan(msg Message) ddtrace.Span {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(tr.consumerServiceName),
		tracer.ResourceName("Consume Topic " + msg.GetTopicPartition().GetTopic()),
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.Tag(ext.MessagingKafkaPartition, msg.GetTopicPartition().GetPartition()),
		tracer.Tag("offset", msg.GetTopicPartition().GetOffset()),
		tracer.Tag(ext.Component, ComponentName(tr.ckgoVersion)),
		tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Measured(),
	}
	if tr.bootstrapServers != "" {
		opts = append(opts, tracer.Tag(ext.KafkaBootstrapServers, tr.bootstrapServers))
	}
	if tr.tagFns != nil {
		for key, tagFn := range tr.tagFns {
			opts = append(opts, tracer.Tag(key, tagFn(msg)))
		}
	}
	if !math.IsNaN(tr.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, tr.analyticsRate))
	}
	// kafka supports headers, so try to extract a span context
	carrier := MessageCarrier{msg: msg}
	if spanctx, err := tracer.Extract(carrier); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, _ := tracer.StartSpanFromContext(tr.ctx, tr.consumerSpanName, opts...)
	// reinject the span context so consumers can pick it up
	tracer.Inject(span.Context(), carrier)
	return span
}
