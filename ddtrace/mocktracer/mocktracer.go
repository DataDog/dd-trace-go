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
	"net/http"
	"net/url"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/internal"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	utils "github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/datastreams"

	"github.com/DataDog/datadog-go/v5/statsd"
)

var _ tracer.Tracer = (*mocktracer)(nil)
var _ Tracer = (*mocktracer)(nil)

// DSMBacklog is an alias to datastreams.Backlog
type DSMBacklog = datastreams.Backlog

// Tracer exposes an interface for querying the currently running mock tracer.
type Tracer interface {
	tracer.Tracer

	// OpenSpans returns the set of started spans that have not been finished yet.
	OpenSpans() []*Span

	FinishSpan(*tracer.Span)
	// FinishedSpans returns the set of finished spans.
	FinishedSpans() []*Span

	SentDSMBacklogs() []DSMBacklog

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
	if utils.BoolEnv(constants.CIVisibilityEnabledEnvironmentVariable, false) && !civisibility.IsTestMode() {
		// If CI Visibility is enabled (and we are not in a CI Visibility testing mode), we need to use the CIVisibilityMockTracer
		// to bypass the CI Visibility spans from the mocktracer.
		// This supports the scenario where the mocktracer is used in a test (we need to keep reporting test spans)
		t := newCIVisibilityMockTracer()
		// Set the global tracer to the mock tracer without stopping the old one (inside the mock tracer)
		internal.StoreGlobalTracer[Tracer, tracer.Tracer](t)
		return t
	}

	var t tracer.Tracer = newMockTracer()
	internal.SetGlobalTracer(t)
	return t.(Tracer)
}

func getGlobalTracer() tracer.Tracer {
	return internal.GetGlobalTracer[tracer.Tracer]()
}

type mocktracer struct {
	sync.RWMutex  // guards below spans
	finishedSpans []*Span
	openSpans     map[uint64]*Span
	dsmTransport  *mockDSMTransport
	dsmProcessor  *datastreams.Processor
}

func (t *mocktracer) SentDSMBacklogs() []DSMBacklog {
	t.dsmProcessor.Flush()
	return t.dsmTransport.backlogs
}

func newMockTracer() *mocktracer {
	var t mocktracer
	t.openSpans = make(map[uint64]*Span)
	t.dsmTransport = &mockDSMTransport{}
	client := &http.Client{
		Transport: t.dsmTransport,
	}
	t.dsmProcessor = datastreams.NewProcessor(&statsd.NoOpClientDirect{}, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, client)
	t.dsmProcessor.Start()
	t.dsmProcessor.Flush()
	return &t
}

// This is called by the spans when they finish
func (t *mocktracer) FinishSpan(s *tracer.Span) {
	t.addFinishedSpan(s)
}

// Stop deactivates the mock tracer and sets the active tracer to a no-op.
func (t *mocktracer) Stop() {
	// N.b.: The main reason for this call is to make TestTracerStop pass.
	internal.SetGlobalTracer(tracer.Tracer(&tracer.NoopTracer{}))
	t.dsmProcessor.Stop()
}

func (t *mocktracer) StartSpan(operationName string, opts ...tracer.StartSpanOption) *tracer.Span {
	var cfg tracer.StartSpanConfig
	for _, fn := range opts {
		fn(&cfg)
	}
	span := newSpan(operationName, &cfg)

	t.Lock()
	t.openSpans[span.Context().SpanID()] = MockSpan(span)
	t.Unlock()

	return span
}

func (t *mocktracer) GetDataStreamsProcessor() *datastreams.Processor {
	return t.dsmProcessor
}

func UnwrapSlice(ss []*Span) []*tracer.Span {
	ret := make([]*tracer.Span, len(ss))
	for i, sp := range ss {
		ret[i] = sp.Unwrap()
	}
	return ret
}

func (t *mocktracer) OpenSpans() []*Span {
	t.RLock()
	defer t.RUnlock()
	spans := make([]*Span, 0, len(t.openSpans))
	for _, s := range t.openSpans {
		spans = append(spans, s)
	}
	return spans
}

func (t *mocktracer) FinishedSpans() []*Span {
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
	// If the span is not in the open spans, we may be finishing a span that was started
	// before the mock tracer was started. In this case, we don't want to add it to the
	// finished spans.
	if _, ok := t.openSpans[s.Context().SpanID()]; !ok {
		return
	}
	delete(t.openSpans, s.Context().SpanID())
	if t.finishedSpans == nil {
		t.finishedSpans = make([]*Span, 0, 1)
	}
	t.finishedSpans = append(t.finishedSpans, MockSpan(s))
}

const (
	traceHeader    = tracer.DefaultTraceIDHeader
	spanHeader     = tracer.DefaultParentIDHeader
	priorityHeader = tracer.DefaultPriorityHeader
	baggagePrefix  = tracer.DefaultBaggageHeaderPrefix
)

func (t *mocktracer) Extract(carrier interface{}) (*tracer.SpanContext, error) {
	return tracer.NewPropagator(&tracer.PropagatorConfig{
		MaxTagsHeaderLen: 512,
	}).Extract(carrier)
}

func (t *mocktracer) Inject(context *tracer.SpanContext, carrier interface{}) error {
	return tracer.NewPropagator(&tracer.PropagatorConfig{
		MaxTagsHeaderLen: 512,
	}).Inject(context, carrier)
}

func (t *mocktracer) TracerConf() tracer.TracerConf {
	return tracer.TracerConf{}
}

func (t *mocktracer) Flush() {
	t.dsmProcessor.Flush()
	for _, s := range t.OpenSpans() {
		t.addFinishedSpan(s.sp)
	}
}
