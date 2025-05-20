// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkatrace

import (
	"math"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
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
				var tPartitionError = msg.GetTopicPartition().GetError()
				if tPartitionError.IsUnknownServerError() {
					instr.Logger().Error("Kafka Broker responded with UNKNOWN_SERVER_ERROR (-1). Please look at " +
						"broker logs for more information. The tracer requires support for Kafka headers to function.")
				}

				tr.TrackProduceOffsets(msg)
			}
			out <- evt
		}
	}()
	return out
}

func (tr *Tracer) StartProduceSpan(msg Message) *tracer.Span {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(tr.producerServiceName),
		tracer.ResourceName("Produce Topic " + msg.GetTopicPartition().GetTopic()),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag(ext.Component, ComponentName(tr.ckgoVersion)),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.MessagingKafkaPartition, msg.GetTopicPartition().GetPartition()),
		tracer.Tag(ext.MessagingDestinationName, msg.GetTopicPartition().GetTopic()),
	}
	if tr.bootstrapServers != "" {
		opts = append(opts, tracer.Tag(ext.KafkaBootstrapServers, tr.bootstrapServers))
	}
	if !math.IsNaN(tr.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, tr.analyticsRate))
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
				var tPartitionError = msg.GetTopicPartition().GetError()
				err = tPartitionError.Error()
				if tPartitionError.IsUnknownServerError() {
					instr.Logger().Error("Kafka Broker responded with UNKNOWN_SERVER_ERROR (-1). Please look at " +
						"broker logs for more information. The tracer requires support for Kafka headers to function.")
				}
				tr.TrackProduceOffsets(msg)
			}
			deliveryChan <- evt
		case e := <-errChan:
			err = e
		}
		span.Finish(tracer.WithError(err))
	}()
	return wrapped, errChan
}
