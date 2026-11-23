// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"math"

	"github.com/IBM/sarama"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type dispatcher interface {
	Messages() <-chan *sarama.ConsumerMessage
}

type wrappedDispatcher struct {
	d        dispatcher
	messages chan *sarama.ConsumerMessage

	cfg *config
	// spanCfg holds the tags that are constant for every message consumed
	// through this dispatcher (component, span kind, messaging system,
	// service name, measured, and any static analytics rate). It is built
	// once when the dispatcher is created (see newConsumerSpanConfig) and
	// merged into each message span via WithStartSpanConfig, instead of
	// rebuilding a Tag() closure per tag on every message.
	spanCfg *tracer.StartSpanConfig
}

func wrapDispatcher(d dispatcher, cfg *config) *wrappedDispatcher {
	return &wrappedDispatcher{
		d:        d,
		messages: make(chan *sarama.ConsumerMessage),
		cfg:      cfg,
		spanCfg:  newConsumerSpanConfig(cfg),
	}
}

// newConsumerSpanConfig builds the base StartSpanConfig holding the tags
// that stay constant for every message consumed through a dispatcher with
// the given config, so per-message calls don't need to rebuild them.
func newConsumerSpanConfig(cfg *config) *tracer.StartSpanConfig {
	opts := []tracer.StartSpanOption{
		instrumentation.ServiceNameWithSource(cfg.consumerServiceName, cfg.serviceSource),
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.Tag(ext.Component, instrumentation.PackageIBMSarama),
		tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Measured(),
	}
	if !math.IsNaN(cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
	return tracer.NewStartSpanConfig(opts...)
}

func (w *wrappedDispatcher) Messages() <-chan *sarama.ConsumerMessage {
	return w.messages
}

func (w *wrappedDispatcher) Run() {
	msgs := w.d.Messages()
	var prev *tracer.Span

	for msg := range msgs {
		// create the next span from the message. Partition/offset/topic and
		// the cluster ID (fetched asynchronously in the background after the
		// dispatcher is created, see startClusterIDFetch) are genuinely
		// per-message/mutable, so they stay dynamic instead of moving into
		// the static spanCfg base.
		tags := map[string]any{
			ext.ResourceName:             "Consume Topic " + msg.Topic,
			ext.MessagingKafkaPartition:  msg.Partition,
			"offset":                     msg.Offset,
			ext.MessagingDestinationName: msg.Topic,
		}
		if clusterID := w.cfg.ClusterID(); clusterID != "" {
			tags[ext.MessagingKafkaClusterID] = clusterID
		}
		opts := []tracer.StartSpanOption{
			tracer.WithTags(tags),
			tracer.WithStartSpanConfig(w.spanCfg),
		}
		if len(w.cfg.consumerCustomTags) > 0 {
			customTags := make(map[string]any, len(w.cfg.consumerCustomTags))
			for tag, tagValueFn := range w.cfg.consumerCustomTags {
				customTags[tag] = tagValueFn(msg)
			}
			// Applied last so a custom tag wins over both the cached
			// static base and the tags above on key collision, matching
			// pre-migration behavior where custom-tag options were
			// appended last.
			opts = append(opts, tracer.WithTags(customTags))
		}
		// kafka supports headers, so try to extract a span context
		carrier := NewConsumerMessageCarrier(msg)
		if spanctx, err := tracer.Extract(carrier); err == nil {
			// If there are span links as a result of context extraction, add them as a StartSpanOption
			if spanctx != nil && spanctx.SpanLinks() != nil {
				opts = append(opts, tracer.WithSpanLinks(spanctx.SpanLinks()))
			}
			opts = append(opts, tracer.ChildOf(spanctx))
		}
		next := tracer.StartSpan(w.cfg.consumerSpanName, opts...)
		// reinject the span context so consumers can pick it up
		tracer.Inject(next.Context(), carrier)
		setConsumeCheckpoint(w.cfg.dataStreamsEnabled, w.cfg.groupID, w.cfg.ClusterID(), msg)
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
