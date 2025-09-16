// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"maps"
	"sync"
	"time"
)

type composedContext struct {
	parent  context.Context
	trace   TraceContext
	baggage BaggageContext
}

var _ PropagationContext = (*composedContext)(nil)

func NewPropagationContext(parent context.Context, trace TraceContext, baggage BaggageContext) *composedContext {
	return &composedContext{
		parent:  parent,
		trace:   trace,
		baggage: baggage,
	}
}

func NewBaggageOnlyContext(parent context.Context, baggage BaggageContext) *composedContext {
	return &composedContext{
		parent:  parent,
		trace:   nil,
		baggage: baggage,
	}
}

func NewTraceOnlyContext(parent context.Context, trace TraceContext) *composedContext {
	return &composedContext{
		parent:  parent,
		trace:   trace,
		baggage: nil,
	}
}

func (c *composedContext) Deadline() (deadline time.Time, ok bool) {
	if c.parent != nil {
		return c.parent.Deadline()
	}
	return time.Time{}, false
}

func (c *composedContext) Done() <-chan struct{} {
	if c.parent != nil {
		return c.parent.Done()
	}
	return nil
}

func (c *composedContext) Err() error {
	if c.parent != nil {
		return c.parent.Err()
	}
	return nil
}

func (c *composedContext) Value(key interface{}) interface{} {
	if c.parent != nil {
		return c.parent.Value(key)
	}
	return nil
}

func (c *composedContext) Trace() TraceContext {
	return c.trace
}

func (c *composedContext) Baggage() BaggageContext {
	return c.baggage
}

func (c *composedContext) HasTrace() bool {
	return c.trace != nil && c.trace.IsValid()
}

func (c *composedContext) HasBaggage() bool {
	return c.baggage != nil && c.baggage.HasBaggage()
}

func (c *composedContext) WithTrace(trace TraceContext) PropagationContext {
	return &composedContext{
		parent:  c.parent,
		trace:   trace,
		baggage: c.baggage,
	}
}

func (c *composedContext) WithBaggage(baggage BaggageContext) PropagationContext {
	return &composedContext{
		parent:  c.parent,
		trace:   c.trace,
		baggage: baggage,
	}
}

func (c *composedContext) WithParent(parent context.Context) PropagationContext {
	return &composedContext{
		parent:  parent,
		trace:   c.trace,
		baggage: c.baggage,
	}
}

type baggageContext struct {
	parent     context.Context
	w3cBaggage map[string]string
	otBaggage  map[string]string
	mu         sync.RWMutex
}

var _ BaggageContext = (*baggageContext)(nil)

func NewBaggageContext(parent context.Context) *baggageContext {
	return &baggageContext{
		parent:     parent,
		w3cBaggage: make(map[string]string),
		otBaggage:  make(map[string]string),
	}
}

func NewBaggageContextWithItems(parent context.Context, w3cBaggage, otBaggage map[string]string) *baggageContext {
	ctx := &baggageContext{
		parent: parent,
	}
	if w3cBaggage != nil {
		ctx.w3cBaggage = maps.Clone(w3cBaggage)
	} else {
		ctx.w3cBaggage = make(map[string]string)
	}
	if otBaggage != nil {
		ctx.otBaggage = maps.Clone(otBaggage)
	} else {
		ctx.otBaggage = make(map[string]string)
	}
	return ctx
}

func (b *baggageContext) Deadline() (deadline time.Time, ok bool) {
	if b.parent != nil {
		return b.parent.Deadline()
	}
	return time.Time{}, false
}

func (b *baggageContext) Done() <-chan struct{} {
	if b.parent != nil {
		return b.parent.Done()
	}
	return nil
}

func (b *baggageContext) Err() error {
	if b.parent != nil {
		return b.parent.Err()
	}
	return nil
}

func (b *baggageContext) Value(key interface{}) interface{} {
	if b.parent != nil {
		return b.parent.Value(key)
	}
	return nil
}

func (b *baggageContext) GetBaggage(key string) (string, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	value, ok := b.w3cBaggage[key]
	return value, ok
}

func (b *baggageContext) SetBaggage(key, value string) BaggageContext {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.w3cBaggage == nil {
		b.w3cBaggage = make(map[string]string)
	}
	newBaggage := maps.Clone(b.w3cBaggage)
	newBaggage[key] = value
	return &baggageContext{
		parent:     b.parent,
		w3cBaggage: newBaggage,
		otBaggage:  maps.Clone(b.otBaggage),
	}
}

