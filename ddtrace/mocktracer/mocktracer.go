// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

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

	"github.com/DataDog/dd-trace-go/v2/ddtrace"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/datastreams"
)

var _ tracer.Tracer = (*mocktracer)(nil)
var _ Tracer = (*mocktracer)(nil)

// Tracer exposes an interface for querying the currently running mock tracer.
type Tracer interface {
	// OpenSpans returns the set of started spans that have not been finished yet.
	OpenSpans() []*tracer.Span

	// FinishedSpans returns the set of finished spans.
	FinishedSpans() []*tracer.Span

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
	t := newMockTracer()
	tracer.SetGlobalTracer(t)
	tracer.Testing = true
	return t
}

type mocktracer struct {
	sync.RWMutex  // guards below spans
	finishedSpans []*tracer.Span
	openSpans     map[uint64]*tracer.Span
}

func newMockTracer() *mocktracer {
	var t mocktracer
	t.openSpans = make(map[uint64]*tracer.Span)
	return &t
}

// Stop deactivates the mock tracer and sets the active tracer to a no-op.
func (*mocktracer) Stop() {
	tracer.SetGlobalTracer(&tracer.NoopTracer{})
	tracer.Testing = false
}

func (t *mocktracer) StartSpan(operationName string, opts ...ddtrace.StartSpanOption) *tracer.Span {
	var cfg ddtrace.StartSpanConfig
	for _, fn := range opts {
		fn(&cfg)
	}
	span := newSpan(t, operationName, &cfg)

	t.Lock()
	t.openSpans[span.Context().SpanID()] = span
	t.Unlock()

	return span
}

func (t *mocktracer) GetDataStreamsProcessor() *datastreams.Processor {
	return &datastreams.Processor{}
}

func (t *mocktracer) OpenSpans() []*tracer.Span {
	t.RLock()
	defer t.RUnlock()
	spans := make([]*tracer.Span, 0, len(t.openSpans))
	for _, s := range t.openSpans {
		spans = append(spans, s)
	}
	return spans
}

func (t *mocktracer) FinishedSpans() []*tracer.Span {
	t.RLock()
	defer t.RUnlock()
	return t.finishedSpans
}

func (t *mocktracer) Reset() {
	t.Lock()
	defer t.Unlock()
	for k := range t.openSpans {
		delete(t.openSpans, k)
	}
	t.finishedSpans = nil
}

func (t *mocktracer) addFinishedSpan(s *tracer.Span) {
	t.Lock()
	defer t.Unlock()
	delete(t.openSpans, s.Context().SpanID())
	if t.finishedSpans == nil {
		t.finishedSpans = make([]*tracer.Span, 0, 1)
	}
	t.finishedSpans = append(t.finishedSpans, s)
}

const (
	traceHeader    = tracer.DefaultTraceIDHeader
	spanHeader     = tracer.DefaultParentIDHeader
	priorityHeader = tracer.DefaultPriorityHeader
	baggagePrefix  = tracer.DefaultBaggageHeaderPrefix
)

func (t *mocktracer) Extract(carrier interface{}) (tracer.SpanContext, error) {
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

func (t *mocktracer) Inject(context tracer.SpanContext, carrier interface{}) error {
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

func (t *mocktracer) TracerConf() tracer.TracerConf {
	return tracer.TracerConf{}
}

func (t *mocktracer) SubmitStats(*tracer.Span)               {}
func (t *mocktracer) SubmitAbandonedSpan(*tracer.Span, bool) {}
func (t *mocktracer) SubmitChunk(any)                        {}
func (t *mocktracer) Flush()                                 {}
