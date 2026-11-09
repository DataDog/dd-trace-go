// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mocktracer

import (
	"sync"
	"sync/atomic"
	_ "unsafe" // Needed for go:linkname.

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/internal"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/datastreams"
)

type ciVisibilityRouter interface {
	tracer.Tracer
	Reset()
	SetMockTracer(tracer.Tracer) bool
	ClearMockTracer(tracer.Tracer) bool
	SetApplicationTracer(tracer.Tracer) bool
	SwapCIVisibilityTracer(tracer.Tracer) (tracer.Tracer, bool)
	TracerForTrace(string, string) tracer.Tracer
}

//go:linkname attachMockTracerToCIVisibility github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.attachMockTracerToCIVisibility
func attachMockTracerToCIVisibility(mockTracer, current tracer.Tracer) tracer.Tracer

//go:linkname newCIVisibilityApplicationRouter github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.newCIVisibilityApplicationRouter
func newCIVisibilityApplicationRouter(application tracer.Tracer) tracer.Tracer

// civisibilitymocktracer is the user-facing handle returned by Start while CI
// Visibility is active. Routing stays in the CI Visibility router; this handle
// only exposes mock assertions and controls the temporary mock override.
type civisibilitymocktracer struct {
	mock *mocktracer

	routerMu sync.RWMutex
	// +checklocks:routerMu
	router ciVisibilityRouter

	isnoop atomic.Bool
}

var (
	_ tracer.Tracer = (*civisibilitymocktracer)(nil)
	_ Tracer        = (*civisibilitymocktracer)(nil)
)

// Serializes mock publication, adoption, and restoration, without holding the lock
// while stopping delegates or routing spans.
var ciVisibilityMockTracerMu sync.Mutex

// startCIVisibilityMockTracer keeps the user-facing mock handle global while
// the mock is active. The handle delegates routing to the CI router and also
// preserves the mocktracer.Tracer contract used by the v1 compatibility layer.
func startCIVisibilityMockTracer() *civisibilitymocktracer {
	ciVisibilityMockTracerMu.Lock()
	t := &civisibilitymocktracer{mock: newMockTracer()}
	var displaced tracer.Tracer
	for {
		current := internal.SnapshotGlobalTracer[tracer.Tracer]()
		t.bindRouter(current.Tracer())
		if current.Replace(t) {
			displaced = current.Tracer()
			break
		}
	}
	// Invalidate the old handle before another bootstrap adoption can observe
	// it. Adoption and publication share this lock, including router-less handles.
	if handle, ok := displaced.(*civisibilitymocktracer); ok {
		handle.isnoop.Store(true)
	}
	ciVisibilityMockTracerMu.Unlock()
	// Publication is complete. A previous handle owns its processor, while a
	// router-less replacement also owns closing the displaced concrete tracer.
	if handle, ok := displaced.(*civisibilitymocktracer); ok {
		handle.mock.dsmProcessor.Stop()
		if oldRouter := handle.currentRouter(); oldRouter != nil && oldRouter != t.currentRouter() {
			oldRouter.Stop()
		}
	} else if t.currentRouter() == nil {
		if mock, ok := displaced.(*mocktracer); ok {
			mock.dsmProcessor.Stop()
		} else {
			displaced.Stop()
		}
	}
	return t
}

func newCIVisibilityMockTracer() *civisibilitymocktracer {
	t := &civisibilitymocktracer{mock: newMockTracer()}
	t.bindRouter(getGlobalTracer())
	return t
}

func (t *civisibilitymocktracer) bindRouter(current tracer.Tracer) {
	if handle, ok := current.(*civisibilitymocktracer); ok {
		current = handle.currentRouter()
	}
	var attached ciVisibilityRouter
	if router, ok := current.(ciVisibilityRouter); ok && router.SetMockTracer(t.mock) {
		attached = router
	} else if router, ok := attachMockTracerToCIVisibility(t.mock, current).(ciVisibilityRouter); ok {
		attached = router
	}
	t.routerMu.Lock()
	t.router = attached
	t.routerMu.Unlock()
}

func (t *civisibilitymocktracer) currentRouter() ciVisibilityRouter {
	t.routerMu.RLock()
	defer t.routerMu.RUnlock()
	return t.router
}

