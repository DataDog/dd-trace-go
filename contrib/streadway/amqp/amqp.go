// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package amqp

import (
	"context"
	"maps"
	"strconv"

	"github.com/streadway/amqp"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const (
	componentName   = instrumentation.PackageStreadwayAMQP
	messagingSystem = "rabbitmq"
	routingKeyTag   = "messaging.rabbitmq.destination.routing_key"
	defaultExchange = "amq.default"
)

type parentKey struct{}

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(componentName)
}

func Publish(ch *amqp.Channel, exchange, key string, mandatory, immediate bool, msg amqp.Publishing, opts ...Option) error {
	return PublishWithContext(context.Background(), ch, exchange, key, mandatory, immediate, msg, opts...)
}

func PublishWithContext(ctx context.Context, ch *amqp.Channel, exchange, key string, mandatory, immediate bool, msg amqp.Publishing, opts ...Option) error {
	if ctx == nil {
		ctx = context.Background()
	}
	span := startPublishSpan(ctx, newConfig(opts), exchange, key, &msg)
	err := ch.Publish(exchange, key, mandatory, immediate, msg)
	span.Finish(tracer.WithError(err))
	return err
}

func Consume(ch *amqp.Channel, queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table, opts ...Option) (<-chan amqp.Delivery, error) {
	deliveries, err := ch.Consume(queue, consumer, autoAck, exclusive, noLocal, noWait, args)
	if err != nil {
		return deliveries, err
	}
	return traceDeliveries(context.Background(), newConfig(opts), queue, deliveries), nil
}

func Get(ch *amqp.Channel, queue string, autoAck bool, opts ...Option) (amqp.Delivery, bool, error) {
	d, ok, err := ch.Get(queue, autoAck)
	if err != nil || !ok {
		return d, ok, err
	}
	startConsumeSpan(newConfig(opts), "basic.get", queue, &d).Finish()
	return d, ok, nil
}

func ContextFromDelivery(ctx context.Context, d amqp.Delivery) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	carrier := TableCarrier(d.Headers)
	if parent, err := tracer.Extract(carrier); err == nil {
		ctx = context.WithValue(ctx, parentKey{}, parent)
	}
	return datastreams.ExtractFromBase64Carrier(ctx, carrier)
}

func traceDeliveries(ctx context.Context, cfg *config, queue string, in <-chan amqp.Delivery) <-chan amqp.Delivery {
	out := make(chan amqp.Delivery)
	go func() {
		defer close(out)
		for d := range in {
			span := startConsumeSpan(cfg, "basic.deliver", queue, &d)
			select {
			case out <- d:
				span.Finish()
			case <-ctx.Done():
				span.Finish()
				for range in {
				}
				return
			}
		}
	}()
	return out
}

func startPublishSpan(ctx context.Context, cfg *config, exchange, key string, msg *amqp.Publishing) *tracer.Span {
	destination := exchange
	if destination == "" {
		destination = defaultExchange
	}
	opts := []tracer.StartSpanOption{
		tracer.WithTags(map[string]any{
			ext.ResourceName:             "basic.publish " + destination + " -> " + key,
			ext.MessagingDestinationName: destination,
			routingKeyTag:                key,
		}),
		tracer.WithStartSpanConfig(cfg.producerBase),
	}
	if parent, ok := ctx.Value(parentKey{}).(*tracer.SpanContext); ok {
		if _, active := tracer.SpanFromContext(ctx); !active {
			opts = append(opts, tracer.ChildOf(parent))
		}
	}
	span, ctx := tracer.StartSpanFromContext(ctx, "amqp.publish", opts...)
	headers := make(amqp.Table, len(msg.Headers)+4)
	maps.Copy(headers, msg.Headers)
	msg.Headers = headers
	carrier := TableCarrier(headers)
	if cfg.dataStreamsEnabled {
		setProduceCheckpoint(ctx, exchange, key, msg, carrier)
	}
	tracer.Inject(span.Context(), carrier)
	return span
}

func setProduceCheckpoint(ctx context.Context, exchange, key string, msg *amqp.Publishing, carrier TableCarrier) {
	if _, ok := datastreams.PathwayFromContext(ctx); !ok {
		ctx = datastreams.ExtractFromBase64Carrier(ctx, carrier)
	}
	var edges []string
	if exchange == "" && key != "" {
		edges = []string{"direction:out", "topic:" + key, "type:rabbitmq"}
	} else {
		edges = []string{"direction:out", "exchange:" + exchange, "has_routing_key:" + strconv.FormatBool(key != ""), "type:rabbitmq"}
	}
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(ctx, options.CheckpointParams{PayloadSize: messageSize(msg.Body, msg.Headers)}, edges...)
	if ok {
		datastreams.InjectToBase64Carrier(ctx, carrier)
	}
}

func startConsumeSpan(cfg *config, operation, queue string, d *amqp.Delivery) *tracer.Span {
	if d.Headers == nil {
		d.Headers = make(amqp.Table, 4)
	}
	carrier := TableCarrier(d.Headers)
	tags := map[string]any{
		ext.ResourceName:             operation + " " + queue,
		ext.MessagingDestinationName: queue,
		routingKeyTag:                d.RoutingKey,
	}
	if d.Exchange != "" {
		tags["messaging.rabbitmq.exchange"] = d.Exchange
	}
	opts := []tracer.StartSpanOption{tracer.WithTags(tags), tracer.WithStartSpanConfig(cfg.consumerBase)}
	if parent, err := tracer.Extract(carrier); err == nil {
		opts = append(opts, tracer.ChildOf(parent))
	}
	span := tracer.StartSpan("amqp.consume", opts...)
	if cfg.dataStreamsEnabled {
		ctx := datastreams.ExtractFromBase64Carrier(tracer.ContextWithSpan(context.Background(), span), carrier)
		ctx, ok := tracer.SetDataStreamsCheckpointWithParams(ctx, options.CheckpointParams{PayloadSize: messageSize(d.Body, d.Headers)}, "direction:in", "topic:"+queue, "type:rabbitmq")
		if ok {
			datastreams.InjectToBase64Carrier(ctx, carrier)
		}
	}
	tracer.Inject(span.Context(), carrier)
	return span
}

func messageSize(body []byte, headers amqp.Table) int64 {
	size := int64(len(body))
	for k, v := range headers {
		size += int64(len(k))
		if s, ok := headerString(v); ok {
			size += int64(len(s))
		}
	}
	return size
}