func (b *baggageContext) AllBaggage() map[string]string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.w3cBaggage == nil {
		return nil
	}
	return maps.Clone(b.w3cBaggage)
}

func (b *baggageContext) ForeachBaggage(handler func(key, value string) bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for k, v := range b.w3cBaggage {
		if !handler(k, v) {
			break
		}
	}
}

func (b *baggageContext) ClearBaggage() BaggageContext {
	return &baggageContext{
		parent:     b.parent,
		w3cBaggage: make(map[string]string),
		otBaggage:  maps.Clone(b.otBaggage),
	}
}

func (b *baggageContext) GetOTBaggage(key string) string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.otBaggage[key]
}

func (b *baggageContext) SetOTBaggage(key, value string) BaggageContext {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.otBaggage == nil {
		b.otBaggage = make(map[string]string)
	}
	newOTBaggage := maps.Clone(b.otBaggage)
	newOTBaggage[key] = value
	return &baggageContext{
		parent:     b.parent,
		w3cBaggage: maps.Clone(b.w3cBaggage),
		otBaggage:  newOTBaggage,
	}
}

func (b *baggageContext) ForeachOTBaggage(handler func(key, value string) bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for k, v := range b.otBaggage {
		if !handler(k, v) {
			break
		}
	}
}

func (b *baggageContext) HasBaggage() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.w3cBaggage) > 0 || len(b.otBaggage) > 0
}

func (b *baggageContext) WithParent(parent context.Context) BaggageContext {
	return &baggageContext{
		parent:     parent,
		w3cBaggage: maps.Clone(b.w3cBaggage),
		otBaggage:  maps.Clone(b.otBaggage),
	}
}

type traceContext struct {
	traceID          traceID
	samplingPriority *float64
	origin           string
}

var _ TraceContext = (*traceContext)(nil)

func NewTraceContext(traceID [16]byte, samplingPriority *float64, origin string) *traceContext {
	return &traceContext{
		traceID:          traceID,
		samplingPriority: samplingPriority,
		origin:           origin,
	}
}

func (t *traceContext) TraceID() string {
	return t.traceID.HexEncoded()
}

func (t *traceContext) TraceIDBytes() [16]byte {
	return t.traceID
}

func (t *traceContext) TraceIDLower() uint64 {
	return t.traceID.Lower()
}

func (t *traceContext) TraceIDUpper() uint64 {
	return t.traceID.Upper()
}

func (t *traceContext) SamplingPriority() (int, bool) {
	if t.samplingPriority == nil {
		return 0, false
	}
	return int(*t.samplingPriority), true
}

func (t *traceContext) Origin() string {
	return t.origin
}

func (t *traceContext) IsValid() bool {
	return !t.traceID.Empty()
}

// mergeBaggage combines two BaggageContext instances, with the second taking precedence.
func mergeBaggage(first, second BaggageContext) *baggageContext {
	if first == nil {
		if second == nil {
			return nil
		}
		if secondConcrete, ok := second.(*baggageContext); ok {
			return secondConcrete
		}
		return createBaggageFromInterface(second)
	}
	if second == nil {
		if firstConcrete, ok := first.(*baggageContext); ok {
			return firstConcrete
		}
		return createBaggageFromInterface(first)
	}

	firstW3C := first.AllBaggage()
	secondW3C := second.AllBaggage()

	mergedW3C := make(map[string]string)
	if firstW3C != nil {
		maps.Copy(mergedW3C, firstW3C)
	}
	if secondW3C != nil {
		maps.Copy(mergedW3C, secondW3C)
	}

	mergedOT := make(map[string]string)
	first.ForeachOTBaggage(func(k, v string) bool {
		mergedOT[k] = v
		return true
	})
	second.ForeachOTBaggage(func(k, v string) bool {
		mergedOT[k] = v
		return true
	})

	parent := context.Background()
	if secondCtx, ok := second.(context.Context); ok {
		parent = secondCtx
	} else if firstCtx, ok := first.(context.Context); ok {
		parent = firstCtx
	}

	return NewBaggageContextWithItems(parent, mergedW3C, mergedOT)
}

func createBaggageFromInterface(baggage BaggageContext) *baggageContext {
	if baggage == nil {
		return nil
	}

	w3cBaggage := baggage.AllBaggage()

	otBaggage := make(map[string]string)
	baggage.ForeachOTBaggage(func(k, v string) bool {
		otBaggage[k] = v
		return true
	})

	parent := context.Background()
	if ctx, ok := baggage.(context.Context); ok {
		parent = ctx
	}

	return NewBaggageContextWithItems(parent, w3cBaggage, otBaggage)
}

