// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package goka provides Datadog APM tracing and Data Streams Monitoring (DSM)
// for the goka Kafka-streams library (github.com/lovoo/goka).
//
// goka does not expose a seam that the IBM/sarama integration can wrap (it uses
// its own sarama consumer-group handler internally), so this package instruments
// through goka's public extension points instead: WithContextWrapper on the
// consume side and per-emit headers on the produce side.
//
// Wire it into a processor by registering the context wrapper and wrapping each
// input callback:
//
//	tr := goka.NewTracer(goka.WithService("orders"), goka.WithDataStreams())
//	p, err := goka.NewProcessor(brokers, goka.DefineGroup(group,
//		goka.Input(topic, codec, tr.WrapCallback(handle)),
//	), goka.WithContextWrapper(tr.WrapContext))
package goka

import (
	"context"
	"fmt"
	"time"

	"github.com/lovoo/goka"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const componentName = "lovoo/goka"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageLovooGoka)
}

// Tracer instruments a goka processor. It is safe for concurrent use: the goka
// context wrapper runs concurrently across partitions, and each invocation
// builds its own per-message state.
type Tracer struct {
	cfg config
}

// NewTracer returns a Tracer configured with the given options.
func NewTracer(opts ...Option) *Tracer {
	cfg := config{}
	defaults(&cfg)
	for _, o := range opts {
		o.apply(&cfg)
	}
	return &Tracer{cfg: cfg}
}

// WrapContext is a goka.ContextWrapper (func(goka.Context) goka.Context) intended
// for goka.WithContextWrapper. It sets the inbound DSM checkpoint for the message
// and returns a context whose Emit/Loopback inject APM trace context and DSM
// pathway into outbound headers.
//
// The APM consume span is started by WrapCallback, not here, because
// WithContextWrapper has no hook for finishing a span once the callback returns.
//
// Note: goka also invokes the context wrapper for VisitValues visit callbacks,
// whose Topic() is the visit name rather than a Kafka topic. With DSM enabled,
// such visits produce an inbound checkpoint keyed on the visit name; ignore those
// nodes in the Data Streams Monitoring graph.
func (tr *Tracer) WrapContext(ctx goka.Context) goka.Context {
	tc := &tracedContext{gctx: ctx, tr: tr}
	if tr.cfg.dataStreamsEnabled {
		tc.dsmCtx = tr.setConsumeCheckpoint(ctx)
	}
	return tc
}

// WrapCallback wraps a goka.ProcessCallback so that each input message is
// processed inside a "kafka.consume" span. The span is finished when the callback
// returns; any panic that unwinds the callback (Context.Fail, an internal goka
// failure, or a bug in the handler) is recovered so the span is tagged with the
// error and then re-raised so goka still shuts the processor down.
//
// Wrap every input callback whose messages you want traced, and register
// WrapContext via goka.WithContextWrapper so emitted messages continue the trace.
//
// DSM commit-offset tracking is also performed here on successful processing (or,
// if the handler calls ctx.DeferCommit, when that deferred commit succeeds), so
// an Input whose callback is not wrapped reports no committed offset to Data
// Streams Monitoring. Wrap every input callback for which you want DSM lag.
//
// Because goka.Context.Emit is asynchronous, the span is finished when the
// callback returns and does not reflect the outcome of emits that resolve later;
// an emit that fails asynchronously (and shuts the partition down) is not tagged
// on the span.
func (tr *Tracer) WrapCallback(cb goka.ProcessCallback) goka.ProcessCallback {
	return func(ctx goka.Context, msg any) {
		tc, ok := ctx.(*tracedContext)
		if !ok {
			// WrapContext was not registered; process without a span rather than panic.
			cb(ctx, msg)
			return
		}
		finish := tc.startConsumeSpan()
		defer finish()
		cb(tc, msg)
	}
}

