// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package franz provides functions to trace the twmb/franz-go package (https://github.com/twmb/franz-go).
package franz

import (
	"context"
	"math"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"gopkg.in/DataDog/dd-trace-go.v1/datastreams"
	"gopkg.in/DataDog/dd-trace-go.v1/datastreams/options"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

const componentName = "twmb/franz-go"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/twmb/franz-go")
}

// A Client wraps a kgo.Client to provide tracing functionality.
type Client struct {
	*kgo.Client
	cfg *config
}

// WrapClient wraps a kgo.Client to enable tracing.
func WrapClient(client *kgo.Client, opts ...Option) *Client {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}
	log.Debug("contrib/twmb/franz-go: Wrapping Client: %#v", cfg)
	return &Client{
		Client: client,
		cfg:    cfg,
	}
}

// NewClient creates a new kgo.Client with tracing enabled.
func NewClient(opts []kgo.Opt, tracingOpts ...Option) (*Client, error) {
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	return WrapClient(client, tracingOpts...), nil
}

// Close closes the underlying kgo.Client.
func (c *Client) Close() {
	c.Client.Close()
}

// Produce wraps kgo.Client.Produce to add tracing.
func (c *Client) Produce(ctx context.Context, r *kgo.Record, promise func(*kgo.Record, error)) {
	span, ctx := tracer.StartSpanFromContext(ctx, c.cfg.producerSpanName,
		tracer.ServiceName(c.cfg.producerServiceName),
		tracer.ResourceName("Produce Topic "+r.Topic),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag(ext.MessagingKafkaPartition, r.Partition),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Measured(),
	)

	if !math.IsNaN(c.cfg.analyticsRate) {
		span.SetTag(ext.EventSampleRate, c.cfg.analyticsRate)
	}

	carrier := NewRecordHeadersCarrier(r.Headers)
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		// We still want to continue even if injection failed
		span.SetTag("error.msg", err.Error())
	}
	r.Headers = carrier.GetHeaders()

	wrappedPromise := func(r *kgo.Record, err error) {
		if promise != nil {
			promise(r, err)
		}
		if err != nil {
			span.SetTag(ext.Error, err)
		}
		if r != nil {
			span.SetTag(ext.MessagingKafkaPartition, r.Partition)
			span.SetTag("offset", r.Offset)
		}
		if c.cfg.dataStreamsEnabled && r != nil {
			tracer.TrackKafkaProduceOffset(r.Topic, r.Partition, r.Offset)
		}
		span.Finish()
	}

	c.Client.Produce(ctx, r, wrappedPromise)
}

// PollFetches wraps kgo.Client.PollFetches to add tracing.
func (c *Client) PollFetches(ctx context.Context) kgo.Fetches {
	fetches := c.Client.PollFetches(ctx)
	if len(fetches.Errors()) > 0 {
		return fetches
	}

	iter := fetches.RecordIter()
	if iter.Done() {
		return fetches
	}

	// Track metrics for data streams if enabled
	if c.cfg.dataStreamsEnabled {
		for !iter.Done() {
			record := iter.Next()
			if record == nil {
				continue
			}

			carrier := NewRecordHeadersCarrier(record.Headers)
			edges := []string{"direction:in", "topic:" + record.Topic, "type:kafka"}
			if c.cfg.groupID != "" {
				edges = append(edges, "group:"+c.cfg.groupID)
			}

			// Extract span context and set checkpoint with payload size
			dsCtx := datastreams.ExtractFromBase64Carrier(ctx, carrier)
			payloadSize := len(record.Value)
			if record.Key != nil {
				payloadSize += len(record.Key)
			}
			tracer.SetDataStreamsCheckpointWithParams(dsCtx, options.CheckpointParams{PayloadSize: int64(payloadSize)}, edges...)

			// Extract and propagate span context
			spanOpts := []tracer.StartSpanOption{
				tracer.ServiceName(c.cfg.consumerServiceName),
				tracer.ResourceName("Consume Topic " + record.Topic),
				tracer.SpanType(ext.SpanTypeMessageConsumer),
				tracer.Tag(ext.MessagingKafkaPartition, record.Partition),
				tracer.Tag("offset", record.Offset),
				tracer.Tag(ext.Component, componentName),
				tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
				tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
				tracer.Measured(),
			}

			if !math.IsNaN(c.cfg.analyticsRate) {
				spanOpts = append(spanOpts, tracer.Tag(ext.EventSampleRate, c.cfg.analyticsRate))
			}

			if spanctx, err := tracer.Extract(carrier); err == nil {
				if linksCtx, ok := spanctx.(ddtrace.SpanContextWithLinks); ok && linksCtx.SpanLinks() != nil {
					spanOpts = append(spanOpts, tracer.WithSpanLinks(linksCtx.SpanLinks()))
				}
				spanOpts = append(spanOpts, tracer.ChildOf(spanctx))
			}

			span := tracer.StartSpan(c.cfg.consumerSpanName, spanOpts...)
			tracer.Inject(span.Context(), carrier)
			span.Finish()
		}
	}

	return fetches
}

func (c *Client) RequestSharded(ctx context.Context, req kmsg.Request) []kgo.ResponseShard {
	return c.Client.RequestSharded(ctx, req)
}

func (c *Client) UpdateFetchMaxBytes(maxBytes, maxPartBytes int32) {
	c.Client.UpdateFetchMaxBytes(maxBytes, maxPartBytes)
}

func (c *Client) PauseFetchTopics(topics ...string) []string {
	return c.Client.PauseFetchTopics(topics...)
}

func (c *Client) ResumeFetchTopics(topics ...string) {
	c.Client.ResumeFetchTopics(topics...)
}

func (c *Client) PauseFetchPartitions(topicPartitions map[string][]int32) map[string][]int32 {
	return c.Client.PauseFetchPartitions(topicPartitions)
}

func (c *Client) ResumeFetchPartitions(topicPartitions map[string][]int32) {
	c.Client.ResumeFetchPartitions(topicPartitions)
}

func (c *Client) SetOffsets(setOffsets map[string]map[int32]kgo.EpochOffset) {
	c.Client.SetOffsets(setOffsets)
}

func (c *Client) AllowRebalance() {
	c.Client.AllowRebalance()
}
