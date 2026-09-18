// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
)

type preservingTestTracer struct {
	accept   bool
	received Tracer
	setCalls int
	stopCnt  atomic.Int32
}

func (*preservingTestTracer) StartSpan(_ string, _ ...StartSpanOption) *Span {
	return nil
}

func (*preservingTestTracer) Extract(_ any) (*SpanContext, error) {
	return nil, nil
}

func (*preservingTestTracer) Inject(_ *SpanContext, _ any) error { return nil }

func (p *preservingTestTracer) Stop() {
	p.stopCnt.Add(1)
}

func (*preservingTestTracer) TracerConf() TracerConf {
	return TracerConf{}
}

func (*preservingTestTracer) Flush() {}

func (p *preservingTestTracer) SetCIVisibilityTracer(real Tracer) bool {
	p.setCalls++
	p.received = real
	return p.accept
}

type applicationPreservingTestTracer struct {
	*preservingTestTracer
	applicationAccept   bool
	applicationReceived Tracer
	applicationSetCalls int
}

func (p *applicationPreservingTestTracer) SetApplicationTracer(application Tracer) bool {
	p.applicationSetCalls++
	p.applicationReceived = application
	return p.applicationAccept
}

func TestInstallGlobalTracerWithCIVisibilityRouterPreservesWhenAccepted(t *testing.T) {
	t.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
	})

	current := &preservingTestTracer{accept: true}
	real := &preservingTestTracer{}
	setGlobalTracer(current)

	setGlobalTracerPreservingCIVisibilityMockTracer(real, true)

	if got := getGlobalTracer(); got != current {
		t.Fatalf("global tracer = %T, want preserved tracer", got)
	}
	if current.setCalls != 1 {
		t.Fatalf("SetCIVisibilityTracer calls = %d, want 1", current.setCalls)
	}
	received, ok := current.received.(*ciVisibilityTracerRouter)
	if !ok || received.ciVisibilityTracer() != real {
		t.Fatalf("received tracer = %T, want CI Visibility router around %T", current.received, real)
	}
	if current.stopCnt.Load() != 0 {
		t.Fatalf("preserved tracer was stopped %d times", current.stopCnt.Load())
	}
	if real.stopCnt.Load() != 0 {
		t.Fatalf("real tracer was stopped %d times", real.stopCnt.Load())
	}
}

func TestInstallGlobalTracerWithCIVisibilityRouterPreservesApplicationTracer(t *testing.T) {
	t.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
	})

	current := &applicationPreservingTestTracer{
		preservingTestTracer: &preservingTestTracer{},
		applicationAccept:    true,
	}
	application := &preservingTestTracer{}
	setGlobalTracer(current)

	setGlobalTracerPreservingCIVisibilityMockTracer(application, false)

	if got := getGlobalTracer(); got != current {
		t.Fatalf("global tracer = %T, want preserved tracer", got)
	}
	if current.applicationSetCalls != 1 {
		t.Fatalf("SetApplicationTracer calls = %d, want 1", current.applicationSetCalls)
	}
	if current.applicationReceived != application {
		t.Fatalf("received tracer = %T, want application tracer", current.applicationReceived)
	}
	if current.stopCnt.Load() != 0 {
		t.Fatalf("preserved tracer was stopped %d times", current.stopCnt.Load())
	}
	if application.stopCnt.Load() != 0 {
		t.Fatalf("application tracer was stopped %d times", application.stopCnt.Load())
	}
}

func TestInstallGlobalTracerWithCIVisibilityRouterTreatsStartDuringActiveCIVisibilityAsApplication(t *testing.T) {
	previousState := civisibility.GetState()
	civisibility.SetState(civisibility.StateInitialized)
	t.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
		civisibility.SetState(previousState)
	})

	current := &applicationPreservingTestTracer{
		preservingTestTracer: &preservingTestTracer{},
		applicationAccept:    true,
	}
	application := &preservingTestTracer{}
	setGlobalTracer(current)

	// The environment-backed tracer config still has CI Visibility enabled,
	// so Start has built the same temporary wrapper used during CI bootstrap.
	setGlobalTracerPreservingCIVisibilityMockTracer(wrapWithCIVisibilityTracerRouter(application), true)

	if got := getGlobalTracer(); got != current {
		t.Fatalf("global tracer = %T, want preserved CI Visibility owner", got)
	}
	if current.setCalls != 0 {
		t.Fatalf("SetCIVisibilityTracer calls = %d, want 0", current.setCalls)
	}
	if current.applicationSetCalls != 1 {
		t.Fatalf("SetApplicationTracer calls = %d, want 1", current.applicationSetCalls)
	}
	if current.applicationReceived != application {
		t.Fatalf("application tracer = %T, want unwrapped %T", current.applicationReceived, application)
	}
}

