// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package kgo

import (
	"context"
	"sync"
	"sync/atomic"

	kgo "github.com/twmb/franz-go/pkg/kgo"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// Compile-time checks that tracingHook satisfies the hook interfaces it implements.
var (
	_ kgo.HookNewClient               = (*tracingHook)(nil)
	_ kgo.HookClientClosed            = (*tracingHook)(nil)
	_ kgo.HookProduceRecordBuffered   = (*tracingHook)(nil)
	_ kgo.HookProduceRecordUnbuffered = (*tracingHook)(nil)
	_ kgo.HookFetchRecordUnbuffered   = (*tracingHook)(nil)
	_ kgo.HookPollStart               = (*tracingHook)(nil)
)

const componentName = "twmb/franz-go"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageTwmbFranzGo)
}

const dsmEdgeTagCacheMax = 1000

// dsmEdgeTagCache caches edge-tag slices keyed by a composite string to avoid
// per-message []string allocations. Entries are shared read-only after store;
// callers must not mutate the returned slice.
type dsmEdgeTagCache struct {
	m    sync.Map
	size atomic.Int32
}

func (c *dsmEdgeTagCache) get(key string) []string {
	if v, ok := c.m.Load(key); ok {
		return v.([]string)
	}
	return nil
}

func (c *dsmEdgeTagCache) getOrStore(key string, tags []string) []string {
	if v, ok := c.m.Load(key); ok {
		return v.([]string)
	}
	if c.size.Load() >= dsmEdgeTagCacheMax {
		return tags
	}
	actual, loaded := c.m.LoadOrStore(key, tags)
	if !loaded {
		c.size.Add(1)
	}
	return actual.([]string)
}

type tracingHook struct {
	cfg           config
	client        *kgo.Client
	activeSpans   []*tracer.Span
	activeSpansMu sync.Mutex
	dsmTagCache   dsmEdgeTagCache
}

func newTracingHook(opts ...Option) *tracingHook {
	cfg := config{}
	defaults(&cfg)
	for _, o := range opts {
		o.apply(&cfg)
	}
	return &tracingHook{cfg: cfg}
}

// WithTracing creates return a kgo.Hook enabling
// tracing on the client
func WithTracing(opts ...Option) kgo.Opt {
	return kgo.WithHooks(newTracingHook(opts...))
}

func (h *tracingHook) finishAndClearActiveSpans() {
	h.activeSpansMu.Lock()
	for _, span := range h.activeSpans {
		span.Finish()
	}
	h.activeSpans = h.activeSpans[:0]
	h.activeSpansMu.Unlock()
}

// OnNewClient is a kgo hook called when the client is initialized
// before any client goroutines are started.
//
// We need a reference to the client in the TracingHook
// in order to retrieve metadata later on for DSM
func (h *tracingHook) OnNewClient(c *kgo.Client) {
	h.client = c
}

// OnPollStart is a kgo hook called at the start of every PollFetches or
// PollRecords call. It finishes the active consume spans from the previous poll
// before the new ones are created via OnFetchRecordUnbuffered.
func (h *tracingHook) OnPollStart(_ context.Context) {
	h.finishAndClearActiveSpans()
}

// OnClientClosed is a kgo hook called when the client is closed.
// It finishes any remaining active consume spans.
func (h *tracingHook) OnClientClosed(*kgo.Client) {
	h.finishAndClearActiveSpans()
}

// OnProduceRecordBuffered is a kgo hook called when a produced record is added
// to the producer's buffer. It starts the produce span and injects it into the
// record's headers.
func (h *tracingHook) OnProduceRecordBuffered(r *kgo.Record) {
	span := h.startProduceSpan(r.Context, r)
	r.Context = tracer.ContextWithSpan(r.Context, span)
	h.setProduceDSMCheckpoint(r)
}

// OnProduceRecordUnbuffered is a kgo hook called when a produced record is
// removed from the producer's buffer. It finishes the produce span.
func (h *tracingHook) OnProduceRecordUnbuffered(r *kgo.Record, err error) {
	span, ok := tracer.SpanFromContext(r.Context)
	if !ok {
		return
	}
	span.SetTag(ext.MessagingKafkaPartition, r.Partition)
	span.SetTag("offset", r.Offset)
	span.Finish(tracer.WithError(err))
}

// OnFetchRecordUnbuffered is a kgo hook called when a fetched record is
// removed from the consumer's buffer. It starts the consume span.
func (h *tracingHook) OnFetchRecordUnbuffered(r *kgo.Record, polled bool) {
	// We shouldn't start a span if the record is not polled, because it
	// means it was discarded in some way before reaching user code.
	if !polled {
		return
	}

	if r.Context == nil {
		r.Context = context.Background()
	}

	span := h.startConsumeSpan(r.Context, r)
	r.Context = tracer.ContextWithSpan(r.Context, span)
	h.setConsumeDSMCheckpoint(r)

	h.activeSpansMu.Lock()
	h.activeSpans = append(h.activeSpans, span)
	h.activeSpansMu.Unlock()
}

