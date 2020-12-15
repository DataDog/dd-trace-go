// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

// Package mocktracer provides a mock implementation of the tracer used in testing. It
// allows querying spans generated at runtime, without having them actually be sent to
// an agent. It provides a simple way to test that instrumentation is running correctly
// in your application.
//
// Simply call "Start" at the beginning of your tests to start and obtain an instance
// of the mock tracer.
package mocktracer

import (
	"strconv"
	"strings"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var _ ddtrace.Tracer = (*mocktracer)(nil)
var _ Tracer = (*mocktracer)(nil)

// Tracer exposes an interface for querying the currently running mock tracer.
type Tracer interface {
	// OpenSpans returns the set of started spans that have not been finished yet.
	OpenSpans() []Span

	// FinishedSpans returns the set of finished spans.
	FinishedSpans() []Span

	// Reset resets the spans and services recorded in the tracer. This is
	// especially useful when running tests in a loop, where a clean start
	// is desired for FinishedSpans calls.
	Reset()

	// Stop deactivates the mock tracer and allows a normal tracer to take over.
	// It should always be called when testing has finished.
	Stop()
}

// Start sets the internal tracer to a mock and returns an interface
// which allows querying it. Call Start at the beginning of your tests
// to activate the mock tracer. When your test runs, use the returned
// interface to query the tracer's state.
func Start() Tracer {
	var t mocktracer
	t.Reset()
	internal.SetGlobalTracer(&t)
	internal.Testing = true
	return &t
}

type mocktracer struct {
	sync.RWMutex  // guards below spans
	finishedSpans []Span
	openSpans     map[uint64]Span
}

// Stop deactivates the mock tracer and sets the active tracer to a no-op.
func (*mocktracer) Stop() {
	internal.SetGlobalTracer(&internal.NoopTracer{})
	internal.Testing = false
}

func (t *mocktracer) StartSpan(operationName string, opts ...ddtrace.StartSpanOption) ddtrace.Span {
	var cfg ddtrace.StartSpanConfig
	for _, fn := range opts {
		fn(&cfg)
	}
	span := newSpan(t, operationName, &cfg)
	t.addOpenSpan(span)
	return span
}

func (t *mocktracer) OpenSpans() []Span {
	t.RLock()
	defer t.RUnlock()
	spans := make([]Span, 0, len(t.openSpans))
	for _, s := range t.openSpans {
		spans = append(spans, s)
	}
	return spans
}

func (t *mocktracer) FinishedSpans() []Span {
	t.RLock()
	defer t.RUnlock()
	return t.finishedSpans
}

func (t *mocktracer) Reset() {
	t.Lock()
	defer t.Unlock()
	t.openSpans = make(map[uint64]Span)
	t.finishedSpans = nil
}

func (t *mocktracer) addOpenSpan(s Span) {
	t.Lock()
	defer t.Unlock()
	t.openSpans[s.SpanID()] = s
}

func (t *mocktracer) addFinishedSpan(s Span) {
	t.Lock()
	defer t.Unlock()
	delete(t.openSpans, s.SpanID())
	if t.finishedSpans == nil {
		t.finishedSpans = make([]Span, 0, 1)
	}
	t.finishedSpans = append(t.finishedSpans, s)
}

const (
	traceHeader    = tracer.DefaultTraceIDHeader
	spanHeader     = tracer.DefaultParentIDHeader
	priorityHeader = tracer.DefaultPriorityHeader
	baggagePrefix  = tracer.DefaultBaggageHeaderPrefix
)

func (t *mocktracer) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	reader, ok := carrier.(tracer.TextMapReader)
	if !ok {
		return nil, tracer.ErrInvalidCarrier
	}
	var sc spanContext
	err := reader.ForeachKey(func(key, v string) error {
		k := strings.ToLower(key)
		if k == traceHeader {
			id, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return tracer.ErrSpanContextCorrupted
			}
			sc.traceID = id
		}
		if k == spanHeader {
			id, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return tracer.ErrSpanContextCorrupted
			}
			sc.spanID = id
		}
		if k == priorityHeader {
			p, err := strconv.Atoi(v)
			if err != nil {
				return tracer.ErrSpanContextCorrupted
			}
			sc.priority = p
			sc.hasPriority = true
		}
		if strings.HasPrefix(k, baggagePrefix) {
			sc.setBaggageItem(strings.TrimPrefix(k, baggagePrefix), v)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if sc.traceID == 0 || sc.spanID == 0 {
		return nil, tracer.ErrSpanContextNotFound
	}
	return &sc, err
}

func (t *mocktracer) Inject(context ddtrace.SpanContext, carrier interface{}) error {
	writer, ok := carrier.(tracer.TextMapWriter)
	if !ok {
		return tracer.ErrInvalidCarrier
	}
	ctx, ok := context.(*spanContext)
	if !ok || ctx.traceID == 0 || ctx.spanID == 0 {
		return tracer.ErrInvalidSpanContext
	}
	writer.Set(traceHeader, strconv.FormatUint(ctx.traceID, 10))
	writer.Set(spanHeader, strconv.FormatUint(ctx.spanID, 10))
	if ctx.hasSamplingPriority() {
		writer.Set(priorityHeader, strconv.Itoa(ctx.priority))
	}
	ctx.ForeachBaggageItem(func(k, v string) bool {
		writer.Set(baggagePrefix+k, v)
		return true
	})
	return nil
}
