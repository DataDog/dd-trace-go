// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package opentelemetry

import (
	oteltrace "go.opentelemetry.io/otel/trace"
	"sync"
	"sync/atomic"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var _ oteltrace.TracerProvider = (*TracerProvider)(nil)

type TracerProvider struct {
	tracer *oteltracer
	sync.Once
	stopped atomic.Bool
}

const defaultName = "otel_datadog"

// todo find a way to map Datadog options to Otel Options
// - for tracer Start
// - for span Start
// - for span Finish
func (p *TracerProvider) Tracer(name string, options ...oteltrace.TracerOption) oteltrace.Tracer {
	if p.stopped.Load() {
		return &noopOteltracer{}
	}
	// name is to no avail, emit a warning
	if len(name) == 0 {
		log.Warn("provided tracer name is invalid: `%s`, using default value: %s", name, defaultName)
	}
	var opts []oteltrace.TracerOption
	for _, option := range options {
		if option != nil {
			opts = append(opts, option)
		}
	}
	cfg := oteltrace.NewTracerConfig(opts...)
	tracer.Start(locOpts...)
	return &oteltracer{
		name:     name,
		cfg:      cfg,
		provider: p, // verify that
		Tracer:   internal.GetGlobalTracer(),
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
// is started. Triggering Flush is async.
func (p *TracerProvider) ForceFlush() { tracer.Flush() }