// propagatorAdapter wraps an old Propagator to implement PropagationContextPropagator
type propagatorAdapter struct {
	wrapped Propagator
}

var _ PropagationContextPropagator = (*propagatorAdapter)(nil)

func NewPropagatorAdapter(p Propagator) *propagatorAdapter {
	return &propagatorAdapter{wrapped: p}
}

func (a *propagatorAdapter) Extract(carrier interface{}) (PropagationContext, error) {
	spanCtx, err := a.wrapped.Extract(carrier)
	if err != nil {
		return nil, err
	}
	if spanCtx == nil {
		return nil, nil
	}

	return SpanContextToPropagationContext(spanCtx), nil
}

func (a *propagatorAdapter) Inject(ctx PropagationContext, carrier interface{}) error {
	if ctx == nil {
		return nil
	}

	spanCtx := PropagationContextToSpanContext(ctx)
	return a.wrapped.Inject(spanCtx, carrier)
}

// SpanContextToPropagationContext converts a SpanContext to PropagationContext
func SpanContextToPropagationContext(sc *SpanContext) PropagationContext {
	if sc == nil {
		return nil
	}

	var trace TraceContext
	var baggage BaggageContext

	// Extract trace context if valid
	if !sc.traceID.Empty() && sc.spanID != 0 {
		var priority *float64
		if p, ok := sc.SamplingPriority(); ok {
			pf := float64(p)
			priority = &pf
		}
		trace = NewTraceContext(sc.traceID, priority, sc.origin)
	}

	// Extract baggage context if present
	if sc.baggage != nil && sc.baggage.HasBaggage() {
		baggage = sc.baggage
	}

	return NewPropagationContext(context.Background(), trace, baggage)
}

// PropagationContextToSpanContext converts a PropagationContext to SpanContext for backward compatibility
func PropagationContextToSpanContext(pc PropagationContext) *SpanContext {
	if pc == nil {
		return nil
	}

	sc := &SpanContext{}

	// Set trace context if present
	if pc.HasTrace() {
		trace := pc.Trace()
		sc.traceID = trace.TraceIDBytes()
		if p, ok := trace.SamplingPriority(); ok {
			sc.trace = newTrace()
			pf := float64(p)
			sc.trace.priority = &pf
		}
		sc.origin = trace.Origin()
	}

	// Set baggage context if present
	if pc.HasBaggage() {
		sc.baggage = pc.Baggage()
	}

	return sc
}

// chainedPropagationContextPropagator implements PropagationContextPropagator with clean baggage/trace separation.
type chainedPropagationContextPropagator struct {
	propagators []PropagationContextPropagator
}

var _ PropagationContextPropagator = (*chainedPropagationContextPropagator)(nil)

func NewChainedPropagationContextPropagator(propagators ...PropagationContextPropagator) *chainedPropagationContextPropagator {
	return &chainedPropagationContextPropagator{
		propagators: propagators,
	}
}

func (p *chainedPropagationContextPropagator) Extract(carrier interface{}) (PropagationContext, error) {
	var resultTrace TraceContext
	var resultBaggage BaggageContext
	var lastErr error

	for _, propagator := range p.propagators {
		ctx, err := propagator.Extract(carrier)
		if err != nil {
			lastErr = err
			continue
		}
		if ctx == nil {
			continue
		}

		// Take the first valid trace context we find
		if resultTrace == nil && ctx.HasTrace() {
			resultTrace = ctx.Trace()
		}

		// Merge all baggage we find
		if ctx.HasBaggage() {
			if resultBaggage == nil {
				resultBaggage = ctx.Baggage()
			} else {
				resultBaggage = mergeBaggage(resultBaggage, ctx.Baggage())
			}
		}
	}

	// If we found nothing, return the last error or not found
	if resultTrace == nil && resultBaggage == nil {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, ErrSpanContextNotFound
	}

	// Return clean composition - no special cases needed
	return NewPropagationContext(context.Background(), resultTrace, resultBaggage), nil
}

func (p *chainedPropagationContextPropagator) Inject(ctx PropagationContext, carrier interface{}) error {
	if ctx == nil {
		return nil
	}

	var lastErr error
	for _, propagator := range p.propagators {
		if err := propagator.Inject(ctx, carrier); err != nil {
			lastErr = err
		}
	}
	return lastErr
}
