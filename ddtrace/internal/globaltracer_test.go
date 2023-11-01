// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal // import "github.com/DataDog/dd-trace-go/v2/ddtrace/internal"

import (
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace"
)

type raceTestTracer struct {
	stopped bool
}

func (*raceTestTracer) StartSpan(_ string, _ ...ddtrace.StartSpanOption) ddtrace.Span {
	return NoopSpan{}
}
func (*raceTestTracer) SetServiceInfo(_, _, _ string) {}
func (*raceTestTracer) Extract(_ interface{}) (ddtrace.SpanContext, error) {
	return NoopSpanContext{}, nil
}
func (*raceTestTracer) Inject(_ ddtrace.SpanContext, _ interface{}) error { return nil }
func (r *raceTestTracer) Stop() {
	r.stopped = true
}

func (t *raceTestTracer) TracerConf() ddtrace.TracerConf {
	return ddtrace.TracerConf{}
}

func (t *raceTestTracer) SubmitStats(ddtrace.Span)               {}
func (t *raceTestTracer) SubmitAbandonedSpan(ddtrace.Span, bool) {}
func (t *raceTestTracer) SubmitChunk(any)                        {}
func (t *raceTestTracer) Flush()                                 {}
func (t *raceTestTracer) Signal(ddtrace.Event)                   {}

func TestGlobalTracer(t *testing.T) {
	// at module initialization, the tracer must be seet
	if GetGlobalTracer() == nil {
		t.Fatal("GetGlobalTracer() must never return nil")
	}
	SetGlobalTracer(&raceTestTracer{})
	SetGlobalTracer(&NoopTracer{})

	// ensure the test resets the global tracer back to nothing
	defer SetGlobalTracer(&raceTestTracer{})

	const numGoroutines = 10

	tracers := make([]*raceTestTracer, numGoroutines)
	for i := range tracers {
		tracers[i] = &raceTestTracer{}
	}

	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(index int) {
			defer wg.Done()
			var tracer ddtrace.Tracer = tracers[index]
			SetGlobalTracer(tracer)

			// get the global tracer: it must be any raceTestTracer
			tracer = GetGlobalTracer()
			if _, ok := tracer.(*raceTestTracer); !ok {
				t.Errorf("GetGlobalTracer() expected to return a *rateTestTracer was %T", tracer)
			}
		}(i)
	}
	wg.Wait()

	SetGlobalTracer(&raceTestTracer{})

	// all tracers must be stopped
	for i, tracer := range tracers {
		if !tracer.stopped {
			t.Errorf("tracer %d is not stopped", i)
		}
	}
}

func BenchmarkGetGlobalTracerSerial(b *testing.B) {
	for i := 0; i < b.N; i++ {
		tracer := GetGlobalTracer()
		if tracer == nil {
			b.Fatal("BUG: tracer must not be nil")
		}
	}
}

func BenchmarkGetGlobalTracerParallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tracer := GetGlobalTracer()
			if tracer == nil {
				b.Fatal("BUG: tracer must not be nil")
			}
		}
	})
}