func (h *tracingHook) startConsumeSpan(ctx context.Context, r *kgo.Record) *tracer.Span {
	opts := []tracer.StartSpanOption{
		instrumentation.ServiceNameWithSource(h.cfg.consumerServiceName, h.cfg.serviceSource),
		tracer.ResourceName("Consume Topic " + r.Topic),
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.Tag(ext.MessagingKafkaPartition, r.Partition),
		tracer.Tag("offset", r.Offset),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.MessagingDestinationName, r.Topic),
		tracer.Measured(),
	}

	carrier := newKafkaHeadersCarrier(r)
	if spanctx, err := tracer.Extract(carrier); err == nil && spanctx != nil {
		if spanctx.SpanLinks() != nil {
			opts = append(opts, tracer.WithSpanLinks(spanctx.SpanLinks()))
		}
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, _ := tracer.StartSpanFromContext(ctx, h.cfg.consumerSpanName, opts...)

	// Re-inject the span context so consumers can pick it up.
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		instr.Logger().Debug("contrib/twmb/franz-go: Failed to inject span context into carrier for consume span, %s", err.Error())
	}
	return span
}

func (h *tracingHook) startProduceSpan(ctx context.Context, r *kgo.Record) *tracer.Span {
	opts := []tracer.StartSpanOption{
		instrumentation.ServiceNameWithSource(h.cfg.producerServiceName, h.cfg.serviceSource),
		tracer.ResourceName("Produce Topic " + r.Topic),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.MessagingDestinationName, r.Topic),
	}

	carrier := newKafkaHeadersCarrier(r)
	if spanctx, err := tracer.Extract(carrier); err == nil && spanctx != nil {
		if spanctx.SpanLinks() != nil {
			opts = append(opts, tracer.WithSpanLinks(spanctx.SpanLinks()))
		}
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, _ := tracer.StartSpanFromContext(ctx, h.cfg.producerSpanName, opts...)
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		instr.Logger().Debug("contrib/twmb/franz-go: Failed to inject span context into carrier for produce span, %s", err.Error())
	}
	return span
}

func (h *tracingHook) setConsumeDSMCheckpoint(r *kgo.Record) {
	if !h.cfg.dataStreamsEnabled {
		return
	}

	// The client should never be nil when we reach that point
	// but still checking to avoid a panic.
	var groupID string
	if h.client != nil {
		// GroupMetadata uses an atomic load internally, so it is safe to call
		// concurrently without additional locking.
		groupID, _ = h.client.GroupMetadata()
	}

	key := "in\x00" + r.Topic + "\x00" + groupID
	edges := h.dsmTagCache.get(key)
	if edges == nil {
		edges = []string{"direction:in", "topic:" + r.Topic, "type:kafka"}
		if groupID != "" {
			edges = append(edges, "group:"+groupID)
		}
		edges = h.dsmTagCache.getOrStore(key, edges)
	}

	carrier := newKafkaHeadersCarrier(r)
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(
		datastreams.ExtractFromBase64Carrier(r.Context, carrier),
		options.CheckpointParams{PayloadSize: msgSize(r)},
		edges...,
	)
	if !ok {
		return
	}
	datastreams.InjectToBase64Carrier(ctx, carrier)
	if groupID != "" {
		tracer.TrackKafkaCommitOffset(groupID, r.Topic, r.Partition, r.Offset)
	}
}

func (h *tracingHook) setProduceDSMCheckpoint(r *kgo.Record) {
	if !h.cfg.dataStreamsEnabled {
		return
	}
	key := "out\x00" + r.Topic
	edges := h.dsmTagCache.get(key)
	if edges == nil {
		edges = []string{"direction:out", "topic:" + r.Topic, "type:kafka"}
		edges = h.dsmTagCache.getOrStore(key, edges)
	}
	carrier := newKafkaHeadersCarrier(r)
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(
		datastreams.ExtractFromBase64Carrier(r.Context, carrier),
		options.CheckpointParams{PayloadSize: msgSize(r)},
		edges...,
	)
	if !ok {
		return
	}
	datastreams.InjectToBase64Carrier(ctx, carrier)
}

func msgSize(r *kgo.Record) int64 {
	var size int64
	for _, h := range r.Headers {
		size += int64(len(h.Key) + len(h.Value))
	}
	return size + int64(len(r.Value)+len(r.Key))
}
