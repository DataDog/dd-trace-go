// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkatrace

import (
	"sync"

	"math"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func WrapProduceChannel[M any, TM Message](tr *Tracer, out chan M, translateFn func(M) TM) chan M {
	if out == nil {
		return out
	}
	in := make(chan M, 1)
	go func() {
		for msg := range in {
			tMsg := translateFn(msg)
			span := tr.StartProduceSpan(tMsg)
			tr.SetProduceCheckpoint(tMsg)
			out <- msg
			span.Finish()
		}
	}()
	return in
}

func WrapProduceEventsChannel[E any, TE Event](tr *Tracer, in chan E, translateFn func(E) TE) chan E {
	if in == nil {
		return nil
	}
	out := make(chan E, 1)
	go func() {
		defer close(out)
		for evt := range in {
			tEvt := translateFn(evt)
			if msg, ok := tEvt.KafkaMessage(); ok {
				tr.TrackProduceOffsets(msg)
			}
			out <- evt
		}
	}()
	return out
}

// newProducerSpanConfig builds the base StartSpanConfig holding the tags
// that stay constant for every message produced through a Tracer with the
// given config, so per-message calls don't need to rebuild them.
func newProducerSpanConfig(tr *Tracer) *tracer.StartSpanConfig {
	opts := []tracer.StartSpanOption{
		instrumentation.ServiceNameWithSource(tr.producerServiceName, tr.serviceSource),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag(ext.Component, ComponentName(tr.ckgoVersion)),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
	}
	if tr.bootstrapServers != "" {
		opts = append(opts, tracer.Tag(ext.KafkaBootstrapServers, tr.bootstrapServers))
	}
	if !math.IsNaN(tr.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, tr.analyticsRate))
	}
	return tracer.NewStartSpanConfig(opts...)
}

func (tr *Tracer) StartProduceSpan(msg Message) *tracer.Span {
	// Partition, topic, and the Kafka cluster ID (fetched asynchronously in
	// the background after the Tracer is created, see startClusterIDFetch in
	// the calling kafka package) are genuinely per-message/mutable, so they
	// stay dynamic instead of moving into the static producerSpanCfg base.
	tags := map[string]any{
		ext.ResourceName:             "Produce Topic " + msg.GetTopicPartition().GetTopic(),
		ext.MessagingKafkaPartition:  msg.GetTopicPartition().GetPartition(),
		ext.MessagingDestinationName: msg.GetTopicPartition().GetTopic(),
	}
	if tr.ClusterID() != "" {
		tags[ext.MessagingKafkaClusterID] = tr.ClusterID()
	}
	opts := []tracer.StartSpanOption{
		tracer.WithTags(tags),
		tracer.WithStartSpanConfig(tr.producerSpanCfg),
	}
	// if there's a span context in the headers, use that as the parent
	carrier := NewMessageCarrier(msg)
	if spanctx, err := tracer.Extract(carrier); err == nil {
		// If there are span links as a result of context extraction, add them as a StartSpanOption
		if spanctx != nil && spanctx.SpanLinks() != nil {
			opts = append(opts, tracer.WithSpanLinks(spanctx.SpanLinks()))
		}
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, _ := tracer.StartSpanFromContext(tr.ctx, tr.producerSpanName, opts...)
	// inject the span context so consumers can pick it up
	tracer.Inject(span.Context(), carrier)
	return span
}

func WrapDeliveryChannel[E any, TE Event](tr *Tracer, deliveryChan chan E, span *tracer.Span, translateFn func(E) TE) (chan E, chan error) {
	return WrapDeliveryChannelGuarded(tr, deliveryChan, nil, span, translateFn)
}

// DeliveryGuard coordinates delivery forwarders that send to a channel owned
// by the producer (its Events channel) with the producer's Close, which
// closes that channel. Without it, a forwarder blocked on its send can panic
// with "send on closed channel" (see #5806).
type DeliveryGuard struct {
	mu     sync.Mutex
	closed bool
	done   chan struct{}
	wg     sync.WaitGroup
}

// NewDeliveryGuard returns a DeliveryGuard ready to use.
func NewDeliveryGuard() *DeliveryGuard {
	return &DeliveryGuard{done: make(chan struct{})}
}

// Close stops all forwarding and waits for in-flight sends to give up. Call
// it before the guarded channel is closed. Later delivery events are dropped.
func (g *DeliveryGuard) Close() {
	g.mu.Lock()
	if !g.closed {
		g.closed = true
		close(g.done)
	}
	g.mu.Unlock()
	g.wg.Wait()
}

// begin registers a send. It returns false when the guard is closed.
func (g *DeliveryGuard) begin() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.wg.Add(1)
	return true
}

// WrapDeliveryChannelGuarded is like WrapDeliveryChannel, but if guard is
// not nil, the forward to deliveryChan stops once guard is closed.
func WrapDeliveryChannelGuarded[E any, TE Event](tr *Tracer, deliveryChan chan E, guard *DeliveryGuard, span *tracer.Span, translateFn func(E) TE) (chan E, chan error) {
	// if the user has selected a delivery channel, we will wrap it and
	// wait for the delivery event to finish the span
	if deliveryChan == nil {
		return nil, nil
	}
	wrapped := make(chan E)
	errChan := make(chan error, 1)
	go func() {
		var err error
		select {
		case evt := <-wrapped:
			tEvt := translateFn(evt)
			if msg, ok := tEvt.KafkaMessage(); ok {
				// delivery errors are returned via TopicPartition.Error
				err = msg.GetTopicPartition().GetError()
				tr.TrackProduceOffsets(msg)
			}
			if guard == nil {
				deliveryChan <- evt
			} else if guard.begin() {
				select {
				case deliveryChan <- evt:
				case <-guard.done:
				}
				guard.wg.Done()
			}
		case e := <-errChan:
			err = e
		}
		span.Finish(tracer.WithError(err))
	}()
	return wrapped, errChan
}
