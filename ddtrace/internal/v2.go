// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal // import "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type TracerV2Adapter struct {
	Tracer v2.Tracer
}

// Extract implements ddtrace.Tracer.
func (ta TracerV2Adapter) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	ctx, err := ta.Tracer.Extract(carrier)
	if err != nil {
		return nil, err
	}
	return SpanContextV2Adapter{Ctx: ctx}, nil
}

var (
	// ErrInvalidSpanContext is returned when the span context found in the
	// carrier is not of the expected type.
	ErrInvalidSpanContext = v2.ErrInvalidSpanContext
)

// Inject implements ddtrace.Tracer.
func (ta TracerV2Adapter) Inject(context ddtrace.SpanContext, carrier interface{}) error {
	sca, ok := context.(SpanContextV2Adapter)
	if !ok {
		return ErrInvalidSpanContext
	}
	return ta.Tracer.Inject(sca.Ctx, carrier)
}

// StartSpan implements ddtrace.Tracer.
func (ta TracerV2Adapter) StartSpan(operationName string, opts ...ddtrace.StartSpanOption) ddtrace.Span {
	if _, ok := ta.Tracer.(*v2.NoopTracer); ok {
		log.Debug("Tracer must be started before starting a span; Review the docs for more information: https://docs.datadoghq.com/tracing/trace_collection/library_config/go/")
	}
	s := ta.Tracer.StartSpan(operationName, ApplyV1Options(opts...))
	return WrapSpan(s)
}

var (
	zeroTime            = time.Time{}
	startSpanConfigPool = sync.Pool{
		New: func() interface{} {
			return new(ddtrace.StartSpanConfig)
		},
	}
	finishConfigPool = sync.Pool{
		New: func() interface{} {
			return new(ddtrace.FinishConfig)
		},
	}
)

// ApplyV1Options consumes a list of v1 StartSpanOptions and returns a function
// that can be used to set the corresponding v2 StartSpanConfig fields.
// This is used to adapt the v1 StartSpanOptions to the v2 StartSpanConfig.
func ApplyV1Options(opts ...ddtrace.StartSpanOption) v2.StartSpanOption {
	return func(cfg *v2.StartSpanConfig) {
		ssc := startSpanConfigPool.Get().(*ddtrace.StartSpanConfig)
		ssc.Tags = make(map[string]interface{})
		defer releaseStartSpanConfig(ssc)
		for _, o := range opts {
			if o == nil {
				continue
			}
			o(ssc)
		}
		if ssc.Parent != nil {
			cfg.Parent = resolveSpantContextV2(ssc.Parent)
		}
		if ssc.Context != nil {
			cfg.Context = ssc.Context
		}
		if ssc.SpanID != 0 {
			cfg.SpanID = ssc.SpanID
		}
		if len(ssc.SpanLinks) > 0 {
			cfg.SpanLinks = ssc.SpanLinks
		}
		if !ssc.StartTime.IsZero() {
			cfg.StartTime = ssc.StartTime
		}
		if len(ssc.Tags) == 0 {
			return
		}
		if cfg.Tags == nil {
			cfg.Tags = ssc.Tags
		} else {
			for k, v := range ssc.Tags {
				cfg.Tags[k] = v
			}
		}
	}
}

func resolveSpantContextV2(ctx ddtrace.SpanContext) *v2.SpanContext {
	if parent, ok := ctx.(SpanContextV2Adapter); ok {
		return parent.Ctx
	}

	// We may have an otelToDDSpanContext that can be converted to a v2.SpanContext
	// by copying its fields.
	// Other SpanContext may fall through here, but they are not guaranteed to be
	// fully supported, as the resulting v2.SpanContext may be missing data.
	return v2.FromGenericCtx(&SpanContextV1Adapter{Ctx: ctx})
}

func releaseStartSpanConfig(ssc *ddtrace.StartSpanConfig) {
	ssc.Parent = nil
	ssc.Context = nil
	ssc.SpanID = 0
	ssc.SpanLinks = nil
	ssc.StartTime = zeroTime
	ssc.Tags = nil
	startSpanConfigPool.Put(ssc)
}

// Stop implements ddtrace.Tracer.
func (ta TracerV2Adapter) Stop() {
	ta.Tracer.Stop()
}

var _ ddtrace.Span = (*SpanV2Adapter)(nil)
var _ ddtrace.SpanWithEvents = (*SpanV2Adapter)(nil)

type SpanV2Adapter struct {
	Span *v2.Span
}

func WrapSpan(span *v2.Span) SpanV2Adapter {
	return SpanV2Adapter{Span: span}
}

// BaggageItem implements ddtrace.Span.
func (sa SpanV2Adapter) BaggageItem(key string) string {
	return sa.Span.BaggageItem(key)
}

// Context implements ddtrace.Span.
func (sa SpanV2Adapter) Context() ddtrace.SpanContext {
	ctx := sa.Span.Context()
	return SpanContextV2Adapter{Ctx: ctx}
}

// Finish implements ddtrace.Span.
func (sa SpanV2Adapter) Finish(opts ...ddtrace.FinishOption) {
	sa.Span.Finish(ApplyV1FinishOptions(opts...))
}