func TestInstallGlobalTracerWithCIVisibilityRouterFallsBackWhenCIVisibilityDisabled(t *testing.T) {
	t.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
	})

	current := &preservingTestTracer{accept: true}
	real := &preservingTestTracer{}
	setGlobalTracer(current)

	setGlobalTracerPreservingCIVisibilityMockTracer(real, false)

	if got := getGlobalTracer(); got != real {
		t.Fatalf("global tracer = %T, want real tracer", got)
	}
	if current.setCalls != 0 {
		t.Fatalf("SetCIVisibilityTracer calls = %d, want 0", current.setCalls)
	}
	if current.stopCnt.Load() != 1 {
		t.Fatalf("previous tracer stop count = %d, want 1", current.stopCnt.Load())
	}
	if real.stopCnt.Load() != 0 {
		t.Fatalf("real tracer was stopped %d times", real.stopCnt.Load())
	}
}

func TestInstallGlobalTracerWithCIVisibilityRouterFallsBackWhenPreserverRejects(t *testing.T) {
	t.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
	})

	current := &preservingTestTracer{accept: false}
	real := &preservingTestTracer{}
	setGlobalTracer(current)

	setGlobalTracerPreservingCIVisibilityMockTracer(real, true)

	if got := getGlobalTracer(); got != real {
		t.Fatalf("global tracer = %T, want concrete CI Visibility tracer %T", got, real)
	}
	if current.setCalls != 1 {
		t.Fatalf("SetCIVisibilityTracer calls = %d, want 1", current.setCalls)
	}
	received, ok := current.received.(*ciVisibilityTracerRouter)
	if !ok || received.ciVisibilityTracer() != real {
		t.Fatalf("received tracer = %T, want CI Visibility router around %T", current.received, real)
	}
	if current.stopCnt.Load() != 1 {
		t.Fatalf("previous tracer stop count = %d, want 1", current.stopCnt.Load())
	}
	if real.stopCnt.Load() != 0 {
		t.Fatalf("real tracer was stopped %d times", real.stopCnt.Load())
	}
}

func TestInstallGlobalTracerWithCIVisibilityRouterKeepsConcreteTracerWithoutCoexistence(t *testing.T) {
	previousState := civisibility.GetState()
	civisibility.SetState(civisibility.StateInitializing)
	t.Cleanup(func() {
		civisibility.SetState(civisibility.StateExiting)
		setGlobalTracer(&NoopTracer{})
		civisibility.SetState(previousState)
	})

	real := &preservingTestTracer{}
	setGlobalTracer(&NoopTracer{})
	setGlobalTracerPreservingCIVisibilityMockTracer(real, true)

	require.Same(t, real, getGlobalTracer())
}

func TestInstallGlobalTracerWithCIVisibilityRouterCreatesRouterForApplicationCoexistence(t *testing.T) {
	previousState := civisibility.GetState()
	ciTracer, _ := newUninstalledTestTracer(t)
	application := &preservingTestTracer{}
	civisibility.SetState(civisibility.StateInitialized)
	setGlobalTracer(ciTracer)
	t.Cleanup(func() {
		civisibility.SetState(civisibility.StateExiting)
		setGlobalTracer(&NoopTracer{})
		civisibility.SetState(previousState)
	})

	setGlobalTracerPreservingCIVisibilityMockTracer(application, false)

	router, ok := getGlobalTracer().(*ciVisibilityTracerRouter)
	require.True(t, ok)
	require.Same(t, ciTracer, router.ciVisibilityTracer())
	require.Same(t, application, router.currentApplicationTracer())
}

func TestInstallGlobalTracerWithCIVisibilityRouterDoesNotNestRouters(t *testing.T) {
	previousState := civisibility.GetState()
	civisibility.SetState(civisibility.StateInitializing)
	t.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
		civisibility.SetState(previousState)
	})

	current := newCIVisibilityTracerRouter(&preservingTestTracer{}, false)
	setGlobalTracer(current)
	setGlobalTracerPreservingCIVisibilityMockTracer(&preservingTestTracer{}, true)

	if _, nested := current.ciVisibilityTracer().(*ciVisibilityTracerRouter); nested {
		t.Fatal("repeated CI start installed a router as the CI delegate of another router")
	}
}