// EmitHeaders returns headers carrying APM trace context and a DSM outbound
// checkpoint for a message produced to topic. Use it with a standalone
// goka.Emitter (which has no processor context):
//
//	headers := tr.EmitHeaders(ctx, topic)
//	emitter.EmitSyncWithHeaders(key, value, headers)
//
// The span is taken from ctx if one is active there. When neither tracing nor DSM
// is enabled the returned headers are empty.
func (tr *Tracer) EmitHeaders(ctx context.Context, topic string) goka.Headers {
	if ctx == nil {
		ctx = context.Background()
	}
	var span *tracer.Span
	if s, ok := tracer.SpanFromContext(ctx); ok {
		span = s
	}
	return tr.outboundHeaders(span, ctx, topic, 0)
}

func (tr *Tracer) setConsumeCheckpoint(gctx goka.Context) context.Context {
	topic := string(gctx.Topic())
	edges := []string{"direction:in", "topic:" + topic, "type:kafka"}
	group := string(gctx.Group())
	if group != "" {
		edges = append(edges, "group:"+group)
	}
	// goka's Context exposes neither the raw message value nor its encoded size
	// at this hook (Value() returns the group-table state, not the input body),
	// so the payload size counts only the key and header bytes we can observe.
	params := options.CheckpointParams{
		PayloadSize: int64(len(gctx.Key())) + headersSize(gctx.Headers()),
	}
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(
		datastreams.ExtractFromBase64Carrier(gctx.Context(), gokaHeadersCarrier(gctx.Headers())),
		params,
		edges...,
	)
	if !ok {
		return nil
	}
	return ctx
}

// trackCommit records the consumed offset for DSM lag tracking. It runs only
// after the callback returns without failing, mirroring goka, which commits the
// offset after successful processing; a message that fails is reprocessed and
// must not be counted as committed.
func (tc *tracedContext) trackCommit() {
	if !tc.tr.cfg.dataStreamsEnabled {
		return
	}
	group := string(tc.gctx.Group())
	if group == "" {
		return
	}
	tracer.TrackKafkaCommitOffset(group, string(tc.gctx.Topic()), tc.gctx.Partition(), tc.gctx.Offset())
}

// injectProduceCheckpoint sets a DSM outbound checkpoint on base and injects the
// resulting pathway into headers. base must be non-nil and should carry the
// inbound pathway so the produce checkpoint chains onto it; callers own that
// fallback (produce uses the consume context, EmitHeaders the supplied context).
func (tr *Tracer) injectProduceCheckpoint(base context.Context, topic string, headers goka.Headers, payloadSize int64) {
	if !tr.cfg.dataStreamsEnabled {
		return
	}
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(
		base,
		options.CheckpointParams{PayloadSize: payloadSize},
		"direction:out", "topic:"+topic, "type:kafka",
	)
	if !ok {
		return
	}
	datastreams.InjectToBase64Carrier(ctx, gokaHeadersCarrier(headers))
}

// headersSize returns the total byte size of the key and value pairs in h.
func headersSize(h goka.Headers) int64 {
	var n int64
	for k, v := range h {
		n += int64(len(k) + len(v))
	}
	return n
}

// valueSize returns the byte size of an emitted value when it is a raw []byte or
// string. goka encodes other values with a codec we cannot see here, so their
// size is reported as 0.
func valueSize(v any) int64 {
	switch t := v.(type) {
	case []byte:
		return int64(len(t))
	case string:
		return int64(len(t))
	default:
		return 0
	}
}

// tracedContext wraps a goka.Context, overriding Emit/Loopback/Context/Fail to
// carry Datadog trace and DSM propagation. All other methods delegate to gctx.
type tracedContext struct {
	gctx goka.Context
	tr   *Tracer

	dsmCtx         context.Context // inbound DSM pathway; base for outbound checkpoints
	span           *tracer.Span    // APM consume span, set by startConsumeSpan
	tracedCtx      context.Context // span context returned by Context()
	spanErr        error           // recorded by Fail for the deferred span finish
	commitDeferred bool            // set by DeferCommit; offset tracking moves to its callback
}

