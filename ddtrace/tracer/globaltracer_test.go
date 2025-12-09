// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"sync"
	"testing"
)

type raceTestTracer struct {
	stopped bool
}

func (*raceTestTracer) StartSpan(_ string, _ ...StartSpanOption) *Span {
	return nil
}
func (*raceTestTracer) SetServiceInfo(_, _, _ string) {}
func (*raceTestTracer) Extract(_ interface{}) (*SpanContext, error) {
	return nil, nil
}
func (*raceTestTracer) Inject(_ *SpanContext, _ interface{}) error { return nil }
func (r *raceTestTracer) Stop() {
	r.stopped = true
}

func (*raceTestTracer) TracerConf() TracerConf {
	return TracerConf{}
}

func (*raceTestTracer) Flush() {}

func TestGlobalTracer(t *testing.T) {
	// at module initialization, the tracer must be seet
	if getGlobalTracer() == nil {
		t.Fatal("getGlobalTracer() must never return nil")
	}
	setGlobalTracer(&raceTestTracer{})
	setGlobalTracer(&NoopTracer{})

	// ensure the test resets the global tracer back to nothing
	defer setGlobalTracer(&raceTestTracer{})

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
			var tracer Tracer = tracers[index]
			setGlobalTracer(tracer)

			// get the global tracer: it must be any raceTestTracer
			tracer = getGlobalTracer()
			if _, ok := tracer.(*raceTestTracer); !ok {
				t.Errorf("getGlobalTracer() expected to return a *rateTestTracer was %T", tracer)
			}
		}(i)
	}
	wg.Wait()

	setGlobalTracer(&raceTestTracer{})

	// all tracers must be stopped
	for i, tracer := range tracers {
		if !tracer.stopped {
			t.Errorf("tracer %d is not stopped", i)
		}
	}
}

func BenchmarkGetGlobalTracerSerial(b *testing.B) {
	for i := 0; i < b.N; i++ {
		tracer := getGlobalTracer()
		if tracer == nil {
			b.Fatal("BUG: tracer must not be nil")
		}
	}
}

func BenchmarkGetGlobalTracerParallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tracer := getGlobalTracer()
			if tracer == nil {
				b.Fatal("BUG: tracer must not be nil")
			}
		}
	})
}