// SetCIVisibilityTracer adopts a newly started CI router when mocktracer was
// installed first. The handle remains global until the mock stops so legacy
// mock spans can finish through the public mocktracer.Tracer contract.
func (t *civisibilitymocktracer) SetCIVisibilityTracer(candidate tracer.Tracer) bool {
	router, ok := candidate.(ciVisibilityRouter)
	if !ok || !router.SetMockTracer(t.mock) {
		return false
	}
	ciVisibilityMockTracerMu.Lock()
	t.routerMu.Lock()
	if t.isnoop.Load() {
		t.routerMu.Unlock()
		ciVisibilityMockTracerMu.Unlock()
		router.ClearMockTracer(t.mock)
		return false
	}
	old := t.router
	if old != nil {
		previous, accepted := old.SwapCIVisibilityTracer(candidate)
		if accepted {
			t.routerMu.Unlock()
			ciVisibilityMockTracerMu.Unlock()
			if router != old {
				router.ClearMockTracer(t.mock)
			}
			if previous != nil {
				previous.Stop()
			}
			return true
		}
	}
	t.router = router
	t.routerMu.Unlock()
	ciVisibilityMockTracerMu.Unlock()
	if old != nil && old != router {
		old.ClearMockTracer(t.mock)
		old.Stop()
	}
	return true
}

func (t *civisibilitymocktracer) SetApplicationTracer(application tracer.Tracer) bool {
	ciVisibilityMockTracerMu.Lock()
	t.routerMu.Lock()
	router := t.router
	if router == nil && application != nil && !t.isnoop.Load() && civisibility.GetState() == civisibility.StateInitializing {
		router = newCIVisibilityApplicationRouter(application).(ciVisibilityRouter)
		router.SetMockTracer(t.mock)
		t.router = router
		t.routerMu.Unlock()
		ciVisibilityMockTracerMu.Unlock()
		return true
	}
	t.routerMu.Unlock()
	ciVisibilityMockTracerMu.Unlock()
	return router != nil && router.SetApplicationTracer(application)
}

func (t *civisibilitymocktracer) Stop() {
	if !t.isnoop.CompareAndSwap(false, true) {
		return
	}
	router := t.currentRouter()
	t.mock.dsmProcessor.Stop()

	state := civisibility.GetState()
	ciVisibilityActive := state == civisibility.StateInitializing || state == civisibility.StateInitialized
	if router != nil {
		router.ClearMockTracer(t.mock)
	}
	if ciVisibilityActive && router != nil {
		if !t.restoreCIVisibilityRouter(router) {
			router.Stop()
		}
		return
	}

	current := internal.SnapshotGlobalTracer[tracer.Tracer]()
	if current.Tracer() == t || current.Tracer() == tracer.Tracer(router) {
		current.Replace(&tracer.NoopTracer{})
	}
	if router != nil {
		router.Stop()
	}
}

// restoreCIVisibilityRouter reports whether the router remains reachable through
// the global tracer. A new mock must not be overwritten by an older mock's Stop.
func (t *civisibilitymocktracer) restoreCIVisibilityRouter(router ciVisibilityRouter) bool {
	ciVisibilityMockTracerMu.Lock()
	defer ciVisibilityMockTracerMu.Unlock()
	for {
		current := internal.SnapshotGlobalTracer[tracer.Tracer]()
		if current.Tracer() == t {
			if current.Replace(router) {
				return true
			}
			continue
		}
		if active, ok := current.Tracer().(*civisibilitymocktracer); ok && active.currentRouter() == router {
			return true
		}
		return current.Tracer() == tracer.Tracer(router)
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
	var cfg tracer.StartSpanConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	switch cfg.Tags[ext.SpanType] {
	case constants.SpanTypeTest, constants.SpanTypeTestSuite, constants.SpanTypeTestModule, constants.SpanTypeTestSession:
		// Without a CI router these events have no destination.
		return nil
	}
	return t.mock.StartSpan(operationName, func(c *tracer.StartSpanConfig) { *c = cfg })
}

func (t *civisibilitymocktracer) FinishSpan(span *tracer.Span) {
	if span == nil || t.isnoop.Load() {
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
