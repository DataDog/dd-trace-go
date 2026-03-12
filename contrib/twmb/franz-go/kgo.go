// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package kgo

import (
	"context"
	"fmt"
	"sync"

	"github.com/DataDog/dd-trace-go/contrib/twmb/franz-go/v2/internal/tracing"
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

// NewClient calls kgo.NewClient with default tracing options.
func NewClient(opts ...kgo.Opt) (*Client, error) {
	return NewClientWithTracing(opts)
}

// NewClientWithTracing wraps kgo.NewClient with custom tracing options.
func NewClientWithTracing(kgoOpts []kgo.Opt, tracingOpts ...tracing.Option) (*Client, error) {
	wrapped := &Client{
		activeSpans: make([]*tracer.Span, 0),
	}
	kgoOpts = append(kgoOpts, kgo.WithHooks(wrapped))

	client, err := kgo.NewClient(kgoOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create kgo.Client: %w", err)
	}
	wrapped.Client = client
	wrapped.tracer = tracing.NewTracer(tracing.KafkaConfig{}, tracingOpts...)

	return wrapped, nil
}

// finishAndClearActiveSpans finishes all active spans and clears the list of
// active spans.
func (c *Client) finishAndClearActiveSpans() {
	c.activeSpansMu.Lock()
	for _, span := range c.activeSpans {
		span.Finish()
	}
	c.activeSpans = c.activeSpans[:0]
	c.activeSpansMu.Unlock()
}

func (c *Client) Close() {
	c.finishAndClearActiveSpans()
	c.Client.Close()
}

// PollFetches is a wrapper around kgo.PollFetches that finishes and clears the
// active spans before polling the next group. The next group's spans are going
// to be started via the OnFetchRecordUnbuffered hook.
func (c *Client) PollFetches(ctx context.Context) kgo.Fetches {
	c.finishAndClearActiveSpans()
	return c.Client.PollFetches(ctx)
}

// PollRecords is a wrapper around kgo.PollRecords that finishes and clears the
// active spans before polling the next group. The next group's spans are going
// to be started via the OnFetchRecordUnbuffered hook.
func (c *Client) PollRecords(ctx context.Context, maxPollRecords int) kgo.Fetches {
	c.finishAndClearActiveSpans()
	return c.Client.PollRecords(ctx, maxPollRecords)
}

// OnProduceRecordBuffered is a kgo hook called when a produced record is added
// to the producer's buffer. It starts the produce span and injects it to the
// record's headers.
func (c *Client) OnProduceRecordBuffered(r *kgo.Record) {
	wrapped := wrapRecord(r)
	span := c.tracer.StartProduceSpan(r.Context, wrapped)
	r.Context = tracer.ContextWithSpan(r.Context, span)
	c.tracer.SetProduceDSMCheckpoint(wrapped)
}

// OnProduceRecordUnbuffered is a kgo hook called when a produced record is
// removed from the producer's buffer. It finishes the produce span.
func (c *Client) OnProduceRecordUnbuffered(r *kgo.Record, err error) {
	span, ok := tracer.SpanFromContext(r.Context)
	if !ok {
		return
	}
	c.tracer.FinishProduceSpan(span, int(r.Partition), r.Offset, err)
}

// OnFetchRecordUnbuffered is a kgo hook called when a fetched record is
// removed from the consumer's buffer. It starts the consume span.
func (c *Client) OnFetchRecordUnbuffered(r *kgo.Record, polled bool) {
	// We shouldn't start a span if the record is not polled, because it
	// means it was discarded in some way before reaching user code.
	if !polled {
		return
	}

	if r.Context == nil {
		r.Context = context.Background()
	}

	// Consumer group ID is assigned lazily after the join/sync, so we
	// need to fetch it here if it hasn't been set yet.
	// See: https://github.com/twmb/franz-go/blob/ffcae1246a950c9cef434532f0867b0d94e41440/pkg/kgo/consumer_group.go#L287-L295
	c.tracerMu.Lock()
	if c.tracer.ConsumerGroupID() == "" {
		if groupID, _ := c.Client.GroupMetadata(); groupID != "" {
			c.tracer.SetConsumerGroupID(groupID)
		}
	}
	c.tracerMu.Unlock()

	wrapped := wrapRecord(r)
	span := c.tracer.StartConsumeSpan(r.Context, wrapped)
	r.Context = tracer.ContextWithSpan(r.Context, span)
	c.tracer.SetConsumeDSMCheckpoint(wrapped)

	c.activeSpansMu.Lock()
	c.activeSpans = append(c.activeSpans, span)
	c.activeSpansMu.Unlock()
}