func (tc *tracedContext) startConsumeSpan() func() {
	cfg := tc.tr.cfg
	gctx := tc.gctx
	topic := string(gctx.Topic())
	opts := []tracer.StartSpanOption{
		instrumentation.ServiceNameWithSource(cfg.consumerServiceName, cfg.serviceSource),
		tracer.ResourceName("Consume Topic " + topic),
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.Tag(ext.MessagingKafkaPartition, gctx.Partition()),
		tracer.Tag("offset", gctx.Offset()),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.MessagingDestinationName, topic),
		tracer.Measured(),
	}
	if group := gctx.Group(); group != "" {
		opts = append(opts, tracer.Tag("kafka.group", string(group)))
	}
	carrier := gokaHeadersCarrier(gctx.Headers())
	if parent, err := tracer.Extract(carrier); err == nil && parent != nil {
		if parent.SpanLinks() != nil {
			opts = append(opts, tracer.WithSpanLinks(parent.SpanLinks()))
		}
		opts = append(opts, tracer.ChildOf(parent))
	}

	span, spanCtx := tracer.StartSpanFromContext(gctx.Context(), cfg.consumerSpanName, opts...)
	tc.span = span
	tc.tracedCtx = spanCtx

	return func() {
		// A failure unwinds the callback by panicking: goka.Context.Fail (via our
		// override, which sets spanErr) or an internal goka failure / plain panic
		// that never reaches spanErr. Recover so the span records the error either
		// way, then re-panic so goka still shuts the processor down.
		if r := recover(); r != nil {
			err := tc.spanErr
			if err == nil {
				if e, ok := r.(error); ok {
					err = e
				} else {
					err = fmt.Errorf("goka: message processing panicked: %v", r)
				}
			}
			span.Finish(tracer.WithError(err))
			panic(r)
		}
		if tc.spanErr != nil {
			span.Finish(tracer.WithError(tc.spanErr))
			return
		}
		// When the handler deferred the commit, offset tracking is handled by the
		// DeferCommit callback instead, so it isn't double-counted here.
		if !tc.commitDeferred {
			tc.trackCommit()
		}
		span.Finish()
	}
}

// outboundHeaders builds the headers to attach to a message emitted to topic:
// span (the active consume span) is injected for APM trace propagation so the
// downstream consumer continues the trace, and a DSM outbound checkpoint is
// chained onto base. It is the single header builder shared by processor emits
// and the standalone EmitHeaders so the two cannot drift apart.
//
// No "kafka.produce" span is created: goka.Context.Emit is asynchronous and
// returns no handle, so a produce span could never be finished on the emit's
// actual completion or tagged with an async delivery error. Trace continuity is
// preserved by propagating the consume span through the headers instead.
func (tr *Tracer) outboundHeaders(span *tracer.Span, base context.Context, topic string, payloadSize int64) goka.Headers {
	headers := goka.Headers{}
	if span != nil {
		tracer.Inject(span.Context(), gokaHeadersCarrier(headers))
	}
	tr.injectProduceCheckpoint(base, topic, headers, payloadSize)
	return headers
}

// produce builds the outbound headers for an emit to topic (propagating the
// consume span and a DSM outbound checkpoint) and prepends them as a
// ContextOption before delegating. It centralises header injection and DSM
// checkpointing so Emit and Loopback share one path. Caller-supplied
// ContextOptions win on header-key collisions (goka merges per-emit headers over
// the ones we prepend).
func (tc *tracedContext) produce(topic, key string, value any, opts []goka.ContextOption, emit func(opts []goka.ContextOption)) {
	base := tc.dsmCtx
	if base == nil {
		base = tc.gctx.Context()
	}
	size := int64(len(key)) + valueSize(value)
	if headers := tc.tr.outboundHeaders(tc.span, base, topic, size); len(headers) > 0 {
		opts = append([]goka.ContextOption{goka.WithCtxEmitHeaders(headers)}, opts...)
	}
	emit(opts)
}