func ApplyV1FinishOptions(opts ...ddtrace.FinishOption) v2.FinishOption {
	return func(cfg *v2.FinishConfig) {
		fc := finishConfigPool.Get().(*ddtrace.FinishConfig)
		defer releaseFinishConfig(fc)
		for _, o := range opts {
			if o == nil {
				continue
			}
			o(fc)
		}
		if fc.Error != nil {
			cfg.Error = fc.Error
		}
		if !fc.FinishTime.IsZero() {
			cfg.FinishTime = fc.FinishTime
		}
		if fc.NoDebugStack {
			cfg.NoDebugStack = fc.NoDebugStack
		}
		if fc.SkipStackFrames != 0 {
			cfg.SkipStackFrames = fc.SkipStackFrames
		}
		if fc.StackFrames != 0 {
			cfg.StackFrames = fc.StackFrames
		}
	}
}

func releaseFinishConfig(fc *ddtrace.FinishConfig) {
	fc.Error = nil
	fc.FinishTime = zeroTime
	fc.NoDebugStack = false
	fc.SkipStackFrames = 0
	fc.StackFrames = 0
	finishConfigPool.Put(fc)
}

// SetBaggageItem implements ddtrace.Span.
func (sa SpanV2Adapter) SetBaggageItem(key string, val string) {
	sa.Span.SetBaggageItem(key, val)
}

// SetOperationName implements ddtrace.Span.
func (sa SpanV2Adapter) SetOperationName(operationName string) {
	sa.Span.SetOperationName(operationName)
}

// SetTag implements ddtrace.Span.
func (sa SpanV2Adapter) SetTag(key string, value interface{}) {
	if key == ext.SamplingPriority {
		key = "_sampling_priority_v1shim"
	}
	sa.Span.SetTag(key, value)
}

// Root implements appsec.rooter.
func (sa SpanV2Adapter) Root() ddtrace.Span {
	if sa.Span == nil {
		return nil
	}
	r := sa.Span.Root()
	if r == nil {
		return nil
	}
	return WrapSpan(r)
}

// Format implements fmt.Formatter.
func (sa SpanV2Adapter) Format(f fmt.State, c rune) {
	sa.Span.Format(f, c)
}

func (sa SpanV2Adapter) AddEvent(name string, opts ...ddtrace.SpanEventOption) {
	sa.Span.AddEvent(name, ApplyV1SpanEventOptions(opts...))
}

func ApplyV1SpanEventOptions(opts ...ddtrace.SpanEventOption) v2.SpanEventOption {
	return func(cfg *v2.SpanEventConfig) {
		ec := &ddtrace.SpanEventConfig{}
		for _, o := range opts {
			o(ec)
		}
		cfg.Time = ec.Time
		cfg.Attributes = ec.Attributes
	}
}

type SpanContextV2Adapter struct {
	Ctx *v2.SpanContext
}

// ForeachBaggageItem implements ddtrace.SpanContext.
func (sca SpanContextV2Adapter) ForeachBaggageItem(handler func(k string, v string) bool) {
	sca.Ctx.ForeachBaggageItem(handler)
}

// SpanID implements ddtrace.SpanContext.
func (sca SpanContextV2Adapter) SpanID() uint64 {
	return sca.Ctx.SpanID()
}

// TraceID implements ddtrace.SpanContext.
func (sca SpanContextV2Adapter) TraceID() uint64 {
	return sca.Ctx.TraceIDLower()
}

// TraceID implements ddtrace.SpanContextW3C.
func (sca SpanContextV2Adapter) TraceID128() string {
	return sca.Ctx.TraceID()
}

// TraceID128Bytes implements ddtrace.SpanContextW3C.
func (sca SpanContextV2Adapter) TraceID128Bytes() [16]byte {
	return sca.Ctx.TraceIDBytes()
}

// Partial copy of traceID from ddtrace/tracer/spancontext.go
type traceID [16]byte // traceID in big endian, i.e. <upper><lower>

var emptyTraceID traceID

func (t *traceID) HexEncoded() string {
	return hex.EncodeToString(t[:])
}

func (t *traceID) SetLower(i uint64) {
	binary.BigEndian.PutUint64(t[8:], i)
}

func (t *traceID) Empty() bool {
	return *t == emptyTraceID
}

type SpanContextV1Adapter struct {
	Ctx     ddtrace.SpanContext
	traceID traceID
}

// ForeachBaggageItem implements ddtrace.SpanContext.
func (sca *SpanContextV1Adapter) ForeachBaggageItem(handler func(k string, v string) bool) {
	sca.Ctx.ForeachBaggageItem(handler)
}

// SpanID implements ddtrace.SpanContext.
func (sca *SpanContextV1Adapter) SpanID() uint64 {
	return sca.Ctx.SpanID()
}

// TraceID implements ddtrace.SpanContext.
func (sca *SpanContextV1Adapter) TraceID() string {
	if sca.traceID.Empty() {
		_ = sca.TraceIDBytes()
	}
	return sca.traceID.HexEncoded()
}

// TraceIDBytes implements ddtrace.SpanContext.
func (sca *SpanContextV1Adapter) TraceIDBytes() [16]byte {
	if !sca.traceID.Empty() {
		return sca.traceID
	}
	if sc128, ok := sca.Ctx.(ddtrace.SpanContextW3C); ok {
		tID := sc128.TraceID128Bytes()
		copy(sca.traceID[:], tID[:])
		return sca.traceID
	}
	tID := sca.Ctx.TraceID()
	sca.traceID.SetLower(tID)
	return sca.traceID
}

// TraceIDLower implements ddtrace.SpanContext.
func (sca *SpanContextV1Adapter) TraceIDLower() uint64 {
	return sca.Ctx.TraceID()
}
