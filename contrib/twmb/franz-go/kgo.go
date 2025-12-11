// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package kgo

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/contrib/twmb/franz-go/v2/internal/tracing"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	kgo "github.com/twmb/franz-go/pkg/kgo"
)

type Client struct {
	*kgo.Client
	activeSpans   []*tracer.Span
	activeSpansMu sync.Mutex
	tracerMu      sync.Mutex
	tracer        *tracing.Tracer
}

// ClientOptions simply wraps a variadic list of kgo.Opt into a slice of kgo.Opt
// to make this package's NewClient easier to use.
func ClientOptions(opts ...kgo.Opt) []kgo.Opt {
	return opts
}

// Note: This function's signature differs from franz-go's kgo.NewClient,
// which has a variadic list of kgo.Opt, since we already must have a variadic
// list of tracing options.
func NewClient(kgoOpts []kgo.Opt, tracingOpts ...tracing.Option) (*Client, error) {
	wrapped := &Client{
		activeSpans: make([]*tracer.Span, 0),
	}
	kgoOpts = append(kgoOpts, kgo.WithHooks(wrapped))

	client, err := kgo.NewClient(kgoOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create kgo.Client: %w", err)
	}
	wrapped.Client = client

	// We can only set the consumer group ID, but not the bootstrap servers
	// since kgo.Client doesn't expose seed brokers. Setting the bootstrap servers is done in the OnBrokerConnect hook.
	// ???: What to do, since the groupID can be an empty string? Nothing, right?
	groupID, _ := wrapped.Client.GroupMetadata()
	wrapped.tracer.SetKafkaConfig(tracing.KafkaConfig{
		ConsumerGroupID: groupID,
	})

	return wrapped, nil
}

func (c *Client) finishAndClearActiveSpans() {
	c.activeSpansMu.Lock()
	for _, span := range c.activeSpans {
		span.Finish()
	}
	c.activeSpans = c.activeSpans[:0]
	c.activeSpansMu.Unlock()
}

func (c *Client) PollFetches(ctx context.Context) kgo.Fetches {
	c.finishAndClearActiveSpans()
	return c.Client.PollFetches(ctx)
}

func (c *Client) PollRecords(ctx context.Context, maxPollRecords int) kgo.Fetches {
	c.finishAndClearActiveSpans()
	return c.Client.PollRecords(ctx, maxPollRecords)
}

func (c *Client) OnProduceRecordBuffered(r *kgo.Record) {
	span := c.tracer.StartProduceSpan(r.Context, wrapRecord(r))
	r.Context = tracer.ContextWithSpan(r.Context, span)
}

func (c *Client) OnProduceRecordUnbuffered(r *kgo.Record, err error) {
	span, ok := tracer.SpanFromContext(r.Context)
	if !ok {
		return
	}
	c.tracer.FinishProduceSpan(span, int(r.Partition), r.Offset, err)
}

// OnFetchRecordBuffered is called when a record is fetched and ready to be consumed
func (c *Client) OnFetchRecordBuffered(r *kgo.Record) {
	slog.Info("OnFetchRecordBuffered")
	slog.Info("Record buffered", "offset", r.Offset)
	slog.Info("OnFetchRecordBuffered done")
}

// OnFetchRecordUnbuffered is called when a record is consumed or discarded
func (c *Client) OnFetchRecordUnbuffered(r *kgo.Record, polled bool) {
	slog.Info("OnFetchRecordUnbuffered", "polled", polled)

	// We shouldn't start a span if the record is not polled, because it
	// means it was discarded in some way before reaching user code.
	if !polled {
		return
	}

	opts := []tracer.StartSpanOption{
		tracer.ServiceName("consumer-service"),
		tracer.ResourceName("Consume Topic " + r.Topic),
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.Tag(ext.MessagingKafkaPartition, r.Partition),
		tracer.Tag("offset", r.Offset),
		// tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.MessagingDestinationName, r.Topic),
		tracer.Measured(),
	}

	spanctx, err := ExtractSpanContext(r)
	if err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}

	span, ctx := tracer.StartSpanFromContext(r.Context, "kafka.consume", opts...)
	r.Context = ctx

	c.activeSpansMu.Lock()
	c.activeSpans = append(c.activeSpans, span)
	c.activeSpansMu.Unlock()

	slog.Info("Record unbuffered", "offset", r.Offset, "polled", polled)
	slog.Info("OnFetchRecordUnbuffered done")
}

// OnBrokerConnect is used to obtain the Client's seed brokers.
// Since franz-go doesn't expose the seed brokers after client creation,
// we intercept broker connections to identify and collect them.
// Seed brokers are distinguished by having negative NodeIDs (e.g., -1, -2)
// and nil Rack values: https://pkg.go.dev/github.com/twmb/franz-go/pkg/kgo#BrokerMetadata
func (c *Client) OnBrokerConnect(meta kgo.BrokerMetadata, initDur time.Duration, conn net.Conn, err error) {
	if meta.NodeID < 0 && meta.Rack == nil {
		addr := fmt.Sprintf("%s:%d", meta.Host, meta.Port)
		c.tracerMu.Lock()
		c.tracer.AddBootstrapServer(addr)
		c.tracerMu.Unlock()
	}
}