// Emit injects trace/DSM headers, then delegates to the wrapped context.
func (tc *tracedContext) Emit(topic goka.Stream, key string, value any, opts ...goka.ContextOption) {
	tc.produce(string(topic), key, value, opts, func(opts []goka.ContextOption) {
		tc.gctx.Emit(topic, key, value, opts...)
	})
}

// Loopback injects trace/DSM headers for the group's loop stream, then delegates.
//
// The loop topic is derived from cfg.loopSuffix (default "-loop"); if the
// application changed goka's suffix via goka.SetLoopSuffix, pass the matching
// WithLoopSuffix or the DSM loop edge and span tags will name the wrong topic.
func (tc *tracedContext) Loopback(key string, value any, opts ...goka.ContextOption) {
	tc.produce(tc.loopTopic(), key, value, opts, func(opts []goka.ContextOption) {
		tc.gctx.Loopback(key, value, opts...)
	})
}

// loopTopic returns the name of the group's loop stream, used to tag the
// Loopback DSM checkpoint. It mirrors goka's own "<group><suffix>" naming.
func (tc *tracedContext) loopTopic() string {
	return string(tc.gctx.Group()) + tc.tr.cfg.loopSuffix
}

// Fail records the error for the deferred span finish, then delegates (which
// panics to unwind the callback).
func (tc *tracedContext) Fail(err error) {
	tc.spanErr = err
	tc.gctx.Fail(err)
}

// Context returns the span context so downstream work becomes a child of the
// consume span, falling back to the wrapped context's when no span is active.
func (tc *tracedContext) Context() context.Context {
	if tc.tracedCtx != nil {
		return tc.tracedCtx
	}
	return tc.gctx.Context()
}

// DeferCommit wraps goka's DeferCommit so DSM commit-offset tracking follows the
// real (deferred) commit instead of the callback return: the offset is recorded
// only when the returned function is called with a nil error. The message
// coordinates are captured now, as the context must not be used once the callback
// has returned.
func (tc *tracedContext) DeferCommit() func(error) {
	commit := tc.gctx.DeferCommit()
	tc.commitDeferred = true

	track := tc.tr.cfg.dataStreamsEnabled
	group := string(tc.gctx.Group())
	topic := string(tc.gctx.Topic())
	partition := tc.gctx.Partition()
	offset := tc.gctx.Offset()

	return func(err error) {
		if err == nil && track && group != "" {
			tracer.TrackKafkaCommitOffset(group, topic, partition, offset)
		}
		commit(err)
	}
}

// The remaining methods delegate unchanged to the wrapped goka.Context.

func (tc *tracedContext) Topic() goka.Stream                      { return tc.gctx.Topic() }
func (tc *tracedContext) Key() string                             { return tc.gctx.Key() }
func (tc *tracedContext) Partition() int32                        { return tc.gctx.Partition() }
func (tc *tracedContext) Offset() int64                           { return tc.gctx.Offset() }
func (tc *tracedContext) Group() goka.Group                       { return tc.gctx.Group() }
func (tc *tracedContext) Value() any                              { return tc.gctx.Value() }
func (tc *tracedContext) Headers() goka.Headers                   { return tc.gctx.Headers() }
func (tc *tracedContext) Timestamp() time.Time                    { return tc.gctx.Timestamp() }
func (tc *tracedContext) Join(topic goka.Table) any               { return tc.gctx.Join(topic) }
func (tc *tracedContext) Lookup(topic goka.Table, key string) any { return tc.gctx.Lookup(topic, key) }
func (tc *tracedContext) SetValue(value any, opts ...goka.ContextOption) {
	tc.gctx.SetValue(value, opts...)
}
func (tc *tracedContext) Delete(opts ...goka.ContextOption) { tc.gctx.Delete(opts...) }

var _ goka.Context = (*tracedContext)(nil)
