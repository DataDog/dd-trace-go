// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package tracer

import (
	"sync/atomic"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// ReadWriteSpan is a span which can be read from and modified by using the provided methods.
type ReadWriteSpan struct {
	span *span
}

// Tag returns the tag value held by the given key.
func (s *ReadWriteSpan) Tag(key string) interface{} {
	s.span.Lock()
	defer s.span.Unlock()

	switch key {
	// String.
	case ext.SpanName:
		return s.span.Name
	case ext.ServiceName:
		return s.span.Service
	case ext.ResourceName:
		return s.span.Resource
	case ext.SpanType:
		return s.span.Type
	// Bool.
	case ext.AnalyticsEvent:
		return s.span.Metrics[ext.EventSampleRate] == 1.0
	case ext.ManualDrop:
		return s.span.Metrics[keySamplingPriority] == -1
	case ext.ManualKeep:
		return s.span.Metrics[keySamplingPriority] == 2
	// Metrics.
	case ext.SamplingPriority, keySamplingPriority:
		if val, ok := s.span.Metrics[keySamplingPriority]; ok {
			return val
		}
		return nil
	}
	if val, ok := s.span.Meta[key]; ok {
		return val
	}
	if val, ok := s.span.Metrics[key]; ok {
		return val
	}
	return nil
}

// IsError reports wether s is an error.
func (s *ReadWriteSpan) IsError() bool {
	s.span.Lock()
	defer s.span.Unlock()

	return s.span.Error == 1
}

// SetTag adds a set of key/value metadata to the span. Setting metric aggregator tags
// (name, env, service, version, resource, http.status_code and keyMeasured) or modifying
// the sampling priority in the processor is not allowed.
func (s *ReadWriteSpan) SetTag(key string, value interface{}) {
	s.span.Lock()
	defer s.span.Unlock()

	switch key {
	case ext.SpanName, ext.SpanType, ext.ResourceName, ext.ServiceName, ext.HTTPCode, ext.Environment, keyMeasured, keyTopLevel, ext.AnalyticsEvent, ext.EventSampleRate:
		// Client side stats are computed pre-processor, so modifying these fields
		// would lead to inaccurate stats.
		log.Debug("Setting the tag %v in the processor is not allowed", key)
		return
	case ext.ManualKeep, ext.ManualDrop, ext.SamplingPriority, keySamplingPriority:
		// Returning is not necessary, as the call to setSamplingPriorityLocked is
		// a no-op on finished spans. Adding this case for the purpose of logging
		// that this is not allowed.
		log.Debug("Setting sampling priority tag %v in the processor is not allowed", key)
		return
	default:
		s.span.setTagLocked(key, value)
	}
}

// finishTrace pushes finished spans from a trace to the processor, and returns
// the modified trace or nil if the trace should be dropped.
func (tr *tracer) finishTrace(spans []*span) []*span {
	if tr.config.onFinish == nil {
		return spans
	}
	modifiedTrace := tr.config.onFinish(newReadWriteSpanSlice(spans))
	var newTrace []*span
	for _, s := range modifiedTrace {
		if s.span != nil {
			newTrace = append(newTrace, s.span)
		}
	}
	if len(newTrace) == 0 {
		atomic.AddUint64(&tr.droppedProcessorSpans, uint64(len(spans)))
		atomic.AddUint64(&tr.droppedProcessorTraces, 1)
		return nil
	}
	if droppedSpans := len(spans) - len(newTrace); droppedSpans > 0 {
		atomic.AddUint64(&tr.droppedProcessorSpans, uint64(droppedSpans))
	}
	return newTrace
}

// newReadWriteSpanSlice copies the elements of slice spans to the
// destination slice of type ReadWriteSpan to be fed to the processor.
func newReadWriteSpanSlice(spans []*span) []ReadWriteSpan {
	rwSlice := make([]ReadWriteSpan, len(spans))
	for i, span := range spans {
		rwSlice[i] = ReadWriteSpan{span}
	}
	return rwSlice
}
