// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"sync/atomic"
	"testing"

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

func (*preservingTestTracer) SetServiceInfo(_, _, _ string) {}

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

	installGlobalTracerWithCIVisibilityRouter(real, true)

	if got := getGlobalTracer(); got != current {
		t.Fatalf("global tracer = %T, want preserved tracer", got)
	}
	if current.setCalls != 1 {
		t.Fatalf("SetCIVisibilityTracer calls = %d, want 1", current.setCalls)
	}
	received, ok := current.received.(*ciVisibilityTracerRouter)
	if !ok || received.CIVisibilityTracer() != real {
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

	installGlobalTracerWithCIVisibilityRouter(application, false)

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
	installGlobalTracerWithCIVisibilityRouter(wrapWithCIVisibilityTracerRouter(application), true)

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

	installGlobalTracerWithCIVisibilityRouter(real, false)

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

	installGlobalTracerWithCIVisibilityRouter(real, true)

	got, ok := getGlobalTracer().(*ciVisibilityTracerRouter)
	if !ok || got.CIVisibilityTracer() != real {
		t.Fatalf("global tracer = %T, want CI Visibility router around %T", getGlobalTracer(), real)
	}
	if current.setCalls != 1 {
		t.Fatalf("SetCIVisibilityTracer calls = %d, want 1", current.setCalls)
	}
	received, ok := current.received.(*ciVisibilityTracerRouter)
	if !ok || received.CIVisibilityTracer() != real {
		t.Fatalf("received tracer = %T, want CI Visibility router around %T", current.received, real)
	}
	if current.stopCnt.Load() != 1 {
		t.Fatalf("previous tracer stop count = %d, want 1", current.stopCnt.Load())
	}
	if real.stopCnt.Load() != 0 {
		t.Fatalf("real tracer was stopped %d times", real.stopCnt.Load())
	}
}
