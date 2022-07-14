// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package tracer

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var _ ReadWriteSpan = (*readWriteSpan)(nil)

// readWriteSpan wraps span and implements the ReadWriteSpan interface.
type readWriteSpan struct {
	*span
}

// readableSpan specifies methods to read from a span.
type readableSpan interface {
	// GetName returns the operation of the span.
	GetName() string
	// etc...
}

// GetName returns the operation name of s.
func (s readWriteSpan) GetName() string {
	s.Lock()
	defer s.Unlock()
	return s.Name
}

// ReadWriteSpan implements the methods of ddtrace.Span (to write to a span)
// and readableSpan (to read from a span).
// Note: I will likely move the ReadWriteSpan interface to the ddtrace package.
type ReadWriteSpan interface {
	ddtrace.Span
	readableSpan
	Drop()
}

// SetTag adds a set of key/value metadata to the span.
func (s readWriteSpan) SetTag(key string, value interface{}) {
	s.Lock()
	defer s.Unlock()
	s.setTagLocked(key, value)
}

// SetOperationName changes the operation name.
func (s readWriteSpan) SetOperationName(operationName string) {
	s.Lock()
	defer s.Unlock()
	s.Name = operationName
}

// Note: the code to implement this method is not in this PR. This is a WIP.
func (s readWriteSpan) Drop() {
	s.Lock()
	defer s.Unlock()
	// currently does nothing.
	s.drop = true
}

// runProcessor pushes finished spans from a trace to the processor. It then
// computes stats (if client side stats are enabled), and reports whether the
// trace should be dropped.
func (tr *tracer) runProcessor(spans []*span) bool {
	shouldKeep := tr.config.postProcessor(newReadWriteSpanSlice(spans))
	tracerCanComputeStats := tr.config.canComputeStats()
	for _, span := range spans {
		span.Lock()
		if tracerCanComputeStats && shouldComputeStats(span) {
			// the agent supports computed stats
			select {
			case tr.stats.In <- newAggregableSpan(span, tr.obfuscator):
				// ok
			default:
				log.Error("Stats channel full, disregarding span.")
			}
		}
		span.Unlock()
	}
	return shouldKeep
}

// newReadWriteSpanSlice copies the elements of slice spans to the
// destination slice of type ReadWriteSpan to be fed to the processor.
func newReadWriteSpanSlice(spans []*span) []ReadWriteSpan {
	rwSlice := make([]ReadWriteSpan, len(spans))
	for i, span := range spans {
		rwSlice[i] = readWriteSpan{span}
	}
	return rwSlice
}
