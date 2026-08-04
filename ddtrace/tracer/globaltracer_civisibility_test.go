// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"sync/atomic"
	"testing"
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

func TestSetGlobalTracerPreservingCIVisibilityMockTracerPreservesWhenAccepted(t *testing.T) {
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
	if current.received != real {
		t.Fatalf("received tracer = %T, want real tracer", current.received)
	}
	if current.stopCnt.Load() != 0 {
		t.Fatalf("preserved tracer was stopped %d times", current.stopCnt.Load())
	}
	if real.stopCnt.Load() != 0 {
		t.Fatalf("real tracer was stopped %d times", real.stopCnt.Load())
	}
}

func TestSetGlobalTracerPreservingCIVisibilityMockTracerFallsBackWhenCIVisibilityDisabled(t *testing.T) {
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

func TestSetGlobalTracerPreservingCIVisibilityMockTracerFallsBackWhenPreserverRejects(t *testing.T) {
	t.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
	})

	current := &preservingTestTracer{accept: false}
	real := &preservingTestTracer{}
	setGlobalTracer(current)

	setGlobalTracerPreservingCIVisibilityMockTracer(real, true)

	if got := getGlobalTracer(); got != real {
		t.Fatalf("global tracer = %T, want real tracer", got)
	}
	if current.setCalls != 1 {
		t.Fatalf("SetCIVisibilityTracer calls = %d, want 1", current.setCalls)
	}
	if current.received != real {
		t.Fatalf("received tracer = %T, want real tracer", current.received)
	}
	if current.stopCnt.Load() != 1 {
		t.Fatalf("previous tracer stop count = %d, want 1", current.stopCnt.Load())
	}
	if real.stopCnt.Load() != 0 {
		t.Fatalf("real tracer was stopped %d times", real.stopCnt.Load())
	}
}
