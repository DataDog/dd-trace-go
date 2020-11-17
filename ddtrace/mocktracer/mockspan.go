// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package mocktracer // import "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

import (
	"fmt"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

var _ ddtrace.Span = (*mockspan)(nil)
var _ Span = (*mockspan)(nil)

// Span is an interface that allows querying a span returned by the mock tracer.
type Span interface {
	// SpanID returns the span's ID.
	SpanID() uint64

	// TraceID returns the span's trace ID.
	TraceID() uint64

	// ParentID returns the span's parent ID.
	ParentID() uint64

	// StartTime returns the time when the span has started.
	StartTime() time.Time

	// FinishTime returns the time when the span has finished.
	FinishTime() time.Time

	// OperationName returns the operation name held by this span.
	OperationName() string

	// Tag returns the value of the tag at key k.
	Tag(k string) interface{}

	// Tags returns a copy of all the tags in this span.
	Tags() map[string]interface{}

	// Context returns the span's SpanContext.
	Context() ddtrace.SpanContext

	// Stringer allows pretty-printing the span's fields for debugging.
	fmt.Stringer
}

func newSpan(t *mocktracer, operationName string, cfg *ddtrace.StartSpanConfig) *mockspan {
	if cfg.Tags == nil {
		cfg.Tags = make(map[string]interface{})
	}
	if cfg.Tags[ext.ResourceName] == nil {
		cfg.Tags[ext.ResourceName] = operationName
	}
	s := &mockspan{
		name:   operationName,
		tracer: t,
	}
	if cfg.StartTime.IsZero() {
		s.startTime = time.Now()
	} else {
		s.startTime = cfg.StartTime
	}
	id := cfg.SpanID
	if id == 0 {
		id = nextID()
	}
	s.context = &spanContext{spanID: id, traceID: id, span: s}
	if ctx, ok := cfg.Parent.(*spanContext); ok {
		if ctx.span != nil && s.tags[ext.ServiceName] == nil {
			// if we have a local parent and no service, inherit the parent's
			s.SetTag(ext.ServiceName, ctx.span.Tag(ext.ServiceName))
		}
		if ctx.hasSamplingPriority() {
			s.SetTag(ext.SamplingPriority, ctx.samplingPriority())
		}
		s.parentID = ctx.spanID
		s.context.priority = ctx.samplingPriority()
		s.context.hasPriority = ctx.hasSamplingPriority()
		s.context.traceID = ctx.traceID
		s.context.baggage = make(map[string]string, len(ctx.baggage))
		ctx.ForeachBaggageItem(func(k, v string) bool {
			s.context.baggage[k] = v
			return true
		})
	}
	for k, v := range cfg.Tags {
		s.SetTag(k, v)
	}
	return s
}

type mockspan struct {
	sync.RWMutex // guards below fields
	name         string
	tags         map[string]interface{}
	finishTime   time.Time
	finished     bool

	startTime time.Time
	parentID  uint64
	context   *spanContext
	tracer    *mocktracer
}

// SetTag sets a given tag on the span.
func (s *mockspan) SetTag(key string, value interface{}) {
	s.Lock()
	defer s.Unlock()
	if s.finished {
		return
	}
	if s.tags == nil {
		s.tags = make(map[string]interface{}, 1)
	}
	if key == ext.SamplingPriority {
		switch p := value.(type) {
		case int:
			s.context.setSamplingPriority(p)
		case float64:
			s.context.setSamplingPriority(int(p))
		}
	}
	s.tags[key] = value
}

func (s *mockspan) FinishTime() time.Time {
	s.RLock()
	defer s.RUnlock()
	return s.finishTime
}

func (s *mockspan) StartTime() time.Time { return s.startTime }

func (s *mockspan) Tag(k string) interface{} {
	s.RLock()
	defer s.RUnlock()
	return s.tags[k]
}

func (s *mockspan) Tags() map[string]interface{} {
	s.RLock()
	defer s.RUnlock()
	// copy
	cp := make(map[string]interface{}, len(s.tags))
	for k, v := range s.tags {
		cp[k] = v
	}
	return cp
}

func (s *mockspan) TraceID() uint64 { return s.context.traceID }

func (s *mockspan) SpanID() uint64 { return s.context.spanID }

func (s *mockspan) ParentID() uint64 { return s.parentID }

func (s *mockspan) OperationName() string {
	s.RLock()
	defer s.RUnlock()
	return s.name
}

// SetOperationName resets the original operation name to the given one.
func (s *mockspan) SetOperationName(operationName string) {
	s.Lock()
	defer s.Unlock()
	s.name = operationName
	return
}

// BaggageItem returns the baggage item with the given key.
func (s *mockspan) BaggageItem(key string) string {
	return s.context.baggageItem(key)
}

// SetBaggageItem sets a new baggage item at the given key. The baggage
// item should propagate to all descendant spans, both in- and cross-process.
func (s *mockspan) SetBaggageItem(key, val string) {
	s.context.setBaggageItem(key, val)
	return
}

// Finish finishes the current span with the given options.
func (s *mockspan) Finish(opts ...ddtrace.FinishOption) {
	var cfg ddtrace.FinishConfig
	for _, fn := range opts {
		fn(&cfg)
	}
	var t time.Time
	if cfg.FinishTime.IsZero() {
		t = time.Now()
	} else {
		t = cfg.FinishTime
	}
	if cfg.Error != nil {
		s.SetTag(ext.Error, cfg.Error)
	}
	if cfg.NoDebugStack {
		s.SetTag(ext.ErrorStack, "<debug stack disabled>")
	}
	s.Lock()
	defer s.Unlock()
	if s.finished {
		return
	}
	s.finished = true
	s.finishTime = t
	s.tracer.removeUnfinishedSpan(s)
	s.tracer.addFinishedSpan(s)
}

// String implements fmt.Stringer.
func (s *mockspan) String() string {
	sc := s.context
	return fmt.Sprintf(`
name: %s
tags: %#v
start: %s
finish: %s
id: %d
parent: %d
trace: %d
baggage: %#v
`, s.name, s.tags, s.startTime, s.finishTime, sc.spanID, s.parentID, sc.traceID, sc.baggage)
}

// Context returns the SpanContext of this Span.
func (s *mockspan) Context() ddtrace.SpanContext { return s.context }
