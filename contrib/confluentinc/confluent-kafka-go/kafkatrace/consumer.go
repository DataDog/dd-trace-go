// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkatrace

import (
	"math"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func WrapConsumeEventsChannel[E any, TE Event](tr *Tracer, in chan E, consumer Consumer, translateFn func(E) TE) chan E {
	// in will be nil when consuming via the events channel is not enabled
	if in == nil {
		return nil
	}

	out := make(chan E, 1)
	go func() {
		defer close(out)
		for evt := range in {
			tEvt := translateFn(evt)
			var next *tracer.Span

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

// newConsumerSpanConfig builds the base StartSpanConfig holding the tags
// that stay constant for every message consumed through a Tracer with the
// given config, so per-message calls don't need to rebuild them.
//
// The analytics rate is deliberately NOT included here: StartConsumeSpan
// applies it as a separate, later Tag() call instead, so it's guaranteed to
// run after any WithCustomTag callback and win a key collision with one
// targeting ext.EventSampleRate — matching pre-migration behavior, where
// this Tag() call was always appended after the tagFns loop.
func newConsumerSpanConfig(tr *Tracer) *tracer.StartSpanConfig {
	opts := []tracer.StartSpanOption{
		instrumentation.ServiceNameWithSource(tr.consumerServiceName, tr.serviceSource),
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.Tag(ext.Component, ComponentName(tr.ckgoVersion)),
		tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Measured(),
	}
	if tr.bootstrapServers != "" {
		opts = append(opts, tracer.Tag(ext.KafkaBootstrapServers, tr.bootstrapServers))
	}
	return tracer.NewStartSpanConfig(opts...)
}

func (tr *Tracer) StartConsumeSpan(msg Message) *tracer.Span {
	// Partition, offset, topic, and the Kafka cluster ID (fetched
	// asynchronously in the background after the Tracer is created, see
	// startClusterIDFetch in the calling kafka package) are genuinely
	// per-message/mutable, so they stay dynamic instead of moving into the
	// static consumerSpanCfg base. tagFns are user-supplied per-message
	// callbacks and are dynamic by nature.
	tags := map[string]any{
		ext.ResourceName:             "Consume Topic " + msg.GetTopicPartition().GetTopic(),
		ext.MessagingKafkaPartition:  msg.GetTopicPartition().GetPartition(),
		"offset":                     msg.GetTopicPartition().GetOffset(),
		ext.MessagingDestinationName: msg.GetTopicPartition().GetTopic(),
	}
	if tr.ClusterID() != "" {
		tags[ext.MessagingKafkaClusterID] = tr.ClusterID()
	}
	opts := []tracer.StartSpanOption{
		tracer.WithTags(tags),
		tracer.WithStartSpanConfig(tr.consumerSpanCfg),
	}
	if tr.tagFns != nil {
		customTags := make(map[string]any, len(tr.tagFns))
		for key, tagFn := range tr.tagFns {
			customTags[key] = tagFn(msg)
		}
		// Applied after the cached static base so a custom tag wins over
		// it on key collision, matching pre-migration behavior where
		// custom-tag options were appended last.
		opts = append(opts, tracer.WithTags(customTags))
	}
	if !math.IsNaN(tr.analyticsRate) {
		// Applied last, after any custom tag, so the configured analytics
		// rate always wins a collision with a custom tag targeting
		// ext.EventSampleRate. Pre-migration, this Tag() call was always
		// appended after the tagFns loop for the same reason.
		opts = append(opts, tracer.Tag(ext.EventSampleRate, tr.analyticsRate))
	}
	// kafka supports headers, so try to extract a span context
	carrier := MessageCarrier{msg: msg}
	if spanctx, err := tracer.Extract(carrier); err == nil {
		// If there are span links as a result of context extraction, add them as a StartSpanOption
		if spanctx != nil && spanctx.SpanLinks() != nil {
			opts = append(opts, tracer.WithSpanLinks(spanctx.SpanLinks()))
		}
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, _ := tracer.StartSpanFromContext(tr.ctx, tr.consumerSpanName, opts...)
	// reinject the span context so consumers can pick it up
	tracer.Inject(span.Context(), carrier)
	return span
}
