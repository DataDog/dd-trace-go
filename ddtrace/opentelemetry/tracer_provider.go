// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"sync"
	"sync/atomic"
	"time"

	oteltrace "go.opentelemetry.io/otel/trace"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var _ oteltrace.TracerProvider = (*TracerProvider)(nil)

// TracerProvider provides implementation of OpenTelemetry TracerProvider interface.
// TracerProvider provides Tracers that are used by instrumentation code to
// trace computational workflows.
// WithInstrumentationVersion and WithSchemaURL TracerOptions are not supported.
type TracerProvider struct {
	tracer  *oteltracer
	ddopts  []tracer.StartOption
	stopped atomic.Bool
	sync.Once
}

func NewTracerProvider(opts ...tracer.StartOption) *TracerProvider {
	return &TracerProvider{ddopts: opts}
}

// Tracer returns an instance of OpenTelemetry Tracer and initializes Datadog Tracer.
func (p *TracerProvider) Tracer(name string, options ...oteltrace.TracerOption) oteltrace.Tracer {
	if p.stopped.Load() {
		return &noopOteltracer{}
	}
	tracer.Start(p.ddopts...)
	return &oteltracer{
		Tracer:   internal.GetGlobalTracer(),
		cfg:      oteltrace.NewTracerConfig(options...),
		provider: p,
	}
}

// Shutdown stops the started tracer. Subsequent calls are valid but become no-op.
// Triggering Shutdown is async.
func (p *TracerProvider) Shutdown() error {
	p.Once.Do(func() {
		tracer.Stop()
		p.stopped.Store(true)
	})
	return nil
}

// ForceFlush flushes any buffered traces. Flush is in effect only if a tracer
// is started.
func (p *TracerProvider) ForceFlush(timeout time.Duration, callback func(ok bool)) {
	if p.stopped.Load() {
		log.Warn("tracer stopped")
		return
	}
	done := make(chan struct{})
	go func() {
		tracer.Flush()
		done <- struct{}{}
	}()
	for {
		select {
		case <-time.After(timeout):
			callback(false)
			return
		case <-done:
			callback(true)
			return
		}
	}
}
