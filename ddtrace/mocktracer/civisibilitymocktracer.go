// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mocktracer

import (
	"sync"
	"sync/atomic"
	_ "unsafe" // Needed for go:linkname.

	"github.com/DataDog/dd-trace-go/v2/ddtrace/internal"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
	"github.com/DataDog/dd-trace-go/v2/internal/datastreams"
)

type ciVisibilityRouter interface {
	tracer.Tracer
	Reset()
	SetMockTracer(tracer.Tracer) bool
	ClearMockTracer(tracer.Tracer) bool
	SetApplicationTracer(tracer.Tracer) bool
	TracerForTrace(string, string) tracer.Tracer
}

//go:linkname attachMockTracerToCIVisibility github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.attachMockTracerToCIVisibility
func attachMockTracerToCIVisibility(mockTracer tracer.Tracer) tracer.Tracer

// civisibilitymocktracer is the user-facing handle returned by Start while CI
// Visibility is active. Routing stays in the CI Visibility router; this handle
// only exposes mock assertions and controls the temporary mock override.
type civisibilitymocktracer struct {
	mock *mocktracer

	routerMu sync.RWMutex
	router   ciVisibilityRouter

	isnoop atomic.Bool
}

var (
	_ tracer.Tracer = (*civisibilitymocktracer)(nil)
	_ Tracer        = (*civisibilitymocktracer)(nil)
)

// startCIVisibilityMockTracer keeps routing in the CI router when available.
// Before CI starts, the handle stays global until the router adopts it.
func startCIVisibilityMockTracer() *civisibilitymocktracer {
	t := newCIVisibilityMockTracer()
	if t.currentRouter() == nil {
		internal.StoreGlobalTracer[Tracer, tracer.Tracer](t)
	}
	return t
}

func newCIVisibilityMockTracer() *civisibilitymocktracer {
	t := &civisibilitymocktracer{mock: newMockTracer()}
	current := getGlobalTracer()
	if handle, ok := current.(*civisibilitymocktracer); ok {
		current = handle.currentRouter()
	}
	if router, ok := current.(ciVisibilityRouter); ok && router.SetMockTracer(t.mock) {
		t.router = router
	} else if router, ok := attachMockTracerToCIVisibility(t.mock).(ciVisibilityRouter); ok {
		t.router = router
	}
	return t
}

func (t *civisibilitymocktracer) currentRouter() ciVisibilityRouter {
	t.routerMu.RLock()
	defer t.routerMu.RUnlock()
	return t.router
}

// SetCIVisibilityTracer adopts a newly started CI router when mocktracer was
// installed first. Once adopted, the router replaces the temporary global
// handle without stopping it; the handle remains valid for assertions and
// mock lifecycle control.
func (t *civisibilitymocktracer) SetCIVisibilityTracer(candidate tracer.Tracer) bool {
	router, ok := candidate.(ciVisibilityRouter)
	if !ok || !router.SetMockTracer(t.mock) {
		return false
	}
	t.routerMu.Lock()
	if t.isnoop.Load() {
		t.routerMu.Unlock()
		router.ClearMockTracer(t.mock)
		return false
	}
	old := t.router
	t.router = router
	t.routerMu.Unlock()
	if getGlobalTracer() == t {
		internal.StoreGlobalTracer[ciVisibilityRouter, tracer.Tracer](router)
	}
	if old != nil && old != router {
		old.ClearMockTracer(t.mock)
		old.Stop()
	}
	return true
}

func (t *civisibilitymocktracer) SetApplicationTracer(application tracer.Tracer) bool {
	if router := t.currentRouter(); router != nil {
		return router.SetApplicationTracer(application)
	}
	return false
}

func (t *civisibilitymocktracer) Stop() {
	if !t.isnoop.CompareAndSwap(false, true) {
		return
	}
	router := t.currentRouter()
	t.mock.dsmProcessor.Stop()

	state := civisibility.GetState()
	ciVisibilityActive := state == civisibility.StateInitializing || state == civisibility.StateInitialized
	current := getGlobalTracer()
	if router != nil {
		router.ClearMockTracer(t.mock)
	}
	if ciVisibilityActive && router != nil {
		if current == t {
			// SetGlobalTracer invokes Stop on the old handle. The CAS above
			// makes that recursive call a no-op.
			internal.SetGlobalTracer(tracer.Tracer(router))
		} else if current != tracer.Tracer(router) {
			router.Stop()
		}
		return
	}

	if current == t || current == tracer.Tracer(router) {
		internal.SetGlobalTracer(tracer.Tracer(&tracer.NoopTracer{}))
	}
	if router != nil && tracer.Tracer(router) != current {
		router.Stop()
	}
}

// StartSpan delegates through the router so direct calls on the returned mock
// handle follow exactly the same routing as package-level tracer.StartSpan
// calls.
func (t *civisibilitymocktracer) StartSpan(operationName string, opts ...tracer.StartSpanOption) *tracer.Span {
	if router := t.currentRouter(); router != nil {
		return router.StartSpan(operationName, opts...)
	}
	if t.isnoop.Load() {
		return nil
	}
	return t.mock.StartSpan(operationName, opts...)
}

func (t *civisibilitymocktracer) FinishSpan(span *tracer.Span) {
	if span == nil {
		return
	}
	t.mock.FinishSpan(span)
}

func (t *civisibilitymocktracer) TracerForTrace(tracerType, fallbackSpanType string) tracer.Tracer {
	if router := t.currentRouter(); router != nil {
		return router.TracerForTrace(tracerType, fallbackSpanType)
	}
	return t.mock
}

func (t *civisibilitymocktracer) GetDataStreamsProcessor() *datastreams.Processor {
	if t.isnoop.Load() {
		return nil
	}
	return t.mock.dsmProcessor
}

func (t *civisibilitymocktracer) SentDSMBacklogs() []datastreams.Backlog {
	if t.isnoop.Load() {
		return nil
	}
	t.mock.dsmProcessor.Flush()
	return t.mock.dsmTransport.backlogs
}

func (t *civisibilitymocktracer) OpenSpans() []*Span {
	return t.mock.OpenSpans()
}

func (t *civisibilitymocktracer) FinishedSpans() []*Span {
	return t.mock.FinishedSpans()
}

func (t *civisibilitymocktracer) Reset() {
	t.mock.Reset()
}

func (t *civisibilitymocktracer) Extract(carrier any) (*tracer.SpanContext, error) {
	if t.isnoop.Load() {
		return nil, nil
	}
	if router := t.currentRouter(); router != nil {
		return router.Extract(carrier)
	}
	return t.mock.Extract(carrier)
}

func (t *civisibilitymocktracer) Inject(context *tracer.SpanContext, carrier any) error {
	if t.isnoop.Load() {
		return nil
	}
	if router := t.currentRouter(); router != nil {
		return router.Inject(context, carrier)
	}
	return t.mock.Inject(context, carrier)
}

func (t *civisibilitymocktracer) TracerConf() tracer.TracerConf {
	if router := t.currentRouter(); router != nil {
		return router.TracerConf()
	}
	return t.mock.TracerConf()
}

func (t *civisibilitymocktracer) Flush() {
	if router := t.currentRouter(); router != nil {
		router.Flush()
		return
	}
	t.mock.Flush()
}
